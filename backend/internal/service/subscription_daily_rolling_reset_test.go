package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 本文件取代上游 v0.1.172 随 99b357083 加入的 subscription_daily_midnight_reset_test.go。
//
// 那一组用例断言的是「日额度每天 0 点刷新」——正是 aicat 刻意不采用的语义
// （理由见 user_subscription.go 的 automaticDailyWindowStartAt）。按交接文件
// §3 的规矩，断言被刻意消除之行为的上游用例不移植，改写成自研口径。
//
// 这里覆盖的是 24 小时滚动窗口：
//   - 满 24 小时才刷新，跨 0 点但不满 24 小时不刷新
//   - 新窗口起点是「锚点 + 整数个 24h」，不是当天 0 点
//   - 手动重置重新锚定，下一份 24 小时之后
//   - **N 天卡恰好发 N 份日额度**（这条是整个分歧的经济理由）
//   - 天卡仍走一次性额度豁免

type dailyRollingResetRepo struct {
	userSubRepoNoop

	resetCalled    bool
	newWindowStart time.Time
}

func (r *dailyRollingResetRepo) ResetDailyUsage(_ context.Context, _ int64, _ *time.Time, newWindowStart time.Time) error {
	r.resetCalled = true
	r.newWindowStart = newWindowStart
	return nil
}

func rollingTestBase() time.Time {
	return time.Date(2026, 8, 6, 9, 17, 0, 0, time.UTC)
}

// newRollingTestSub 造一个多日订阅，窗口锚点由调用方指定。
// StartsAt 固定比锚点早很多，避免触发 automaticWindowStartAt 里
// 「遗留 0 点锚点提升为 StartsAt」的分支干扰断言。
func newRollingTestSub(dailyWindowStart time.Time, base time.Time) *UserSubscription {
	start := dailyWindowStart
	return &UserSubscription{
		ID:               1,
		UserID:           10,
		GroupID:          20,
		StartsAt:         base.AddDate(0, 0, -3),
		ExpiresAt:        base.AddDate(0, 0, 30),
		DailyUsageUSD:    43.34,
		DailyWindowStart: &start,
	}
}

// 跨了 0 点但不满 24 小时——上游口径会刷新，aicat 口径必须不刷新。
// 这条是与上游语义差异最直接的对照。
func TestCheckAndResetWindows_DailyDoesNotResetBefore24hEvenAcrossMidnight(t *testing.T) {
	base := rollingTestBase()
	// 锚点定在傍晚 20:00，再往后 8 小时是次日 04:00：日历日翻了篇，但只过了 8 小时。
	anchor := time.Date(base.Year(), base.Month(), base.Day(), 20, 0, 0, 0, time.UTC)
	repo := &dailyRollingResetRepo{}
	svc := &SubscriptionService{userSubRepo: repo}
	sub := newRollingTestSub(anchor, base)

	svc.now = func() time.Time { return anchor.Add(8 * time.Hour) }
	require.NotEqual(t, anchor.Day(), svc.now().Day(), "前置条件：确实跨了 0 点")

	require.NoError(t, svc.CheckAndResetWindows(context.Background(), sub))
	require.False(t, repo.resetCalled, "跨 0 点但不满 24 小时，不得刷新日额度")
	require.Equal(t, 43.34, sub.DailyUsageUSD, "用量不应被清零")
}

// 满 24 小时后刷新，且新窗口起点是锚点 + 24h，不是当天 0 点。
func TestCheckAndResetWindows_DailyResetsOn24hRollingBoundary(t *testing.T) {
	base := rollingTestBase()
	anchor := time.Date(base.Year(), base.Month(), base.Day(), 20, 0, 0, 0, time.UTC)
	repo := &dailyRollingResetRepo{}
	svc := &SubscriptionService{userSubRepo: repo}
	sub := newRollingTestSub(anchor, base)

	svc.now = func() time.Time { return anchor.Add(24*time.Hour + time.Minute) }

	require.NoError(t, svc.CheckAndResetWindows(context.Background(), sub))
	require.True(t, repo.resetCalled)
	require.Equal(t, anchor.Add(24*time.Hour), repo.newWindowStart,
		"新窗口起点应为锚点+24h，而不是当天 0 点")
	require.NotEqual(t, startOfDay(svc.now()), repo.newWindowStart)
}

// 面板显示的「下次刷新时间」必须与实际刷新时刻同口径。
func TestDailyResetTime_IsAnchorPlus24hForMultiDaySubscription(t *testing.T) {
	base := rollingTestBase()
	anchor := time.Date(base.Year(), base.Month(), base.Day(), 20, 0, 0, 0, time.UTC)
	sub := newRollingTestSub(anchor, base)

	resetAt := sub.DailyResetTime()
	require.NotNil(t, resetAt)
	require.Equal(t, anchor.Add(24*time.Hour), *resetAt)
	require.True(t, sub.NeedsDailyResetAt(*resetAt), "到了显示的时刻就应当真的可以刷新")
	require.False(t, sub.NeedsDailyResetAt(resetAt.Add(-time.Second)), "差一秒不得刷新")
}

// **这条是整个分歧存在的理由。** 日历日口径下 N 天卡会跨 N+1 个自然日、
// 发 N+1 份日额度；滚动口径必须恰好 N 份。
func TestDailyWindows_NDayCardYieldsExactlyNDailyWindows(t *testing.T) {
	const days = 30
	// 下午买的卡——这正是日历日口径多发一份的场景。
	startsAt := time.Date(2026, 7, 16, 18, 57, 0, 0, time.UTC)
	anchor := startsAt
	sub := &UserSubscription{
		ID:               1,
		StartsAt:         startsAt,
		ExpiresAt:        startsAt.AddDate(0, 0, days),
		DailyWindowStart: &anchor,
	}
	require.False(t, sub.HasOneTimeDailyQuota(), "30 天卡不是一次性额度")

	// 从买入时刻起每小时推进一次，数一共发生过几次窗口推进。
	windows := 1 // 买入即持有第一份
	current := anchor
	for cursor := startsAt; cursor.Before(sub.ExpiresAt); cursor = cursor.Add(time.Hour) {
		next, ok := sub.automaticDailyWindowStartAt(cursor)
		if ok && next.After(current) {
			windows++
			current = next
			sub.DailyWindowStart = &current
		}
	}

	require.Equal(t, days, windows,
		"%d 天卡必须恰好发 %d 份日额度；日历日口径会发 %d 份（多送一份）", days, days, days+1)
}

// 手动重置＝立即发一份新额度，因此重新锚定，下一份 24 小时之后。
func TestNeedsDailyReset_ManualResetReanchorsRollingWindow(t *testing.T) {
	base := rollingTestBase()
	manualResetAt := base.Add(16*time.Hour + 49*time.Minute)
	sub := newRollingTestSub(manualResetAt, base)

	require.False(t, sub.NeedsDailyResetAt(manualResetAt.Add(23*time.Hour)),
		"手动重置后 23 小时内不得再刷新")
	require.True(t, sub.NeedsDailyResetAt(manualResetAt.Add(24*time.Hour)),
		"手动重置满 24 小时后刷新")
}

// 天卡（到期 ≤ 开始+1 天）仍是一次性额度：无论跨不跨 0 点、满不满 24 小时都不刷新。
// 这一条与上游一致，是挡住「23:55 买、00:05 再领一份」的关键。
func TestCheckAndResetWindows_OneTimeDailyCardNeverRefreshes(t *testing.T) {
	// 贴着 0 点买入——日历日口径下最坏的情形。
	startsAt := time.Date(2026, 8, 8, 23, 34, 0, 0, time.UTC)
	anchor := startsAt
	repo := &dailyRollingResetRepo{}
	svc := &SubscriptionService{userSubRepo: repo}
	sub := &UserSubscription{
		ID:               1,
		StartsAt:         startsAt,
		ExpiresAt:        startsAt.AddDate(0, 0, 1),
		DailyUsageUSD:    20,
		DailyWindowStart: &anchor,
	}
	require.True(t, sub.HasOneTimeDailyQuota())

	for _, at := range []time.Time{
		startsAt.Add(30 * time.Minute), // 跨 0 点
		startsAt.Add(12 * time.Hour),
		startsAt.Add(23 * time.Hour),
	} {
		svc.now = func() time.Time { return at }
		require.NoError(t, svc.CheckAndResetWindows(context.Background(), sub))
		require.False(t, repo.resetCalled, "天卡在 %v 不得刷新日额度", at)
		require.Equal(t, 20.0, sub.DailyUsageUSD)
	}
}
