package service

import (
	"context"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// 邀请「首单双向奖励」的领域逻辑（aicat 自研，2026-09）。
//
// 业务口径：被邀请人注册即持有一张「首充券」，在券有效期内的第一笔真正交付
// 完成的站内订单如果达到阈值，被邀请人加余额、邀请人加返利额度；不到阈值则
// 当场永久作废。常规 20% 返利与本功能完全独立、互不影响。
//
// 为什么单独一个文件：affiliate_service.go 是与上游合并的热点文件，自研逻辑
// 全部放新文件，既有文件只做接口块的一处连续插入。

// 首单奖励的状态机取值。前四个既是新表 status 列的落库值（applied /
// void_below_threshold / void_expired），也是用户端查询返回的 status；
// 后四个只在查询接口里出现，不落表。
const (
	// FirstOrderBonusStatusApplied 已发放（落表）。
	FirstOrderBonusStatusApplied = "applied"
	// FirstOrderBonusStatusVoidBelowThreshold 首单未达阈值，券当场作废（落表）。
	FirstOrderBonusStatusVoidBelowThreshold = "void_below_threshold"
	// FirstOrderBonusStatusVoidExpired 首单发生时券已过期（落表）；查询接口里也用于惰性判定。
	FirstOrderBonusStatusVoidExpired = "void_expired"
	// FirstOrderBonusStatusVoidConsumed 功能上线前/关闭期间已完成过订单，券永久无效（只在查询里出现）。
	FirstOrderBonusStatusVoidConsumed = "void_consumed"
	// FirstOrderBonusStatusAvailable 券可用。
	FirstOrderBonusStatusAvailable = "available"
	// FirstOrderBonusStatusNotInvitee 不是被邀请人，没有券。
	FirstOrderBonusStatusNotInvitee = "not_invitee"
	// FirstOrderBonusStatusDisabled 功能未生效（总开关或本功能开关关闭）。
	FirstOrderBonusStatusDisabled = "disabled"
)

// AffiliateLedgerKindFirstOrderBonus 是 user_affiliate_ledger.kind 的取值。
// 常规返利与转余额写 NULL；首单奖励写这个值，用于把它从「单人返利上限」
// 统计里剔除（见 GetAccruedRebateFromInvitee）。
const AffiliateLedgerKindFirstOrderBonus = "first_order_bonus"

// ApplyFirstOrderBonusForOrder 各分支的固定 Reason，会原样写进审计 detail。
const (
	firstOrderBonusReasonDisabled        = "disabled"
	firstOrderBonusReasonNoInviter       = "no_inviter"
	firstOrderBonusReasonAlreadyRecorded = "already_recorded"
	firstOrderBonusReasonNotFirstOrder   = "not_first_order"
	firstOrderBonusReasonExpired         = "expired"
	firstOrderBonusReasonBelowThreshold  = "below_threshold"
	firstOrderBonusReasonAlreadyClaimed  = "already_claimed"
	firstOrderBonusReasonApplied         = "applied"
)

// affiliateFirstOrderAmountEpsilon 是阈值比较的容差。
// 订单金额与阈值都可能来自 DECIMAL → float64 的转换，恰好等于阈值的订单
// 不能因为末位误差被判成「不满阈值」而把券烧掉（作废不可逆）。
const affiliateFirstOrderAmountEpsilon = 1e-8

// AffiliateFirstOrderBonusRecord 是 user_affiliate_first_order_bonus 的一行。
type AffiliateFirstOrderBonusRecord struct {
	UserID       int64
	InviterID    *int64
	OrderID      int64
	OrderAmount  float64
	InviteeBonus float64
	InviterBonus float64
	Status       string
	CreatedAt    time.Time
}

// AffiliateFirstOrderBonusVoidInput 是落一条作废记录所需的入参。
type AffiliateFirstOrderBonusVoidInput struct {
	UserID      int64
	InviterID   *int64
	OrderID     int64
	OrderAmount float64
	Status      string
}

// AffiliateFirstOrderBonusApplyInput 是发放首单奖励所需的入参。
type AffiliateFirstOrderBonusApplyInput struct {
	UserID       int64
	InviterID    int64
	OrderID      int64
	OrderAmount  float64
	InviteeBonus float64
	InviterBonus float64
	FreezeHours  int
}

// AffiliateFirstOrderBonusStatus 是用户端 GET /user/aff/first-order-bonus 的响应体。
type AffiliateFirstOrderBonusStatus struct {
	Enabled             bool       `json:"enabled"`
	Status              string     `json:"status"`
	Threshold           float64    `json:"threshold"`
	InviteeBonus        float64    `json:"invitee_bonus"`
	InviterBonus        float64    `json:"inviter_bonus"`
	ValidDays           int        `json:"valid_days"`
	ExpiresAt           *time.Time `json:"expires_at,omitempty"`
	AppliedAt           *time.Time `json:"applied_at,omitempty"`
	AppliedOrderID      *int64     `json:"applied_order_id,omitempty"`
	AppliedInviteeBonus *float64   `json:"applied_invitee_bonus,omitempty"`
}

// AffiliateFirstOrderBonusOutcome 是一次发放尝试的结果，供调用方写审计。
type AffiliateFirstOrderBonusOutcome struct {
	Applied      bool
	Reason       string
	InviteeBonus float64
	InviterBonus float64
	InviterID    *int64
}

// IsFirstOrderBonusActive 是 SettingService 的薄包装：让 PaymentService 在
// 开事务之前就能判断功能是否生效。功能默认关闭，不能每笔订单都白开一个事务、
// 白占一行审计。
func (s *AffiliateService) IsFirstOrderBonusActive(ctx context.Context) bool {
	if s == nil || s.settingService == nil {
		return false
	}
	return s.settingService.IsAffiliateFirstOrderBonusActive(ctx)
}

// firstOrderBonusConfig 读取归正后的配置；SettingService 缺失时用文档化默认值。
func (s *AffiliateService) firstOrderBonusConfig(ctx context.Context) AffiliateFirstOrderBonusConfig {
	if s == nil || s.settingService == nil {
		return DefaultAffiliateFirstOrderBonusConfig()
	}
	return s.settingService.GetAffiliateFirstOrderBonusConfig(ctx).Normalized()
}

// ApplyFirstOrderBonusForOrder 尝试为一笔已交付的订单发放首单奖励。
//
// 调用方（PaymentService）已经在事务里，并且已经用审计行抢占了这笔订单，
// 所以这里的每一步都在同一个事务内；返回 error 让调用方回滚。
//
// 判定顺序是有意固定的：先看功能开关（省掉后面所有查询），再看有没有邀请人，
// 然后锁被邀请人的 user_affiliates 行把同一用户的并发首单串行化，之后才是
// 「已经处理过吗 / 是不是首单 / 过期了吗 / 够阈值吗」。锁必须在读记录之前，
// 否则两个并发请求会同时读到「没有记录」。
func (s *AffiliateService) ApplyFirstOrderBonusForOrder(ctx context.Context, inviteeUserID, orderID int64, orderAmount float64) (*AffiliateFirstOrderBonusOutcome, error) {
	if s == nil || s.repo == nil {
		return &AffiliateFirstOrderBonusOutcome{Reason: firstOrderBonusReasonDisabled}, nil
	}
	if !s.IsFirstOrderBonusActive(ctx) {
		return &AffiliateFirstOrderBonusOutcome{Reason: firstOrderBonusReasonDisabled}, nil
	}
	cfg := s.firstOrderBonusConfig(ctx)

	// 这里可以建行（已在事务里）：被邀请人极少数情况下可能还没有 user_affiliates 行，
	// 而下一步的 FOR UPDATE 需要有行可锁。
	invitee, err := s.repo.EnsureUserAffiliate(ctx, inviteeUserID)
	if err != nil {
		return nil, err
	}
	if invitee == nil || invitee.InviterID == nil || *invitee.InviterID <= 0 {
		return &AffiliateFirstOrderBonusOutcome{Reason: firstOrderBonusReasonNoInviter}, nil
	}
	inviterID := *invitee.InviterID

	if err := s.repo.LockUserAffiliateForUpdate(ctx, inviteeUserID); err != nil {
		return nil, err
	}

	record, err := s.repo.GetFirstOrderBonusRecord(ctx, inviteeUserID)
	if err != nil {
		return nil, err
	}
	if record != nil {
		return &AffiliateFirstOrderBonusOutcome{Reason: firstOrderBonusReasonAlreadyRecorded, InviterID: &inviterID}, nil
	}

	hasOther, err := s.repo.HasEarlierOrFulfilledPaymentOrder(ctx, inviteeUserID, orderID)
	if err != nil {
		return nil, err
	}
	if hasOther {
		// 不是首单：不落表。券还可能因为那笔真正的首单而被处理（或已经被处理过）。
		return &AffiliateFirstOrderBonusOutcome{Reason: firstOrderBonusReasonNotFirstOrder, InviterID: &inviterID}, nil
	}

	// 有效期从 user_affiliates.created_at 起算（对被邀请人恒等于注册时间，
	// 因为 inviter_id 只在注册流程内写入且同事务先建行），与现有返利有效期同源。
	if cfg.ValidDays > 0 && time.Now().After(invitee.CreatedAt.AddDate(0, 0, cfg.ValidDays)) {
		if _, err := s.repo.RecordFirstOrderBonusVoid(ctx, AffiliateFirstOrderBonusVoidInput{
			UserID:      inviteeUserID,
			InviterID:   &inviterID,
			OrderID:     orderID,
			OrderAmount: orderAmount,
			Status:      FirstOrderBonusStatusVoidExpired,
		}); err != nil {
			return nil, err
		}
		return &AffiliateFirstOrderBonusOutcome{Reason: firstOrderBonusReasonExpired, InviterID: &inviterID}, nil
	}

	if orderAmount+affiliateFirstOrderAmountEpsilon < cfg.Threshold {
		if _, err := s.repo.RecordFirstOrderBonusVoid(ctx, AffiliateFirstOrderBonusVoidInput{
			UserID:      inviteeUserID,
			InviterID:   &inviterID,
			OrderID:     orderID,
			OrderAmount: orderAmount,
			Status:      FirstOrderBonusStatusVoidBelowThreshold,
		}); err != nil {
			return nil, err
		}
		return &AffiliateFirstOrderBonusOutcome{Reason: firstOrderBonusReasonBelowThreshold, InviterID: &inviterID}, nil
	}

	freezeHours := 0
	if s.settingService != nil {
		freezeHours = s.settingService.GetAffiliateRebateFreezeHours(ctx)
	}

	applied, err := s.repo.ApplyFirstOrderBonus(ctx, AffiliateFirstOrderBonusApplyInput{
		UserID:       inviteeUserID,
		InviterID:    inviterID,
		OrderID:      orderID,
		OrderAmount:  orderAmount,
		InviteeBonus: cfg.InviteeBonus,
		InviterBonus: cfg.InviterBonus,
		FreezeHours:  freezeHours,
	})
	if err != nil {
		return nil, err
	}
	if !applied {
		// 主键冲突：另一条并发路径先落了记录，本次一分钱都没动。
		return &AffiliateFirstOrderBonusOutcome{Reason: firstOrderBonusReasonAlreadyClaimed, InviterID: &inviterID}, nil
	}
	return &AffiliateFirstOrderBonusOutcome{
		Applied:      true,
		Reason:       firstOrderBonusReasonApplied,
		InviteeBonus: cfg.InviteeBonus,
		InviterBonus: cfg.InviterBonus,
		InviterID:    &inviterID,
	}, nil
}

// GetFirstOrderBonusStatus 返回用户当前的首充券状态（只读，绝不建行、绝不落表）。
//
// 判定顺序是「记录优先」：一旦落了表就直接按记录返回，不再看邀请人、不再看订单。
// 这样管理员事后改配置、改开关都不会让已经结算过的用户看到自相矛盾的状态。
func (s *AffiliateService) GetFirstOrderBonusStatus(ctx context.Context, userID int64) (*AffiliateFirstOrderBonusStatus, error) {
	if s == nil || s.repo == nil {
		return nil, infraerrors.ServiceUnavailable("SERVICE_UNAVAILABLE", "affiliate service unavailable")
	}
	if userID <= 0 {
		return nil, infraerrors.BadRequest("INVALID_USER", "invalid user")
	}

	cfg := s.firstOrderBonusConfig(ctx)
	out := &AffiliateFirstOrderBonusStatus{
		Enabled:      s.IsFirstOrderBonusActive(ctx),
		Threshold:    cfg.Threshold,
		InviteeBonus: cfg.InviteeBonus,
		InviterBonus: cfg.InviterBonus,
		ValidDays:    cfg.ValidDays,
	}

	// 1) 有记录：直接按记录返回。
	record, err := s.repo.GetFirstOrderBonusRecord(ctx, userID)
	if err != nil {
		return nil, err
	}
	if record != nil {
		out.Status = record.Status
		if record.Status == FirstOrderBonusStatusApplied {
			appliedAt := record.CreatedAt
			appliedOrderID := record.OrderID
			appliedBonus := record.InviteeBonus
			out.AppliedAt = &appliedAt
			out.AppliedOrderID = &appliedOrderID
			out.AppliedInviteeBonus = &appliedBonus
		}
		return out, nil
	}

	// 2) 功能未生效。
	if !out.Enabled {
		out.Status = FirstOrderBonusStatusDisabled
		return out, nil
	}

	// 3) 不是被邀请人。只读查询，不能用 EnsureUserAffiliate（它会 INSERT）。
	summary, err := s.repo.GetUserAffiliateReadOnly(ctx, userID)
	if err != nil {
		return nil, err
	}
	if summary == nil || summary.InviterID == nil || *summary.InviterID <= 0 {
		out.Status = FirstOrderBonusStatusNotInvitee
		return out, nil
	}

	// 4) 已经有交付完成的订单：券永久无效。
	// excludeOrderID 传 0 —— 没有 id=0 的订单，等价于「任何已交付订单」。
	consumed, err := s.repo.HasEarlierOrFulfilledPaymentOrder(ctx, userID, 0)
	if err != nil {
		return nil, err
	}
	if consumed {
		out.Status = FirstOrderBonusStatusVoidConsumed
		return out, nil
	}

	// 5) 惰性过期判定（不落表，落表只发生在真的来了一笔订单时）。
	if cfg.ValidDays > 0 {
		expiresAt := summary.CreatedAt.AddDate(0, 0, cfg.ValidDays)
		if time.Now().After(expiresAt) {
			out.Status = FirstOrderBonusStatusVoidExpired
			return out, nil
		}
		out.ExpiresAt = &expiresAt
	}

	out.Status = FirstOrderBonusStatusAvailable
	return out, nil
}
