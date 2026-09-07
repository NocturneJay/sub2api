package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/user"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// 邀请「首单双向奖励」的持久层（aicat 自研，2026-09）。
// 表结构见 migrations/902_affiliate_first_order_bonus.sql。

// firstOrderBonusSelectSQL 读一行首单奖励记录。
// DECIMAL 列统一 ::double precision，与本文件其它 affiliate 查询口径一致
// （Go 侧一律 float64，不引入 decimal 类型）。
const firstOrderBonusSelectSQL = `
SELECT user_id,
       inviter_id,
       order_id,
       order_amount::double precision,
       invitee_bonus::double precision,
       inviter_bonus::double precision,
       status,
       created_at
FROM user_affiliate_first_order_bonus
WHERE user_id = $1
LIMIT 1`

// firstOrderBonusHasOtherOrderSQL 判「这笔订单是不是该账号的首单」。
//
// 两条路径共用一条谓词，靠 $2（本单 id）分支：
//
//   - $2 = 0（用户端只读查询，没有「本单」可参照）：退化成「存在任何已交付
//     完成的订单」。判「曾交付」用 completed_at IS NOT NULL 而不是 status，
//     否则自助退款把状态改成 REFUND_* 之后老用户能重新领券。这一支必须单列，
//     因为它没有可比的时间基准，硬套时间序会因标量子查询为 NULL 而恒假。
//
//   - $2 <> 0（发放路径按订单判定）：只有「付款更早」的订单才有资格否掉本单，
//     因此「已交付完成」与「已付款正在履约（PAID / RECHARGING）」两个候选集合
//     共用同一套 (COALESCE(paid_at, created_at), id) 时间序。共用是必须的：
//     若 completed_at 这一支不带时间序，一笔付款更晚却先完成的订单会把真正的
//     首单也判成 not_first_order，结果两笔都跳过、券永远发不出去。退款订单不受
//     影响 —— 它 completed_at 不清、付款时间必然更早，照样挡得住重复领取。
//     用元组比较而不是只比时间，保证同一时刻的两笔也有确定先后；那笔更早的订单
//     自己判定时看到本单「付款更晚」，所以两边不会互相判死，与 commit 顺序无关。
//
// 右侧标量子查询写成 (SELECT a, b FROM ...)：括号加在 SELECT 外面，返回两列供
// 行比较。写成 (SELECT (a, b) FROM ...) 只返回一列 record，PostgreSQL 解析期
// 就报 subquery has too few columns。
const firstOrderBonusHasOtherOrderSQL = `
SELECT EXISTS (
  SELECT 1 FROM payment_orders o
  WHERE o.user_id = $1 AND o.id <> $2
    AND (
      ($2 = 0 AND o.completed_at IS NOT NULL)
      OR (
        $2 <> 0
        AND (o.completed_at IS NOT NULL OR o.status IN ('PAID', 'RECHARGING'))
        AND (COALESCE(o.paid_at, o.created_at), o.id)
            < (SELECT COALESCE(p.paid_at, p.created_at), p.id FROM payment_orders p WHERE p.id = $2)
      )
    )
)`

// GetUserAffiliateReadOnly 只读查 user_affiliates 行；不存在返回 nil, nil。
// 与 EnsureUserAffiliate 的区别就是这一点：绝不 INSERT。用户端查券状态时
// 不能因为一次 GET 就给未绑定邀请人的用户建行。
func (r *affiliateRepository) GetUserAffiliateReadOnly(ctx context.Context, userID int64) (*service.AffiliateSummary, error) {
	if userID <= 0 {
		return nil, nil
	}
	client := clientFromContext(ctx, r.client)
	summary, err := queryAffiliateByUserID(ctx, client, userID)
	if err != nil {
		if errors.Is(err, service.ErrAffiliateProfileNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return summary, nil
}

// GetFirstOrderBonusRecord 读首单奖励记录；不存在返回 nil, nil。
func (r *affiliateRepository) GetFirstOrderBonusRecord(ctx context.Context, userID int64) (*service.AffiliateFirstOrderBonusRecord, error) {
	if userID <= 0 {
		return nil, nil
	}
	client := clientFromContext(ctx, r.client)
	rows, err := client.QueryContext(ctx, firstOrderBonusSelectSQL, userID)
	if err != nil {
		return nil, fmt.Errorf("query affiliate first order bonus record: %w", err)
	}
	defer func() { _ = rows.Close() }()

	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, err
		}
		return nil, nil
	}

	var out service.AffiliateFirstOrderBonusRecord
	var inviterID sql.NullInt64
	if err := rows.Scan(
		&out.UserID,
		&inviterID,
		&out.OrderID,
		&out.OrderAmount,
		&out.InviteeBonus,
		&out.InviterBonus,
		&out.Status,
		&out.CreatedAt,
	); err != nil {
		return nil, err
	}
	if inviterID.Valid {
		v := inviterID.Int64
		out.InviterID = &v
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return &out, nil
}

// HasEarlierOrFulfilledPaymentOrder 见 firstOrderBonusHasOtherOrderSQL 的说明。
func (r *affiliateRepository) HasEarlierOrFulfilledPaymentOrder(ctx context.Context, userID, excludeOrderID int64) (bool, error) {
	if userID <= 0 {
		return false, nil
	}
	client := clientFromContext(ctx, r.client)
	rows, err := client.QueryContext(ctx, firstOrderBonusHasOtherOrderSQL, userID, excludeOrderID)
	if err != nil {
		return false, fmt.Errorf("query earlier or fulfilled payment order: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var exists bool
	if rows.Next() {
		if err := rows.Scan(&exists); err != nil {
			return false, err
		}
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	return exists, nil
}

// LockUserAffiliateForUpdate 在当前事务里锁住被邀请人的 user_affiliates 行。
// 同一用户的并发首单（重复 webhook、重试）必须串行化，否则两条路径会同时
// 读到「没有记录」而各自往下走；真正的最后一道闸是新表主键，这把锁只是
// 让并发路径不要白跑一遍余额更新再回滚。
//
// 行不存在时不报错：调用方已经 EnsureUserAffiliate 过了，这里没有可锁的行
// 只可能是并发删号，不值得把一笔已到账的订单打断。
func (r *affiliateRepository) LockUserAffiliateForUpdate(ctx context.Context, userID int64) error {
	if userID <= 0 {
		return nil
	}
	client := clientFromContext(ctx, r.client)
	rows, err := client.QueryContext(ctx, "SELECT user_id FROM user_affiliates WHERE user_id = $1 FOR UPDATE", userID)
	if err != nil {
		return fmt.Errorf("lock user affiliate row: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var ignored int64
		if err := rows.Scan(&ignored); err != nil {
			return err
		}
	}
	return rows.Err()
}

// RecordFirstOrderBonusVoid 落一条作废记录（void_expired / void_below_threshold）。
// ON CONFLICT DO NOTHING 保证「一人一次」由主键兜底；返回值告诉调用方本次是否新写入。
func (r *affiliateRepository) RecordFirstOrderBonusVoid(ctx context.Context, in service.AffiliateFirstOrderBonusVoidInput) (bool, error) {
	if in.UserID <= 0 || in.OrderID <= 0 {
		return false, nil
	}
	client := clientFromContext(ctx, r.client)
	rows, err := client.QueryContext(ctx, `
INSERT INTO user_affiliate_first_order_bonus
    (user_id, inviter_id, order_id, order_amount, invitee_bonus, inviter_bonus, status, created_at)
VALUES ($1, $2, $3, $4, 0, 0, $5, NOW())
ON CONFLICT (user_id) DO NOTHING
RETURNING user_id`,
		in.UserID, nullableInt64Arg(in.InviterID), in.OrderID, in.OrderAmount, in.Status)
	if err != nil {
		return false, fmt.Errorf("record affiliate first order bonus void: %w", err)
	}
	defer func() { _ = rows.Close() }()

	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return false, err
		}
		return false, nil
	}
	var recordedUserID int64
	if err := rows.Scan(&recordedUserID); err != nil {
		return false, err
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	return true, nil
}

// ApplyFirstOrderBonus 在一个事务里完成整套发放，顺序固定：
//
//  1. 抢记录（ON CONFLICT DO NOTHING）—— 没抢到就一分钱都不动；
//  2. 确保邀请人有 user_affiliates 行（邀请人可能从没访问过邀请页）；
//  3. 被邀请人加余额 + 一条 first_order_bonus 台账（余额历史里解释这笔钱从哪来）；
//  4. 邀请人加返利额度 + 一条带 kind 的 accrue 台账（复用常规返利的两条 SQL）。
//
// 任一步失败都整体回滚：绝不允许「记录表说发了、台账没有」。
func (r *affiliateRepository) ApplyFirstOrderBonus(ctx context.Context, in service.AffiliateFirstOrderBonusApplyInput) (bool, error) {
	if in.UserID <= 0 || in.InviterID <= 0 || in.OrderID <= 0 {
		return false, nil
	}

	var applied bool
	err := r.withTx(ctx, func(txCtx context.Context, txClient *dbent.Client) error {
		claimed, err := insertFirstOrderBonusApplied(txCtx, txClient, in)
		if err != nil {
			return err
		}
		if !claimed {
			applied = false
			return nil
		}

		if _, err := ensureUserAffiliateWithClient(txCtx, txClient, in.InviterID); err != nil {
			return err
		}

		if err := creditFirstOrderBonusBalance(txCtx, txClient, in); err != nil {
			return err
		}

		if in.InviterBonus > 0 {
			kind := service.AffiliateLedgerKindFirstOrderBonus
			sourceOrderID := in.OrderID
			accrued, err := r.accrueQuota(txCtx, in.InviterID, in.UserID, in.InviterBonus, in.FreezeHours, &sourceOrderID, &kind)
			if err != nil {
				return err
			}
			if !accrued {
				// 邀请人行在同一事务里刚 ensure 过，更新影响 0 行只能是数据异常。
				return fmt.Errorf("accrue first order bonus quota for inviter %d: no rows affected", in.InviterID)
			}
		}

		applied = true
		return nil
	})
	if err != nil {
		return false, err
	}
	return applied, nil
}

// insertFirstOrderBonusApplied 抢占「这个账号的首充券」。
func insertFirstOrderBonusApplied(ctx context.Context, client affiliateQueryExecer, in service.AffiliateFirstOrderBonusApplyInput) (bool, error) {
	rows, err := client.QueryContext(ctx, `
INSERT INTO user_affiliate_first_order_bonus
    (user_id, inviter_id, order_id, order_amount, invitee_bonus, inviter_bonus, status, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, NOW())
ON CONFLICT (user_id) DO NOTHING
RETURNING user_id`,
		in.UserID, in.InviterID, in.OrderID, in.OrderAmount, in.InviteeBonus, in.InviterBonus,
		service.FirstOrderBonusStatusApplied)
	if err != nil {
		return false, fmt.Errorf("insert affiliate first order bonus record: %w", err)
	}
	defer func() { _ = rows.Close() }()

	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return false, err
		}
		return false, nil
	}
	var claimedUserID int64
	if err := rows.Scan(&claimedUserID); err != nil {
		return false, err
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	return true, nil
}

// creditFirstOrderBonusBalance 给被邀请人加余额并记一条台账。
// AddBalance + AddTotalRecharged 与 TransferQuotaToBalance / UpdateBalance
// 的正数入账口径一致（充值统计要认这笔钱）。
func creditFirstOrderBonusBalance(ctx context.Context, txClient *dbent.Client, in service.AffiliateFirstOrderBonusApplyInput) error {
	if in.InviteeBonus <= 0 {
		return nil
	}
	affected, err := txClient.User.Update().
		Where(user.IDEQ(in.UserID)).
		AddBalance(in.InviteeBonus).
		AddTotalRecharged(in.InviteeBonus).
		Save(ctx)
	if err != nil {
		return fmt.Errorf("credit invitee balance by first order bonus: %w", err)
	}
	if affected == 0 {
		return service.ErrUserNotFound
	}

	balanceAfter, err := queryUserBalance(ctx, txClient, in.UserID)
	if err != nil {
		return err
	}

	if _, err := txClient.ExecContext(ctx, `
INSERT INTO user_affiliate_ledger (
    user_id,
    action,
    amount,
    source_user_id,
    source_order_id,
    balance_after,
    kind,
    created_at,
    updated_at
)
VALUES ($1, 'first_order_bonus', $2, $3, $4, $5, $6, NOW(), NOW())`,
		in.UserID,
		in.InviteeBonus,
		in.InviterID,
		in.OrderID,
		balanceAfter,
		service.AffiliateLedgerKindFirstOrderBonus,
	); err != nil {
		return fmt.Errorf("insert affiliate first order bonus ledger: %w", err)
	}
	return nil
}

// nullableStringArg 把 *string 展开成 SQL 参数：nil → NULL。
func nullableStringArg(v *string) any {
	if v == nil {
		return nil
	}
	return *v
}
