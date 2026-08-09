package service

import "time"

const subscriptionDayDuration = 24 * time.Hour

type UserSubscription struct {
	ID      int64
	UserID  int64
	GroupID int64

	StartsAt  time.Time
	ExpiresAt time.Time
	Status    string

	DailyWindowStart   *time.Time
	WeeklyWindowStart  *time.Time
	MonthlyWindowStart *time.Time

	DailyUsageUSD   float64
	WeeklyUsageUSD  float64
	MonthlyUsageUSD float64

	AssignedBy *int64
	AssignedAt time.Time
	Notes      string

	CreatedAt time.Time
	UpdatedAt time.Time
	DeletedAt *time.Time

	User           *User
	Group          *Group
	AssignedByUser *User
}

func (s *UserSubscription) IsActive() bool {
	return s.Status == SubscriptionStatusActive && time.Now().Before(s.ExpiresAt)
}

func (s *UserSubscription) IsExpired() bool {
	return time.Now().After(s.ExpiresAt)
}

func (s *UserSubscription) DaysRemaining() int {
	return s.daysRemainingAt(time.Now())
}

func (s *UserSubscription) daysRemainingAt(now time.Time) int {
	remaining := s.ExpiresAt.Sub(now)
	if remaining <= 0 {
		return 0
	}

	days := int(remaining / subscriptionDayDuration)
	if remaining%subscriptionDayDuration != 0 {
		days++
	}
	return days
}

func (s *UserSubscription) IsWindowActivated() bool {
	return s.DailyWindowStart != nil || s.WeeklyWindowStart != nil || s.MonthlyWindowStart != nil
}

func (s *UserSubscription) HasOneTimeDailyQuota() bool {
	if s == nil || s.StartsAt.IsZero() || s.ExpiresAt.IsZero() {
		return false
	}
	return !s.ExpiresAt.After(s.StartsAt.AddDate(0, 0, 1))
}

func (s *UserSubscription) NeedsDailyReset() bool {
	return s.NeedsDailyResetAt(time.Now())
}

func (s *UserSubscription) NeedsDailyResetAt(now time.Time) bool {
	_, ok := s.automaticDailyWindowStartAt(now)
	return ok
}

func (s *UserSubscription) NeedsWeeklyReset() bool {
	return s.NeedsWeeklyResetAt(time.Now())
}

func (s *UserSubscription) NeedsWeeklyResetAt(now time.Time) bool {
	if s.WeeklyWindowStart == nil {
		return false
	}
	return !now.Before(s.WeeklyWindowStart.Add(7 * 24 * time.Hour))
}

func (s *UserSubscription) NeedsMonthlyReset() bool {
	return s.NeedsMonthlyResetAt(time.Now())
}

func (s *UserSubscription) NeedsMonthlyResetAt(now time.Time) bool {
	if s.MonthlyWindowStart == nil {
		return false
	}
	return !now.Before(s.MonthlyWindowStart.Add(30 * 24 * time.Hour))
}

func (s *UserSubscription) canAutomaticallyResetDailyAt(now time.Time) bool {
	_, ok := s.automaticDailyWindowStartAt(now)
	return ok
}

// automaticDailyWindowStartAt 计算日窗口的当前起点。
//
// ⚠️ **aicat 与上游的有意分歧，同步时勿改回。** 上游 `99b357083` 把日额度改成
// 「配置时区每天 0 点刷新」；aicat 保留 **24 小时滚动窗口**，与周/月一致。
//
// 理由是产品口径不同，不是技术优劣：
//  1. 本站的「天卡 / 日额度」向用户承诺的就是「24 小时」，不是自然日。
//  2. 日历日口径下，N 天卡会跨 N+1 个自然日、发出 **N+1 份**日额度。实测线上
//     月卡（07-16 18:57 买、08-15 18:57 到期）跨 31 个自然日 = 31 份 $60 日额度，
//     比卖出去的多一份。既不符合用户预期，也让站点白送一份额度。
//  3. 上游列的两条「回归」——手动重置后锚点移到重置时刻、新建/续费按购买时刻
//     滚动——在 24 小时口径下都是**正确行为**，只有相对「日历日」这个意图才算 bug。
//
// **刻意保留上游的函数名与签名**：这样 NeedsDailyResetAt /
// canAutomaticallyResetDailyAt / CheckAndResetWindows 三个调用点与上游逐字一致，
// 分歧收敛在「这一个函数体 + subscription_service.go 里三处锚点写入」，
// 未来同步的冲突点可预期。CI 里有源码断言钉死，见 netcup_ci_build_*.sh。
//
// 天卡（到期 ≤ 开始+1 天）仍走 HasOneTimeDailyQuota 豁免：整张卡一份额度、永不刷新，
// 这与「天卡就是 24 小时」的承诺一致，也挡住了「23:55 买、00:05 再领一份」。
func (s *UserSubscription) automaticDailyWindowStartAt(now time.Time) (time.Time, bool) {
	if s.HasOneTimeDailyQuota() {
		return time.Time{}, false
	}
	return s.automaticWindowStartAt(s.DailyWindowStart, 24*time.Hour, now)
}

func (s *UserSubscription) canAutomaticallyResetWeeklyAt(now time.Time) bool {
	_, ok := s.automaticWindowStartAt(s.WeeklyWindowStart, 7*24*time.Hour, now)
	return ok
}

func (s *UserSubscription) canAutomaticallyResetMonthlyAt(now time.Time) bool {
	_, ok := s.automaticWindowStartAt(s.MonthlyWindowStart, 30*24*time.Hour, now)
	return ok
}

// automaticWindowStartAt 计算期限对齐滚动窗口的当前起点。
// 窗口从锚点按整数个 period 步进，且不越过订阅到期时间，避免最后一个不完整
// 周期重复发放额度（issue #5051）。
//
// aicat 侧**日/周/月三个窗口都走这个函数**（上游只有周/月走）：日窗口经
// automaticDailyWindowStartAt 以 24h 为 period 调用。上面那条「不越过到期时间」
// 的约束正是日窗口不出现 N+1 泄漏的原因，别按「日窗口不需要」把它简化掉。
func (s *UserSubscription) automaticWindowStartAt(previous *time.Time, period time.Duration, now time.Time) (time.Time, bool) {
	if previous == nil {
		return time.Time{}, false
	}

	anchor := *previous
	// Older subscriptions initialized their first windows at midnight on their
	// start date. Only that initial value is unambiguous; later midnight anchors
	// may be manual resets and must remain authoritative.
	legacyAnchor := startOfDay(s.StartsAt)
	if legacyAnchor.Before(s.StartsAt) && anchor.Equal(legacyAnchor) {
		anchor = s.StartsAt
	}
	next := anchor.Add(period)
	if now.Before(next) || !next.Before(s.ExpiresAt) {
		return time.Time{}, false
	}

	periods := now.Sub(anchor) / period
	lastPeriodBeforeExpiry := (s.ExpiresAt.Sub(anchor) - 1) / period
	if periods > lastPeriodBeforeExpiry {
		periods = lastPeriodBeforeExpiry
	}
	return anchor.Add(periods * period), true
}

func (s *UserSubscription) DailyResetTime() *time.Time {
	if s.DailyWindowStart == nil {
		return nil
	}
	if s.HasOneTimeDailyQuota() {
		t := s.ExpiresAt
		return &t
	}
	// aicat 分歧：日窗口 24 小时滚动，下次刷新 = 窗口起点 + 24h（不是次日 0 点）。
	// 这里必须与 automaticDailyWindowStartAt 同口径——它是用户在面板看到的
	// 「下次刷新时间」，两边不一致会表现为「显示的时刻到了却没刷新」。
	t := s.DailyWindowStart.Add(24 * time.Hour)
	return &t
}

func (s *UserSubscription) WeeklyResetTime() *time.Time {
	if s.WeeklyWindowStart == nil {
		return nil
	}
	t := s.WeeklyWindowStart.Add(7 * 24 * time.Hour)
	return &t
}

func (s *UserSubscription) MonthlyResetTime() *time.Time {
	if s.MonthlyWindowStart == nil {
		return nil
	}
	t := s.MonthlyWindowStart.Add(30 * 24 * time.Hour)
	return &t
}

func (s *UserSubscription) CheckDailyLimit(group *Group, additionalCost float64) bool {
	if !group.HasDailyLimit() {
		return true
	}
	return s.DailyUsageUSD+additionalCost <= *group.DailyLimitUSD
}

func (s *UserSubscription) CheckWeeklyLimit(group *Group, additionalCost float64) bool {
	if !group.HasWeeklyLimit() {
		return true
	}
	return s.WeeklyUsageUSD+additionalCost <= *group.WeeklyLimitUSD
}

func (s *UserSubscription) CheckMonthlyLimit(group *Group, additionalCost float64) bool {
	if !group.HasMonthlyLimit() {
		return true
	}
	return s.MonthlyUsageUSD+additionalCost <= *group.MonthlyLimitUSD
}

func (s *UserSubscription) CheckAllLimits(group *Group, additionalCost float64) (daily, weekly, monthly bool) {
	daily = s.CheckDailyLimit(group, additionalCost)
	weekly = s.CheckWeeklyLimit(group, additionalCost)
	monthly = s.CheckMonthlyLimit(group, additionalCost)
	return
}
