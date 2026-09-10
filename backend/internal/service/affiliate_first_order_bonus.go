package service

import (
	"context"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// 邀请「首单双向奖励」的领域逻辑（aicat 自研，2026-09）。
//
// 业务口径（v2，2026-09-10 主人拍板「读法 B」）：
//
//   - 被邀请人：注册即持有一张「首充券」，在券有效期内的第一笔真正交付完成的
//     站内订单如果达到阈值，加余额；不到阈值或已过期则当场永久作废。
//   - 邀请人：好友首单**不看阈值、不看券有效期**，按首单金额 × 首单返利率
//     （默认 50%）计算总返利、封顶一个金额（默认 10$），再扣掉常规比例返利
//     已经发过的那部分；差额为正才额外累计，为负则什么都不额外发（即「不低于
//     常规返利」）。所以好友首充 5$ 邀请人拿 2.5$，首充 20$ 拿 10$，首充 200$
//     常规 10% 已经是 20$，不再额外发。
//
// 常规比例返利完全独立、互不影响；本功能只在它之上补差额。
//
// 为什么单独一个文件：affiliate_service.go 是与上游合并的热点文件，自研逻辑
// 全部放新文件，既有文件只做接口块的一处连续插入。

// 首单奖励的状态机取值。前三个既是新表 status 列的落库值，描述的是
// **被邀请人那份券**的结局（applied / void_below_threshold / void_expired）；
// 邀请人那份看记录里的 inviter_bonus 列，与 status 无关。
// 后四个只在查询接口里出现，不落表。
const (
	// FirstOrderBonusStatusApplied 被邀请人奖励已发放（落表）。
	FirstOrderBonusStatusApplied = "applied"
	// FirstOrderBonusStatusVoidBelowThreshold 首单未达阈值，被邀请人的券当场作废（落表）。
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
// v2 起 expired / below_threshold 描述的只是被邀请人那份的结局，邀请人那份
// 在这两种情况下照样按比例发，所以它们可以与 Outcome.Applied=true 同时出现。
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

// AffiliateFirstOrderBonusApplyInput 是结算一笔首单所需的入参。
// InviteeBonus / InviterBonus 可以为 0（那一侧不发钱，但记录照样落表）。
// Status 是被邀请人那份券的结局，落进记录表的 status 列。
type AffiliateFirstOrderBonusApplyInput struct {
	UserID       int64
	InviterID    int64
	OrderID      int64
	OrderAmount  float64
	InviteeBonus float64
	InviterBonus float64
	Status       string
	FreezeHours  int
}

// AffiliateFirstOrderBonusStatus 是用户端 GET /user/aff/first-order-bonus 的响应体。
type AffiliateFirstOrderBonusStatus struct {
	Enabled             bool       `json:"enabled"`
	Status              string     `json:"status"`
	Threshold           float64    `json:"threshold"`
	InviteeBonus        float64    `json:"invitee_bonus"`
	InviterRatePercent  float64    `json:"inviter_rate_percent"`
	InviterCap          float64    `json:"inviter_cap"`
	ValidDays           int        `json:"valid_days"`
	ExpiresAt           *time.Time `json:"expires_at,omitempty"`
	AppliedAt           *time.Time `json:"applied_at,omitempty"`
	AppliedOrderID      *int64     `json:"applied_order_id,omitempty"`
	AppliedInviteeBonus *float64   `json:"applied_invitee_bonus,omitempty"`
}

// AffiliateFirstOrderBonusOutcome 是一次结算尝试的结果，供调用方写审计。
// Applied 表示「有钱动了」（任一侧 > 0）；InviteeStatus 是被邀请人那份的结局，
// 只在真的落了表时非空。
type AffiliateFirstOrderBonusOutcome struct {
	Applied       bool
	Reason        string
	InviteeStatus string
	InviteeBonus  float64
	InviterBonus  float64
	InviterID     *int64
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

// ApplyFirstOrderBonusForOrder 尝试为一笔已交付的订单结算首单奖励。
//
// 调用方（PaymentService）已经在事务里，并且已经用审计行抢占了这笔订单，
// 所以这里的每一步都在同一个事务内；返回 error 让调用方回滚。
//
// 判定顺序是有意固定的：先看功能开关（省掉后面所有查询），再看有没有邀请人，
// 然后锁被邀请人的 user_affiliates 行把同一用户的并发首单串行化，之后才是
// 「已经处理过吗 / 是不是首单」。锁必须在读记录之前，否则两个并发请求会同时
// 读到「没有记录」。确认是首单之后，两侧各算各的：被邀请人看有效期与阈值，
// 邀请人只看金额；最后一次性落表。
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

	// —— 被邀请人那份：看有效期，再看阈值 ——
	// 有效期从 user_affiliates.created_at 起算（对被邀请人恒等于注册时间，
	// 因为 inviter_id 只在注册流程内写入且同事务先建行），与现有返利有效期同源。
	inviteeStatus := FirstOrderBonusStatusApplied
	inviteeBonus := cfg.InviteeBonus
	reason := firstOrderBonusReasonApplied
	switch {
	case cfg.ValidDays > 0 && time.Now().After(invitee.CreatedAt.AddDate(0, 0, cfg.ValidDays)):
		inviteeStatus, inviteeBonus, reason = FirstOrderBonusStatusVoidExpired, 0, firstOrderBonusReasonExpired
	case orderAmount+affiliateFirstOrderAmountEpsilon < cfg.Threshold:
		inviteeStatus, inviteeBonus, reason = FirstOrderBonusStatusVoidBelowThreshold, 0, firstOrderBonusReasonBelowThreshold
	}

	// —— 邀请人那份：只看金额，不看阈值、不看券有效期 ——
	// 需要邀请人的 profile 才能算出「常规返利已经发了多少」（专属比例覆盖全局）。
	inviter, err := s.repo.EnsureUserAffiliate(ctx, inviterID)
	if err != nil {
		return nil, err
	}
	inviterBonus := s.firstOrderInviterBonus(ctx, invitee, inviter, orderAmount, cfg)

	freezeHours := 0
	if s.settingService != nil {
		freezeHours = s.settingService.GetAffiliateRebateFreezeHours(ctx)
	}

	applied, err := s.repo.ApplyFirstOrderBonus(ctx, AffiliateFirstOrderBonusApplyInput{
		UserID:       inviteeUserID,
		InviterID:    inviterID,
		OrderID:      orderID,
		OrderAmount:  orderAmount,
		InviteeBonus: inviteeBonus,
		InviterBonus: inviterBonus,
		Status:       inviteeStatus,
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
		Applied:       inviteeBonus > 0 || inviterBonus > 0,
		Reason:        reason,
		InviteeStatus: inviteeStatus,
		InviteeBonus:  inviteeBonus,
		InviterBonus:  inviterBonus,
		InviterID:     &inviterID,
	}, nil
}

// firstOrderInviterBonus 算邀请人在好友首单上应**额外**累计的返利额度：
//
//	首单总返利 = min(首单金额 × 首单返利率, 封顶额)
//	额外累计   = 首单总返利 − 常规比例返利在这笔订单上已发的金额，负数取 0
//
// 常规返利那一份用与 AccrueInviteRebateForOrder 完全相同的口径反推（专属比例
// 优先、返利有效期、单人上限截断、8 位小数），这样两条路径加起来正好等于
// 「首单返利率、封顶、不低于常规」这句话，而不会多发或少发一分。
// 返利率或封顶额为 0 都表示「邀请人那份不发」。
func (s *AffiliateService) firstOrderInviterBonus(ctx context.Context, invitee, inviter *AffiliateSummary, orderAmount float64, cfg AffiliateFirstOrderBonusConfig) float64 {
	if orderAmount <= 0 || cfg.InviterRatePercent <= 0 || cfg.InviterCap <= 0 {
		return 0
	}
	total := roundTo(orderAmount*(cfg.InviterRatePercent/100), 8)
	if total > cfg.InviterCap {
		total = cfg.InviterCap
	}
	if total <= 0 {
		return 0
	}

	regular := roundTo(orderAmount*(s.resolveRebateRatePercent(ctx, inviter)/100), 8)
	if s.settingService != nil && invitee != nil {
		if durationDays := s.settingService.GetAffiliateRebateDurationDays(ctx); durationDays > 0 {
			if time.Now().After(invitee.CreatedAt.AddDate(0, 0, durationDays)) {
				regular = 0
			}
		}
		if perInviteeCap := s.settingService.GetAffiliateRebatePerInviteeCap(ctx); perInviteeCap > 0 && regular > perInviteeCap {
			// 首单之前该好友还没产生过返利，常规路径最多只发到单人上限。
			regular = roundTo(perInviteeCap, 8)
		}
	}

	extra := roundTo(total-regular, 8)
	if extra <= 0 {
		return 0
	}
	return extra
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
		Enabled:            s.IsFirstOrderBonusActive(ctx),
		Threshold:          cfg.Threshold,
		InviteeBonus:       cfg.InviteeBonus,
		InviterRatePercent: cfg.InviterRatePercent,
		InviterCap:         cfg.InviterCap,
		ValidDays:          cfg.ValidDays,
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
