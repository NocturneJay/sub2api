package middleware

import (
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/stretchr/testify/require"
)

func TestSubscriptionLimitRetryAfterSeconds_WindowResets(t *testing.T) {
	// 日窗口按**日历日**对齐（上游 v0.1.172 的 99b357083 恢复了这个语义：
	// DailyResetTime = StartOfDay(窗口起点) + 1 天，即窗口起点那天的次日 0 点）。
	//
	// 本用例原来造的是「窗口起点在 20 小时前」并断言 4 小时后重置——那是 v0.1.170
	// 引入的**滚动 24 小时**口径，也正是上游这次要修掉的回归。改成日历日口径后，
	// 20 小时前的起点意味着 0 点刷新时刻早已过去，函数落到 `secs <= 0` 的 60 秒兜底。
	//
	// 更要紧的是：日历日口径**依赖时区**，而 timezone.Location() 未初始化时回落到
	// time.Local。若继续把 now/dayStart 钉死成 UTC，结果就随跑测试的机器时区变化
	// （本机 CDT 下算出的刷新时刻在过去，CI 的 UTC 容器下同样在过去）。
	// 因此这里显式在**配置时区**里构造时刻：起点与 now 落在同一个日历日，
	// 次日 0 点必然在 14 小时后，任何时区下都成立。
	loc := timezone.Location()
	now := time.Date(2026, 7, 27, 10, 0, 0, 0, loc)
	dayStart := now.Add(-8 * time.Hour)         // 同一日历日内 02:00，次日 0 点在 14 小时后
	weekStart := now.Add(-6 * 24 * time.Hour)   // 周窗口仍是滚动 7 天：1 天后重置
	monthStart := now.Add(-29 * 24 * time.Hour) // 月窗口仍是滚动 30 天：1 天后重置
	sub := &service.UserSubscription{
		// StartsAt/ExpiresAt 拉开超过 1 天，避免命中 HasOneTimeDailyQuota 分支
		StartsAt:           now.AddDate(0, 0, -10),
		ExpiresAt:          now.AddDate(0, 0, 20),
		DailyWindowStart:   &dayStart,
		WeeklyWindowStart:  &weekStart,
		MonthlyWindowStart: &monthStart,
	}

	require.Equal(t, 14*3600, subscriptionLimitRetryAfterSeconds(sub, service.ErrDailyLimitExceeded, now))
	require.Equal(t, 24*3600, subscriptionLimitRetryAfterSeconds(sub, service.ErrWeeklyLimitExceeded, now))
	require.Equal(t, 24*3600, subscriptionLimitRetryAfterSeconds(sub, service.ErrMonthlyLimitExceeded, now))
}

func TestSubscriptionLimitRetryAfterSeconds_OneTimeDailyQuotaUsesExpiry(t *testing.T) {
	// 一次性日卡（有效期 ≤1 天）：日窗口不滚动，重置时刻即订阅到期时刻。
	now := time.Date(2026, 7, 27, 10, 0, 0, 0, time.UTC)
	dayStart := now.Add(-2 * time.Hour)
	sub := &service.UserSubscription{
		StartsAt:         now.Add(-2 * time.Hour),
		ExpiresAt:        now.Add(10 * time.Hour),
		DailyWindowStart: &dayStart,
	}
	require.Equal(t, 10*3600, subscriptionLimitRetryAfterSeconds(sub, service.ErrDailyLimitExceeded, now))
}

func TestSubscriptionLimitRetryAfterSeconds_EdgeCases(t *testing.T) {
	now := time.Date(2026, 7, 27, 10, 0, 0, 0, time.UTC)

	// nil 订阅 → 0（不设头）
	require.Equal(t, 0, subscriptionLimitRetryAfterSeconds(nil, service.ErrDailyLimitExceeded, now))

	// 窗口未激活（重置时间不可知）→ 0（不设头）
	inactive := &service.UserSubscription{
		StartsAt:  now.AddDate(0, 0, -10),
		ExpiresAt: now.AddDate(0, 0, 20),
	}
	require.Equal(t, 0, subscriptionLimitRetryAfterSeconds(inactive, service.ErrDailyLimitExceeded, now))

	// 非限额错误 → 0
	dayStart := now.Add(-20 * time.Hour)
	sub := &service.UserSubscription{
		StartsAt:         now.AddDate(0, 0, -10),
		ExpiresAt:        now.AddDate(0, 0, 20),
		DailyWindowStart: &dayStart,
	}
	require.Equal(t, 0, subscriptionLimitRetryAfterSeconds(sub, errors.New("other"), now))

	// 重置时间已过（窗口维护竞态）→ 60s fallback 而非 1s 紧循环
	stale := now.Add(-25 * time.Hour)
	subStale := &service.UserSubscription{
		StartsAt:         now.AddDate(0, 0, -10),
		ExpiresAt:        now.AddDate(0, 0, 20),
		DailyWindowStart: &stale,
	}
	require.Equal(t, subscriptionLimitRetryAfterFallbackSeconds,
		subscriptionLimitRetryAfterSeconds(subStale, service.ErrDailyLimitExceeded, now))
}
