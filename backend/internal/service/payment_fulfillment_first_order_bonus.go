package service

import (
	"context"
	"fmt"
	"log/slog"

	dbent "github.com/Wei-Shaw/sub2api/ent"
)

// 订单履约链路上的「首单双向奖励」挂载点（aicat 自研，2026-09）。
// 放新文件：payment_fulfillment.go 是与上游合并的热点，那边只留三处一行调用。

// affiliateRebateAuditClaim / affiliateFirstOrderBonusAuditClaim 是两套互不相交
// 的审计动作名。它们共用 tryClaimAffiliateRebateAudit / updateClaimedAffiliateRebateAudit，
// 靠这两个值把「谁抢了谁的行」分开。
var (
	affiliateRebateAuditClaim = affiliateAuditClaim{
		appliedAction: "AFFILIATE_REBATE_APPLIED",
		skippedAction: "AFFILIATE_REBATE_SKIPPED",
	}
	affiliateFirstOrderBonusAuditClaim = affiliateAuditClaim{
		appliedAction: "AFFILIATE_FIRST_ORDER_BONUS_APPLIED",
		skippedAction: "AFFILIATE_FIRST_ORDER_BONUS_SKIPPED",
	}
)

// applyAffiliateFirstOrderBonusForOrder 在订单交付完成前尝试发放首单奖励。
//
// 与常规返利有两点关键差别：
//
//  1. 开事务之前先读开关。功能默认关闭，不能让每一笔订单都白开一个事务、
//     白占一行审计（审计有 (order_id, action) 唯一索引，写进去就删不掉了）。
//  2. fail-open：任何一步出错都只写一行 FAILED 审计 + 日志，然后返回 nil。
//     首充券是赠品，不能因为它把一笔已经通过 redeem 到账的订单打成 FAILED；
//     常规返利敢 fail-closed 是因为它只碰早就存在的表。
//     （writeAuditLog 撞唯一索引只记日志、不会炸，所以这条兜底路径是安全的。）
//
// 保留 error 返回通道是给调用点留形状——目前实际只会返回 nil。
func (s *PaymentService) applyAffiliateFirstOrderBonusForOrder(ctx context.Context, o *dbent.PaymentOrder) error {
	if o == nil {
		return nil
	}
	orderAmount := affiliateRebateBaseAmount(o)
	if orderAmount <= 0 || s.affiliateService == nil {
		return nil
	}
	if !s.affiliateService.IsFirstOrderBonusActive(ctx) {
		return nil
	}

	applied, err := s.runAffiliateFirstOrderBonusTx(ctx, o, orderAmount)
	if err != nil {
		s.writeAuditLog(ctx, o.ID, "AFFILIATE_FIRST_ORDER_BONUS_FAILED", "system", map[string]any{
			"error": err.Error(),
		})
		slog.Error("affiliate first order bonus failed",
			"orderID", o.ID,
			"userID", o.UserID,
			"error", err.Error(),
		)
		return nil
	}

	// 提交之后才失效缓存：被邀请人的余额变了，鉴权/计费缓存必须重读。
	// 邀请人侧不失效，与现有 AccrueQuota 的口径保持一致（返利额度不进这些缓存）。
	if applied {
		s.affiliateService.InvalidateUserCaches(ctx, o.UserID)
	}
	return nil
}

// runAffiliateFirstOrderBonusTx 跑完整的「抢审计 → 判定发放 → 改审计 → 提交」，
// 返回本次是否真的发了钱。任何错误都往上抛给 fail-open 的调用方。
func (s *PaymentService) runAffiliateFirstOrderBonusTx(ctx context.Context, o *dbent.PaymentOrder, orderAmount float64) (bool, error) {
	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return false, fmt.Errorf("begin affiliate first order bonus tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	txCtx := dbent.NewTxContext(ctx, tx)
	claimed, err := s.tryClaimAffiliateRebateAudit(txCtx, tx.Client(), o.ID, orderAmount, affiliateFirstOrderBonusAuditClaim)
	if err != nil {
		return false, fmt.Errorf("claim affiliate first order bonus audit: %w", err)
	}
	if !claimed {
		// 这笔订单已经被处理过（或正在被另一条路径处理）。
		return false, nil
	}

	outcome, err := s.affiliateService.ApplyFirstOrderBonusForOrder(txCtx, o.UserID, o.ID, orderAmount)
	if err != nil {
		return false, fmt.Errorf("apply affiliate first order bonus: %w", err)
	}
	if outcome == nil {
		return false, fmt.Errorf("apply affiliate first order bonus: nil outcome for order %d", o.ID)
	}

	if outcome.Applied {
		// v2 起 APPLIED 表示「有钱动了」：好友首单不满阈值时被邀请人那份作废、
		// 邀请人那份照样按比例发，也走这条分支，靠 inviteeStatus / reason 区分。
		detail := map[string]any{
			"orderAmount":   orderAmount,
			"reason":        outcome.Reason,
			"inviteeStatus": outcome.InviteeStatus,
			"inviteeBonus":  outcome.InviteeBonus,
			"inviterBonus":  outcome.InviterBonus,
		}
		if outcome.InviterID != nil {
			detail["inviterID"] = *outcome.InviterID
		}
		if err := s.updateClaimedAffiliateRebateAudit(txCtx, tx.Client(), o.ID,
			affiliateFirstOrderBonusAuditClaim.appliedAction, detail, affiliateFirstOrderBonusAuditClaim); err != nil {
			return false, fmt.Errorf("update affiliate first order bonus applied audit: %w", err)
		}
	} else {
		skippedDetail := map[string]any{
			"orderAmount": orderAmount,
			"reason":      outcome.Reason,
		}
		if outcome.InviteeStatus != "" {
			// 落了表但两侧都是 0（例如后台把两份奖励都设成 0）：记录结局，方便对账。
			skippedDetail["inviteeStatus"] = outcome.InviteeStatus
		}
		if err := s.updateClaimedAffiliateRebateAudit(txCtx, tx.Client(), o.ID,
			affiliateFirstOrderBonusAuditClaim.skippedAction, skippedDetail, affiliateFirstOrderBonusAuditClaim); err != nil {
			return false, fmt.Errorf("update affiliate first order bonus skipped audit: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit affiliate first order bonus tx: %w", err)
	}
	return outcome.Applied, nil
}
