package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 首单双向奖励的服务层用例。全部走桩，不碰数据库。
//
// 两个桩都用「嵌入接口」的写法：只实现本组用例真正会用到的方法，其余方法
// 保持 nil，一旦被调到就 panic —— 这正是我们想要的信号（首单奖励路径不该
// 顺手去碰返利记录、转余额之类的东西）。

type firstOrderBonusSettingRepoStub struct {
	SettingRepository
	values map[string]string
}

func (s *firstOrderBonusSettingRepoStub) GetValue(_ context.Context, key string) (string, error) {
	if value, ok := s.values[key]; ok {
		return value, nil
	}
	return "", ErrSettingNotFound
}

type firstOrderBonusRepoStub struct {
	AffiliateRepository

	ensureSummary *AffiliateSummary
	ensureErr     error
	readOnly      *AffiliateSummary
	readOnlyErr   error
	record        *AffiliateFirstOrderBonusRecord
	recordErr     error
	hasOther      bool
	hasOtherErr   error
	applyResult   bool
	applyErr      error

	ensureCalls    []int64
	readOnlyCalls  []int64
	lockCalls      []int64
	hasOtherCalls  [][2]int64
	voidCalls      []AffiliateFirstOrderBonusVoidInput
	applyCalls     []AffiliateFirstOrderBonusApplyInput
	getRecordCalls []int64
}

func (r *firstOrderBonusRepoStub) EnsureUserAffiliate(_ context.Context, userID int64) (*AffiliateSummary, error) {
	r.ensureCalls = append(r.ensureCalls, userID)
	if r.ensureErr != nil {
		return nil, r.ensureErr
	}
	if r.ensureSummary == nil {
		return &AffiliateSummary{UserID: userID, AffCode: "SELF", CreatedAt: time.Now().Add(-time.Hour)}, nil
	}
	cp := *r.ensureSummary
	return &cp, nil
}

func (r *firstOrderBonusRepoStub) GetUserAffiliateReadOnly(_ context.Context, userID int64) (*AffiliateSummary, error) {
	r.readOnlyCalls = append(r.readOnlyCalls, userID)
	if r.readOnlyErr != nil {
		return nil, r.readOnlyErr
	}
	if r.readOnly == nil {
		return nil, nil
	}
	cp := *r.readOnly
	return &cp, nil
}

func (r *firstOrderBonusRepoStub) GetFirstOrderBonusRecord(_ context.Context, userID int64) (*AffiliateFirstOrderBonusRecord, error) {
	r.getRecordCalls = append(r.getRecordCalls, userID)
	if r.recordErr != nil {
		return nil, r.recordErr
	}
	if r.record == nil {
		return nil, nil
	}
	cp := *r.record
	return &cp, nil
}

func (r *firstOrderBonusRepoStub) HasEarlierOrFulfilledPaymentOrder(_ context.Context, userID, excludeOrderID int64) (bool, error) {
	r.hasOtherCalls = append(r.hasOtherCalls, [2]int64{userID, excludeOrderID})
	if r.hasOtherErr != nil {
		return false, r.hasOtherErr
	}
	return r.hasOther, nil
}

func (r *firstOrderBonusRepoStub) LockUserAffiliateForUpdate(_ context.Context, userID int64) error {
	r.lockCalls = append(r.lockCalls, userID)
	return nil
}

func (r *firstOrderBonusRepoStub) RecordFirstOrderBonusVoid(_ context.Context, in AffiliateFirstOrderBonusVoidInput) (bool, error) {
	r.voidCalls = append(r.voidCalls, in)
	return true, nil
}

func (r *firstOrderBonusRepoStub) ApplyFirstOrderBonus(_ context.Context, in AffiliateFirstOrderBonusApplyInput) (bool, error) {
	r.applyCalls = append(r.applyCalls, in)
	if r.applyErr != nil {
		return false, r.applyErr
	}
	return r.applyResult, nil
}

// newFirstOrderBonusService 组装一个「功能开启、阈值 20、双方各 10、有效期 30 天」
// 的 AffiliateService；overrides 用来覆盖单个 settings 键。
func newFirstOrderBonusService(repo AffiliateRepository, overrides map[string]string) *AffiliateService {
	values := map[string]string{
		SettingKeyAffiliateEnabled:             "true",
		SettingKeyAffiliateRebateFreezeHours:   "0",
		SettingKeyAffiliateFirstOrderBonus:     `{"enabled":true,"threshold":20,"invitee_bonus":10,"inviter_bonus":10,"valid_days":30}`,
		SettingKeyAffiliateRebateRate:          "20",
		SettingKeyAffiliateRebatePerInviteeCap: "0",
		SettingKeyAffiliateRebateDurationDays:  "0",
	}
	for k, v := range overrides {
		values[k] = v
	}
	settingSvc := NewSettingService(&firstOrderBonusSettingRepoStub{values: values}, nil)
	return NewAffiliateService(repo, settingSvc, nil, nil)
}

func firstOrderBonusInt64Ptr(v int64) *int64 { return &v }

// ---------------------------------------------------------------------------
// ApplyFirstOrderBonusForOrder
// ---------------------------------------------------------------------------

func TestApplyFirstOrderBonusForOrderDisabledWhenFeatureOff(t *testing.T) {
	repo := &firstOrderBonusRepoStub{}
	svc := newFirstOrderBonusService(repo, map[string]string{
		SettingKeyAffiliateFirstOrderBonus: `{"enabled":false,"threshold":20,"invitee_bonus":10,"inviter_bonus":10,"valid_days":30}`,
	})

	outcome, err := svc.ApplyFirstOrderBonusForOrder(context.Background(), 42, 7, 100)
	require.NoError(t, err)
	require.False(t, outcome.Applied)
	require.Equal(t, "disabled", outcome.Reason)
	// 关着的时候一次库都不该查。
	require.Empty(t, repo.ensureCalls)
	require.Empty(t, repo.lockCalls)
}

func TestApplyFirstOrderBonusForOrderDisabledWhenAffiliateMasterSwitchOff(t *testing.T) {
	repo := &firstOrderBonusRepoStub{}
	svc := newFirstOrderBonusService(repo, map[string]string{SettingKeyAffiliateEnabled: "false"})

	outcome, err := svc.ApplyFirstOrderBonusForOrder(context.Background(), 42, 7, 100)
	require.NoError(t, err)
	require.Equal(t, "disabled", outcome.Reason)
	require.Empty(t, repo.ensureCalls)
}

func TestApplyFirstOrderBonusForOrderNoInviter(t *testing.T) {
	repo := &firstOrderBonusRepoStub{
		ensureSummary: &AffiliateSummary{UserID: 42, AffCode: "SELF", CreatedAt: time.Now().Add(-time.Hour)},
	}
	svc := newFirstOrderBonusService(repo, nil)

	outcome, err := svc.ApplyFirstOrderBonusForOrder(context.Background(), 42, 7, 100)
	require.NoError(t, err)
	require.False(t, outcome.Applied)
	require.Equal(t, "no_inviter", outcome.Reason)
	require.Equal(t, []int64{42}, repo.ensureCalls)
	// 没有邀请人就不必锁行。
	require.Empty(t, repo.lockCalls)
}

func TestApplyFirstOrderBonusForOrderAlreadyRecorded(t *testing.T) {
	repo := &firstOrderBonusRepoStub{
		ensureSummary: &AffiliateSummary{UserID: 42, InviterID: firstOrderBonusInt64Ptr(9), CreatedAt: time.Now().Add(-time.Hour)},
		record:        &AffiliateFirstOrderBonusRecord{UserID: 42, Status: FirstOrderBonusStatusVoidBelowThreshold},
	}
	svc := newFirstOrderBonusService(repo, nil)

	outcome, err := svc.ApplyFirstOrderBonusForOrder(context.Background(), 42, 7, 100)
	require.NoError(t, err)
	require.False(t, outcome.Applied)
	require.Equal(t, "already_recorded", outcome.Reason)
	// 锁必须在读记录之前，否则并发首单会同时读到「没有记录」。
	require.Equal(t, []int64{42}, repo.lockCalls)
	require.Equal(t, []int64{42}, repo.getRecordCalls)
	require.Empty(t, repo.hasOtherCalls)
	require.Empty(t, repo.applyCalls)
}

func TestApplyFirstOrderBonusForOrderNotFirstOrderDoesNotRecord(t *testing.T) {
	repo := &firstOrderBonusRepoStub{
		ensureSummary: &AffiliateSummary{UserID: 42, InviterID: firstOrderBonusInt64Ptr(9), CreatedAt: time.Now().Add(-time.Hour)},
		hasOther:      true,
	}
	svc := newFirstOrderBonusService(repo, nil)

	outcome, err := svc.ApplyFirstOrderBonusForOrder(context.Background(), 42, 7, 100)
	require.NoError(t, err)
	require.False(t, outcome.Applied)
	require.Equal(t, "not_first_order", outcome.Reason)
	require.Equal(t, [][2]int64{{42, 7}}, repo.hasOtherCalls)
	// 不是首单就什么都不落表：券可能还挂在真正的那笔首单上。
	require.Empty(t, repo.voidCalls)
	require.Empty(t, repo.applyCalls)
}

func TestApplyFirstOrderBonusForOrderExpiredRecordsVoid(t *testing.T) {
	repo := &firstOrderBonusRepoStub{
		ensureSummary: &AffiliateSummary{UserID: 42, InviterID: firstOrderBonusInt64Ptr(9), CreatedAt: time.Now().Add(-40 * 24 * time.Hour)},
	}
	svc := newFirstOrderBonusService(repo, nil)

	outcome, err := svc.ApplyFirstOrderBonusForOrder(context.Background(), 42, 7, 100)
	require.NoError(t, err)
	require.False(t, outcome.Applied)
	require.Equal(t, "expired", outcome.Reason)
	require.Len(t, repo.voidCalls, 1)
	require.Equal(t, FirstOrderBonusStatusVoidExpired, repo.voidCalls[0].Status)
	require.Equal(t, int64(42), repo.voidCalls[0].UserID)
	require.Equal(t, int64(7), repo.voidCalls[0].OrderID)
	require.InDelta(t, 100.0, repo.voidCalls[0].OrderAmount, 1e-9)
	require.NotNil(t, repo.voidCalls[0].InviterID)
	require.Equal(t, int64(9), *repo.voidCalls[0].InviterID)
	require.Empty(t, repo.applyCalls)
}

func TestApplyFirstOrderBonusForOrderNeverExpiresWhenValidDaysZero(t *testing.T) {
	repo := &firstOrderBonusRepoStub{
		ensureSummary: &AffiliateSummary{UserID: 42, InviterID: firstOrderBonusInt64Ptr(9), CreatedAt: time.Now().Add(-5000 * 24 * time.Hour)},
		applyResult:   true,
	}
	svc := newFirstOrderBonusService(repo, map[string]string{
		SettingKeyAffiliateFirstOrderBonus: `{"enabled":true,"threshold":20,"invitee_bonus":10,"inviter_bonus":10,"valid_days":0}`,
	})

	outcome, err := svc.ApplyFirstOrderBonusForOrder(context.Background(), 42, 7, 100)
	require.NoError(t, err)
	require.True(t, outcome.Applied)
	require.Empty(t, repo.voidCalls)
}

func TestApplyFirstOrderBonusForOrderBelowThresholdRecordsVoid(t *testing.T) {
	repo := &firstOrderBonusRepoStub{
		ensureSummary: &AffiliateSummary{UserID: 42, InviterID: firstOrderBonusInt64Ptr(9), CreatedAt: time.Now().Add(-time.Hour)},
	}
	svc := newFirstOrderBonusService(repo, nil)

	outcome, err := svc.ApplyFirstOrderBonusForOrder(context.Background(), 42, 7, 19.99)
	require.NoError(t, err)
	require.False(t, outcome.Applied)
	require.Equal(t, "below_threshold", outcome.Reason)
	require.Len(t, repo.voidCalls, 1)
	require.Equal(t, FirstOrderBonusStatusVoidBelowThreshold, repo.voidCalls[0].Status)
	require.InDelta(t, 19.99, repo.voidCalls[0].OrderAmount, 1e-9)
	require.Empty(t, repo.applyCalls)
}

func TestApplyFirstOrderBonusForOrderExactThresholdApplies(t *testing.T) {
	// 恰好等于阈值必须发放：作废不可逆，不能让 DECIMAL→float64 的末位误差烧掉券。
	repo := &firstOrderBonusRepoStub{
		ensureSummary: &AffiliateSummary{UserID: 42, InviterID: firstOrderBonusInt64Ptr(9), CreatedAt: time.Now().Add(-time.Hour)},
		applyResult:   true,
	}
	svc := newFirstOrderBonusService(repo, map[string]string{SettingKeyAffiliateRebateFreezeHours: "72"})

	outcome, err := svc.ApplyFirstOrderBonusForOrder(context.Background(), 42, 7, 20)
	require.NoError(t, err)
	require.True(t, outcome.Applied)
	require.Equal(t, "applied", outcome.Reason)
	require.InDelta(t, 10.0, outcome.InviteeBonus, 1e-9)
	require.InDelta(t, 10.0, outcome.InviterBonus, 1e-9)
	require.NotNil(t, outcome.InviterID)
	require.Equal(t, int64(9), *outcome.InviterID)

	require.Len(t, repo.applyCalls, 1)
	in := repo.applyCalls[0]
	require.Equal(t, int64(42), in.UserID)
	require.Equal(t, int64(9), in.InviterID)
	require.Equal(t, int64(7), in.OrderID)
	require.InDelta(t, 20.0, in.OrderAmount, 1e-9)
	require.InDelta(t, 10.0, in.InviteeBonus, 1e-9)
	require.InDelta(t, 10.0, in.InviterBonus, 1e-9)
	// 冻结期沿用常规返利那一套设置。
	require.Equal(t, 72, in.FreezeHours)
	require.Empty(t, repo.voidCalls)
}

func TestApplyFirstOrderBonusForOrderAlreadyClaimed(t *testing.T) {
	repo := &firstOrderBonusRepoStub{
		ensureSummary: &AffiliateSummary{UserID: 42, InviterID: firstOrderBonusInt64Ptr(9), CreatedAt: time.Now().Add(-time.Hour)},
		applyResult:   false, // 主键冲突：并发路径先落了记录
	}
	svc := newFirstOrderBonusService(repo, nil)

	outcome, err := svc.ApplyFirstOrderBonusForOrder(context.Background(), 42, 7, 50)
	require.NoError(t, err)
	require.False(t, outcome.Applied)
	require.Equal(t, "already_claimed", outcome.Reason)
	require.Len(t, repo.applyCalls, 1)
}

func TestApplyFirstOrderBonusForOrderPropagatesRepoError(t *testing.T) {
	sentinel := errors.New("db down")
	repo := &firstOrderBonusRepoStub{
		ensureSummary: &AffiliateSummary{UserID: 42, InviterID: firstOrderBonusInt64Ptr(9), CreatedAt: time.Now().Add(-time.Hour)},
		applyErr:      sentinel,
	}
	svc := newFirstOrderBonusService(repo, nil)

	outcome, err := svc.ApplyFirstOrderBonusForOrder(context.Background(), 42, 7, 50)
	require.ErrorIs(t, err, sentinel)
	require.Nil(t, outcome)
}

// ---------------------------------------------------------------------------
// GetFirstOrderBonusStatus
// ---------------------------------------------------------------------------

func TestGetFirstOrderBonusStatusAppliedRecordWinsEvenWithNullInviter(t *testing.T) {
	appliedAt := time.Now().Add(-3 * time.Hour).UTC()
	repo := &firstOrderBonusRepoStub{
		record: &AffiliateFirstOrderBonusRecord{
			UserID:       42,
			InviterID:    nil, // 邀请人被删号后置 NULL，记录本身仍然有效
			OrderID:      1234,
			OrderAmount:  50,
			InviteeBonus: 10,
			InviterBonus: 10,
			Status:       FirstOrderBonusStatusApplied,
			CreatedAt:    appliedAt,
		},
	}
	svc := newFirstOrderBonusService(repo, nil)

	status, err := svc.GetFirstOrderBonusStatus(context.Background(), 42)
	require.NoError(t, err)
	require.Equal(t, FirstOrderBonusStatusApplied, status.Status)
	require.True(t, status.Enabled)
	require.NotNil(t, status.AppliedAt)
	require.True(t, appliedAt.Equal(*status.AppliedAt))
	require.NotNil(t, status.AppliedOrderID)
	require.Equal(t, int64(1234), *status.AppliedOrderID)
	require.NotNil(t, status.AppliedInviteeBonus)
	require.InDelta(t, 10.0, *status.AppliedInviteeBonus, 1e-9)
	// 记录优先：不再看邀请人、不再看订单。
	require.Empty(t, repo.readOnlyCalls)
	require.Empty(t, repo.hasOtherCalls)
}

func TestGetFirstOrderBonusStatusVoidRecordCarriesNoAppliedFields(t *testing.T) {
	repo := &firstOrderBonusRepoStub{
		record: &AffiliateFirstOrderBonusRecord{
			UserID:    42,
			OrderID:   1234,
			Status:    FirstOrderBonusStatusVoidBelowThreshold,
			CreatedAt: time.Now(),
		},
	}
	svc := newFirstOrderBonusService(repo, nil)

	status, err := svc.GetFirstOrderBonusStatus(context.Background(), 42)
	require.NoError(t, err)
	require.Equal(t, FirstOrderBonusStatusVoidBelowThreshold, status.Status)
	require.Nil(t, status.AppliedAt)
	require.Nil(t, status.AppliedOrderID)
	require.Nil(t, status.AppliedInviteeBonus)
}

func TestGetFirstOrderBonusStatusDisabledStillReturnsConfig(t *testing.T) {
	repo := &firstOrderBonusRepoStub{}
	svc := newFirstOrderBonusService(repo, map[string]string{
		SettingKeyAffiliateFirstOrderBonus: `{"enabled":false,"threshold":25,"invitee_bonus":8,"inviter_bonus":6,"valid_days":15}`,
	})

	status, err := svc.GetFirstOrderBonusStatus(context.Background(), 42)
	require.NoError(t, err)
	require.Equal(t, FirstOrderBonusStatusDisabled, status.Status)
	require.False(t, status.Enabled)
	// 关闭时配置字段照常返回：前端要用这些数字渲染文案。
	require.InDelta(t, 25.0, status.Threshold, 1e-9)
	require.InDelta(t, 8.0, status.InviteeBonus, 1e-9)
	require.InDelta(t, 6.0, status.InviterBonus, 1e-9)
	require.Equal(t, 15, status.ValidDays)
	require.Empty(t, repo.readOnlyCalls)
}

func TestGetFirstOrderBonusStatusNotInvitee(t *testing.T) {
	repo := &firstOrderBonusRepoStub{readOnly: nil}
	svc := newFirstOrderBonusService(repo, nil)

	status, err := svc.GetFirstOrderBonusStatus(context.Background(), 42)
	require.NoError(t, err)
	require.Equal(t, FirstOrderBonusStatusNotInvitee, status.Status)
	// 只读：绝不能走 EnsureUserAffiliate（它会 INSERT）。
	require.Empty(t, repo.ensureCalls)
	require.Equal(t, []int64{42}, repo.readOnlyCalls)
}

func TestGetFirstOrderBonusStatusNotInviteeWhenInviterMissing(t *testing.T) {
	repo := &firstOrderBonusRepoStub{
		readOnly: &AffiliateSummary{UserID: 42, AffCode: "SELF", CreatedAt: time.Now()},
	}
	svc := newFirstOrderBonusService(repo, nil)

	status, err := svc.GetFirstOrderBonusStatus(context.Background(), 42)
	require.NoError(t, err)
	require.Equal(t, FirstOrderBonusStatusNotInvitee, status.Status)
}

func TestGetFirstOrderBonusStatusVoidConsumedBeatsExpired(t *testing.T) {
	// 无记录 + 已过期 + 有已交付订单：consumed 优先（顺序 4 在 5 之前）。
	repo := &firstOrderBonusRepoStub{
		readOnly: &AffiliateSummary{UserID: 42, InviterID: firstOrderBonusInt64Ptr(9), CreatedAt: time.Now().Add(-90 * 24 * time.Hour)},
		hasOther: true,
	}
	svc := newFirstOrderBonusService(repo, nil)

	status, err := svc.GetFirstOrderBonusStatus(context.Background(), 42)
	require.NoError(t, err)
	require.Equal(t, FirstOrderBonusStatusVoidConsumed, status.Status)
	// 只读查询用 excludeOrderID=0：没有 id=0 的订单，等价于「任何已交付订单」。
	require.Equal(t, [][2]int64{{42, 0}}, repo.hasOtherCalls)
	require.Nil(t, status.ExpiresAt)
}

func TestGetFirstOrderBonusStatusVoidExpiredIsLazyAndDoesNotRecord(t *testing.T) {
	repo := &firstOrderBonusRepoStub{
		readOnly: &AffiliateSummary{UserID: 42, InviterID: firstOrderBonusInt64Ptr(9), CreatedAt: time.Now().Add(-90 * 24 * time.Hour)},
	}
	svc := newFirstOrderBonusService(repo, nil)

	status, err := svc.GetFirstOrderBonusStatus(context.Background(), 42)
	require.NoError(t, err)
	require.Equal(t, FirstOrderBonusStatusVoidExpired, status.Status)
	require.Nil(t, status.ExpiresAt)
	// 惰性判定：一次 GET 不该落表。
	require.Empty(t, repo.voidCalls)
}

func TestGetFirstOrderBonusStatusAvailableCarriesExpiresAt(t *testing.T) {
	createdAt := time.Now().Add(-24 * time.Hour)
	repo := &firstOrderBonusRepoStub{
		readOnly: &AffiliateSummary{UserID: 42, InviterID: firstOrderBonusInt64Ptr(9), CreatedAt: createdAt},
	}
	svc := newFirstOrderBonusService(repo, nil)

	status, err := svc.GetFirstOrderBonusStatus(context.Background(), 42)
	require.NoError(t, err)
	require.Equal(t, FirstOrderBonusStatusAvailable, status.Status)
	require.True(t, status.Enabled)
	require.NotNil(t, status.ExpiresAt)
	require.True(t, createdAt.AddDate(0, 0, 30).Equal(*status.ExpiresAt))
}

func TestGetFirstOrderBonusStatusAvailableOmitsExpiresAtWhenNeverExpires(t *testing.T) {
	repo := &firstOrderBonusRepoStub{
		readOnly: &AffiliateSummary{UserID: 42, InviterID: firstOrderBonusInt64Ptr(9), CreatedAt: time.Now().Add(-5000 * 24 * time.Hour)},
	}
	svc := newFirstOrderBonusService(repo, map[string]string{
		SettingKeyAffiliateFirstOrderBonus: `{"enabled":true,"threshold":20,"invitee_bonus":10,"inviter_bonus":10,"valid_days":0}`,
	})

	status, err := svc.GetFirstOrderBonusStatus(context.Background(), 42)
	require.NoError(t, err)
	require.Equal(t, FirstOrderBonusStatusAvailable, status.Status)
	require.Nil(t, status.ExpiresAt)
}

func TestGetFirstOrderBonusStatusRejectsInvalidUser(t *testing.T) {
	svc := newFirstOrderBonusService(&firstOrderBonusRepoStub{}, nil)
	status, err := svc.GetFirstOrderBonusStatus(context.Background(), 0)
	require.Error(t, err)
	require.Nil(t, status)
}

// ---------------------------------------------------------------------------
// ParseAffiliateFirstOrderBonusConfig
// ---------------------------------------------------------------------------

func TestParseAffiliateFirstOrderBonusConfigFallsBackToDefaults(t *testing.T) {
	def := DefaultAffiliateFirstOrderBonusConfig()

	for name, raw := range map[string]string{
		"empty":      "",
		"whitespace": "   ",
		"broken":     "{not json",
		"array":      `["nope"]`,
	} {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, def, ParseAffiliateFirstOrderBonusConfig(raw))
		})
	}
}

func TestParseAffiliateFirstOrderBonusConfigKeepsDefaultsForMissingFields(t *testing.T) {
	cfg := ParseAffiliateFirstOrderBonusConfig(`{"enabled":true,"threshold":50}`)
	require.True(t, cfg.Enabled)
	require.InDelta(t, 50.0, cfg.Threshold, 1e-9)
	// 缺的字段保持默认，而不是掉成零值（否则后台显示 0、实际按默认执行）。
	require.InDelta(t, AffiliateFirstOrderInviteeBonusDefault, cfg.InviteeBonus, 1e-9)
	require.InDelta(t, AffiliateFirstOrderInviterBonusDefault, cfg.InviterBonus, 1e-9)
	require.Equal(t, AffiliateFirstOrderValidDaysDefault, cfg.ValidDays)
}

func TestParseAffiliateFirstOrderBonusConfigNormalizesOutOfRange(t *testing.T) {
	cfg := ParseAffiliateFirstOrderBonusConfig(`{"enabled":true,"threshold":-5,"invitee_bonus":999999999,"inviter_bonus":0,"valid_days":99999}`)
	require.InDelta(t, AffiliateFirstOrderThresholdDefault, cfg.Threshold, 1e-9)
	require.InDelta(t, AffiliateFirstOrderAmountMax, cfg.InviteeBonus, 1e-9)
	// 0 是合法值（只给一边发钱），不该被回落成默认。
	require.InDelta(t, 0.0, cfg.InviterBonus, 1e-9)
	require.Equal(t, AffiliateFirstOrderValidDaysMax, cfg.ValidDays)

	negativeDays := ParseAffiliateFirstOrderBonusConfig(`{"valid_days":-1}`)
	require.Equal(t, AffiliateFirstOrderValidDaysDefault, negativeDays.ValidDays)
}

func TestAffiliateFirstOrderBonusConfigMarshalSettingValueRoundTrips(t *testing.T) {
	cfg := AffiliateFirstOrderBonusConfig{Enabled: true, Threshold: 30, InviteeBonus: 12, InviterBonus: 8, ValidDays: 45}
	require.Equal(t, cfg, ParseAffiliateFirstOrderBonusConfig(cfg.MarshalSettingValue()))
}

var _ AffiliateRepository = (*firstOrderBonusRepoStub)(nil)
var _ SettingRepository = (*firstOrderBonusSettingRepoStub)(nil)
