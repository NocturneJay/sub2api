package middleware

import (
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/stretchr/testify/require"
)

func TestSubscriptionLimitRetryAfterSeconds_WindowResets(t *testing.T) {
	now := time.Date(2026, 7, 27, 10, 0, 0, 0, time.UTC)
	dayStart := now.Add(-20 * time.Hour)        // 日窗口 4 小时后重置
	weekStart := now.Add(-6 * 24 * time.Hour)   // 周窗口 1 天后重置
	monthStart := now.Add(-29 * 24 * time.Hour) // 月窗口 1 天后重置
	sub := &service.UserSubscription{
		// StartsAt/ExpiresAt 拉开超过 1 天，避免命中 HasOneTimeDailyQuota 分支
		StartsAt:           now.AddDate(0, 0, -10),
		ExpiresAt:          now.AddDate(0, 0, 20),
		DailyWindowStart:   &dayStart,
		WeeklyWindowStart:  &weekStart,
		MonthlyWindowStart: &monthStart,
	}

	require.Equal(t, 4*3600, subscriptionLimitRetryAfterSeconds(sub, service.ErrDailyLimitExceeded, now))
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
