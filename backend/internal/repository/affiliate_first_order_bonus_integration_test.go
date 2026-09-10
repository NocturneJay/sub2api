//go:build integration

package repository

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// 邀请「首单双向奖励」持久层的 Postgres 集成测试（aicat 自研，2026-09）。
//
// 为什么这一层必须靠集成测试兜底：affiliate_first_order_bonus_repo.go 几乎全是
// 手写 SQL —— ON CONFLICT DO NOTHING ... RETURNING 的抢占语义、(付款时间, id)
// 元组比较、NULL 安全的 kind 过滤、SELECT ... FOR UPDATE。这几处 sqlite 与
// mock 的行为都和 Postgres 不一样，只有真库能证伪。
//
// 所有用例跑在 testEntTx 的事务里、结束自动回滚；唯一例外是
// LockUserAffiliateForUpdate 的「事务外」分支——它必须真的不在事务里才有意义，
// 所以自己建行、自己删干净。

// firstOrderBonusOrderSeed 描述一笔用于「首单」判定的订单。
// 只填判定真正读到的列（金额、状态、付款/完成时间、创建时间），
// 其余列走 092_payment_orders.sql 的默认值。
type firstOrderBonusOrderSeed struct {
	Amount      float64
	Status      string
	OrderType   string
	CreatedAt   *time.Time
	PaidAt      *time.Time
	CompletedAt *time.Time
}

// nullableTimeArg 把 *time.Time 展开成 SQL 参数：nil → NULL。
// 与 affiliate_repo.go 的 nullableInt64Arg / nullableStringArg 同一套写法，
// 不依赖 database/sql 对指针的隐式解引用。
func nullableTimeArg(v *time.Time) any {
	if v == nil {
		return nil
	}
	return *v
}

// mustCreateFirstOrderBonusOrder 插一笔 payment_orders 并返回 id。
// payment_orders 不在 ent schema 的测试 fixture 里，所以直接写 SQL：
// user_id / amount / pay_amount / expires_at 是仅有的四个 NOT NULL 且无默认值的列。
func mustCreateFirstOrderBonusOrder(t *testing.T, ctx context.Context, client *dbent.Client, userID int64, seed firstOrderBonusOrderSeed) int64 {
	t.Helper()

	createdAt := time.Now()
	if seed.CreatedAt != nil {
		createdAt = *seed.CreatedAt
	}
	orderType := seed.OrderType
	if orderType == "" {
		orderType = "balance"
	}
	status := seed.Status
	if status == "" {
		status = "PENDING"
	}

	rows, err := client.QueryContext(ctx, `
INSERT INTO payment_orders (
    user_id, amount, pay_amount, order_type, status,
    expires_at, paid_at, completed_at, created_at, updated_at
)
VALUES ($1, $2, $2, $3, $4, $5, $6, $7, $8, $8)
RETURNING id`,
		userID,
		seed.Amount,
		orderType,
		status,
		createdAt.Add(time.Hour),
		nullableTimeArg(seed.PaidAt),
		nullableTimeArg(seed.CompletedAt),
		createdAt,
	)
	require.NoError(t, err, "insert payment order")
	defer func() { _ = rows.Close() }()

	require.True(t, rows.Next(), "insert payment order returned no id")
	var orderID int64
	require.NoError(t, rows.Scan(&orderID))
	require.NoError(t, rows.Err())
	return orderID
}

// firstOrderBonusLedgerRow 是 user_affiliate_ledger 里与本功能相关的列。
// DECIMAL 列一律 ::double precision，否则 lib/pq 交回 []byte 扫不进 float64。
type firstOrderBonusLedgerRow struct {
	Action        string
	Kind          sql.NullString
	Amount        float64
	SourceUserID  sql.NullInt64
	SourceOrderID sql.NullInt64
	BalanceAfter  sql.NullFloat64
	FrozenUntil   sql.NullTime
}

func queryFirstOrderBonusLedger(t *testing.T, ctx context.Context, client *dbent.Client, userID int64) []firstOrderBonusLedgerRow {
	t.Helper()

	rows, err := client.QueryContext(ctx, `
SELECT action,
       kind,
       amount::double precision,
       source_user_id,
       source_order_id,
       balance_after::double precision,
       frozen_until
FROM user_affiliate_ledger
WHERE user_id = $1
ORDER BY id`, userID)
	require.NoError(t, err, "query affiliate ledger")
	defer func() { _ = rows.Close() }()

	var out []firstOrderBonusLedgerRow
	for rows.Next() {
		var row firstOrderBonusLedgerRow
		require.NoError(t, rows.Scan(
			&row.Action,
			&row.Kind,
			&row.Amount,
			&row.SourceUserID,
			&row.SourceOrderID,
			&row.BalanceAfter,
			&row.FrozenUntil,
		))
		out = append(out, row)
	}
	require.NoError(t, rows.Err())
	return out
}

// mustSetupFirstOrderBonusPair 建一对「邀请人 / 被邀请人」并绑定邀请关系。
// label 让同一个用例里的多个用户邮箱不冲突（UnixNano 在同一纳秒内可能重复）。
func mustSetupFirstOrderBonusPair(
	t *testing.T,
	ctx context.Context,
	repo service.AffiliateRepository,
	client *dbent.Client,
	label string,
	inviteeBalance float64,
) (inviter *service.User, invitee *service.User) {
	t.Helper()

	nonce := time.Now().UnixNano()
	inviter = mustCreateUser(t, client, &service.User{
		Email:        fmt.Sprintf("fob-%s-inviter-%d@example.com", label, nonce),
		PasswordHash: "hash",
		Role:         service.RoleUser,
		Status:       service.StatusActive,
		Concurrency:  5,
	})
	invitee = mustCreateUser(t, client, &service.User{
		Email:        fmt.Sprintf("fob-%s-invitee-%d@example.com", label, nonce),
		PasswordHash: "hash",
		Role:         service.RoleUser,
		Status:       service.StatusActive,
		Balance:      inviteeBalance,
		Concurrency:  5,
	})

	bound, err := repo.BindInviter(ctx, invitee.ID, inviter.ID)
	require.NoError(t, err, "bind inviter")
	require.True(t, bound, "invitee must bind to inviter")

	return inviter, invitee
}

// TestAffiliateRepository_ApplyFirstOrderBonus_CreditsBothSidesOnce 覆盖发放主路径：
// 一次成功要同时产生「记录 + 被邀请人余额 + 邀请人冻结额度 + 两条台账」；
// 第二次必须被主键挡住，一分钱都不能再动。
func TestAffiliateRepository_ApplyFirstOrderBonus_CreditsBothSidesOnce(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	txCtx := dbent.NewTxContext(ctx, tx)
	client := tx.Client()

	repo := NewAffiliateRepository(client, integrationDB)
	inviter, invitee := mustSetupFirstOrderBonusPair(t, txCtx, repo, client, "apply", 3.5)

	paidAt := time.Now().Add(-5 * time.Minute)
	completedAt := time.Now().Add(-4 * time.Minute)
	orderID := mustCreateFirstOrderBonusOrder(t, txCtx, client, invitee.ID, firstOrderBonusOrderSeed{
		Amount:      25,
		Status:      "COMPLETED",
		PaidAt:      &paidAt,
		CompletedAt: &completedAt,
	})

	// 冻结期 > 0：邀请人这 10$ 必须进 aff_frozen_quota，台账 frozen_until 非空。
	applied, err := repo.ApplyFirstOrderBonus(txCtx, service.AffiliateFirstOrderBonusApplyInput{
		UserID:       invitee.ID,
		InviterID:    inviter.ID,
		OrderID:      orderID,
		OrderAmount:  25,
		InviteeBonus: 10,
		InviterBonus: 10,
		Status:       service.FirstOrderBonusStatusApplied,
		FreezeHours:  72,
	})
	require.NoError(t, err)
	require.True(t, applied, "first apply must report applied=true")

	// 1) 记录表：一行 applied，金额与两侧奖励都落到位。
	record, err := repo.GetFirstOrderBonusRecord(txCtx, invitee.ID)
	require.NoError(t, err)
	require.NotNil(t, record, "applied record must be readable back")
	require.Equal(t, invitee.ID, record.UserID)
	require.NotNil(t, record.InviterID)
	require.Equal(t, inviter.ID, *record.InviterID)
	require.Equal(t, orderID, record.OrderID)
	require.InDelta(t, 25.0, record.OrderAmount, 1e-9)
	require.InDelta(t, 10.0, record.InviteeBonus, 1e-9)
	require.InDelta(t, 10.0, record.InviterBonus, 1e-9)
	require.Equal(t, service.FirstOrderBonusStatusApplied, record.Status)
	require.False(t, record.CreatedAt.IsZero())

	// 2) 被邀请人：余额与累计充值同时 +10（与 TransferQuotaToBalance 的入账口径一致）。
	require.InDelta(t, 13.5, querySingleFloat(t, txCtx, client,
		"SELECT balance::double precision FROM users WHERE id = $1", invitee.ID), 1e-9)
	require.InDelta(t, 10.0, querySingleFloat(t, txCtx, client,
		"SELECT total_recharged::double precision FROM users WHERE id = $1", invitee.ID), 1e-9)

	// 3) 邀请人：冻结期 > 0 时只动 aff_frozen_quota 与 aff_history_quota。
	require.InDelta(t, 0.0, querySingleFloat(t, txCtx, client,
		"SELECT aff_quota::double precision FROM user_affiliates WHERE user_id = $1", inviter.ID), 1e-9)
	require.InDelta(t, 10.0, querySingleFloat(t, txCtx, client,
		"SELECT aff_frozen_quota::double precision FROM user_affiliates WHERE user_id = $1", inviter.ID), 1e-9)
	require.InDelta(t, 10.0, querySingleFloat(t, txCtx, client,
		"SELECT aff_history_quota::double precision FROM user_affiliates WHERE user_id = $1", inviter.ID), 1e-9)

	// 4) 被邀请人台账：action='first_order_bonus'、kind='first_order_bonus'，
	//    balance_after 是入账后的快照（余额历史里解释这 10$ 从哪来的唯一依据）。
	inviteeLedger := queryFirstOrderBonusLedger(t, txCtx, client, invitee.ID)
	require.Len(t, inviteeLedger, 1, "invitee must get exactly one ledger row")
	require.Equal(t, "first_order_bonus", inviteeLedger[0].Action)
	require.True(t, inviteeLedger[0].Kind.Valid)
	require.Equal(t, service.AffiliateLedgerKindFirstOrderBonus, inviteeLedger[0].Kind.String)
	require.InDelta(t, 10.0, inviteeLedger[0].Amount, 1e-9)
	require.True(t, inviteeLedger[0].SourceUserID.Valid)
	require.Equal(t, inviter.ID, inviteeLedger[0].SourceUserID.Int64)
	require.True(t, inviteeLedger[0].SourceOrderID.Valid)
	require.Equal(t, orderID, inviteeLedger[0].SourceOrderID.Int64)
	require.True(t, inviteeLedger[0].BalanceAfter.Valid)
	require.InDelta(t, 13.5, inviteeLedger[0].BalanceAfter.Float64, 1e-9)
	require.False(t, inviteeLedger[0].FrozenUntil.Valid, "invitee balance credit is never frozen")

	// 5) 邀请人台账：action 沿用 'accrue'（解冻/转余额/管理端记录全部复用），
	//    额外带 kind；冻结期 > 0 所以 frozen_until 非空。
	inviterLedger := queryFirstOrderBonusLedger(t, txCtx, client, inviter.ID)
	require.Len(t, inviterLedger, 1, "inviter must get exactly one ledger row")
	require.Equal(t, "accrue", inviterLedger[0].Action)
	require.True(t, inviterLedger[0].Kind.Valid)
	require.Equal(t, service.AffiliateLedgerKindFirstOrderBonus, inviterLedger[0].Kind.String)
	require.InDelta(t, 10.0, inviterLedger[0].Amount, 1e-9)
	require.True(t, inviterLedger[0].SourceUserID.Valid)
	require.Equal(t, invitee.ID, inviterLedger[0].SourceUserID.Int64)
	require.True(t, inviterLedger[0].SourceOrderID.Valid)
	require.Equal(t, orderID, inviterLedger[0].SourceOrderID.Int64)
	require.True(t, inviterLedger[0].FrozenUntil.Valid, "freeze hours > 0 must set frozen_until")
	require.True(t, inviterLedger[0].FrozenUntil.Time.After(time.Now().Add(71*time.Hour)),
		"frozen_until should be ~72h out, got %s", inviterLedger[0].FrozenUntil.Time)

	// 6) 第二次发放：主键把它挡在门外，返回 false 且余额/额度/台账一律不动。
	//    故意换一组更大的奖励金额——如果实现漏了 ON CONFLICT，这里会被立刻放大。
	appliedAgain, err := repo.ApplyFirstOrderBonus(txCtx, service.AffiliateFirstOrderBonusApplyInput{
		UserID:       invitee.ID,
		InviterID:    inviter.ID,
		OrderID:      orderID,
		OrderAmount:  25,
		InviteeBonus: 999,
		InviterBonus: 999,
		Status:       service.FirstOrderBonusStatusApplied,
		FreezeHours:  72,
	})
	require.NoError(t, err, "second apply must not error out")
	require.False(t, appliedAgain, "second apply must report applied=false")

	require.InDelta(t, 13.5, querySingleFloat(t, txCtx, client,
		"SELECT balance::double precision FROM users WHERE id = $1", invitee.ID), 1e-9)
	require.InDelta(t, 10.0, querySingleFloat(t, txCtx, client,
		"SELECT aff_frozen_quota::double precision FROM user_affiliates WHERE user_id = $1", inviter.ID), 1e-9)
	require.Len(t, queryFirstOrderBonusLedger(t, txCtx, client, invitee.ID), 1,
		"second apply must not add an invitee ledger row")
	require.Len(t, queryFirstOrderBonusLedger(t, txCtx, client, inviter.ID), 1,
		"second apply must not add an inviter ledger row")
	require.Equal(t, 1, querySingleInt(t, txCtx, client,
		"SELECT COUNT(*) FROM user_affiliate_first_order_bonus WHERE user_id = $1", invitee.ID))

	// 记录本身也不能被第二次调用改写。
	unchanged, err := repo.GetFirstOrderBonusRecord(txCtx, invitee.ID)
	require.NoError(t, err)
	require.NotNil(t, unchanged)
	require.InDelta(t, 10.0, unchanged.InviteeBonus, 1e-9)
	require.InDelta(t, 10.0, unchanged.InviterBonus, 1e-9)
}

// TestAffiliateRepository_ApplyFirstOrderBonus_ZeroFreezeGoesToAvailableQuota 补一条
// 冻结期为 0 的路径：邀请人的 10$ 直接进 aff_quota，台账 frozen_until 为空。
// 这两条 SQL 是 accrueQuota 的两个分支，只测其中一条会漏掉另一条的列名错误。
func TestAffiliateRepository_ApplyFirstOrderBonus_ZeroFreezeGoesToAvailableQuota(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	txCtx := dbent.NewTxContext(ctx, tx)
	client := tx.Client()

	repo := NewAffiliateRepository(client, integrationDB)
	inviter, invitee := mustSetupFirstOrderBonusPair(t, txCtx, repo, client, "nofreeze", 0)

	paidAt := time.Now()
	orderID := mustCreateFirstOrderBonusOrder(t, txCtx, client, invitee.ID, firstOrderBonusOrderSeed{
		Amount:    20,
		Status:    "RECHARGING",
		OrderType: "subscription",
		PaidAt:    &paidAt,
	})

	applied, err := repo.ApplyFirstOrderBonus(txCtx, service.AffiliateFirstOrderBonusApplyInput{
		UserID:       invitee.ID,
		InviterID:    inviter.ID,
		OrderID:      orderID,
		OrderAmount:  20,
		InviteeBonus: 10,
		InviterBonus: 10,
		Status:       service.FirstOrderBonusStatusApplied,
		FreezeHours:  0,
	})
	require.NoError(t, err)
	require.True(t, applied)

	require.InDelta(t, 10.0, querySingleFloat(t, txCtx, client,
		"SELECT aff_quota::double precision FROM user_affiliates WHERE user_id = $1", inviter.ID), 1e-9)
	require.InDelta(t, 0.0, querySingleFloat(t, txCtx, client,
		"SELECT aff_frozen_quota::double precision FROM user_affiliates WHERE user_id = $1", inviter.ID), 1e-9)

	inviterLedger := queryFirstOrderBonusLedger(t, txCtx, client, inviter.ID)
	require.Len(t, inviterLedger, 1)
	require.Equal(t, "accrue", inviterLedger[0].Action)
	require.True(t, inviterLedger[0].Kind.Valid)
	require.Equal(t, service.AffiliateLedgerKindFirstOrderBonus, inviterLedger[0].Kind.String)
	require.False(t, inviterLedger[0].FrozenUntil.Valid, "freeze hours == 0 must leave frozen_until NULL")
}

// TestAffiliateRepository_ApplyFirstOrderBonus_VoidedInviteeStillPaysInviterOnce 覆盖 v2 的作废形态：
// 好友首单不满阈值时，被邀请人那份传 0 + status=void_below_threshold，邀请人那份照样累计；
// 第二次调用（哪怕换了状态、订单和金额）必须被主键挡住，一分钱都不能再动。
// 作废是不可逆的，被改写等于把用户的券状态说反。
func TestAffiliateRepository_ApplyFirstOrderBonus_VoidedInviteeStillPaysInviterOnce(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	txCtx := dbent.NewTxContext(ctx, tx)
	client := tx.Client()

	repo := NewAffiliateRepository(client, integrationDB)
	inviter, invitee := mustSetupFirstOrderBonusPair(t, txCtx, repo, client, "void", 1.25)

	firstOrderID := mustCreateFirstOrderBonusOrder(t, txCtx, client, invitee.ID, firstOrderBonusOrderSeed{
		Amount: 5,
		Status: "COMPLETED",
	})
	secondOrderID := mustCreateFirstOrderBonusOrder(t, txCtx, client, invitee.ID, firstOrderBonusOrderSeed{
		Amount: 50,
		Status: "COMPLETED",
	})

	applied, err := repo.ApplyFirstOrderBonus(txCtx, service.AffiliateFirstOrderBonusApplyInput{
		UserID:       invitee.ID,
		InviterID:    inviter.ID,
		OrderID:      firstOrderID,
		OrderAmount:  5,
		InviteeBonus: 0,
		InviterBonus: 2,
		Status:       service.FirstOrderBonusStatusVoidBelowThreshold,
		FreezeHours:  0,
	})
	require.NoError(t, err)
	require.True(t, applied, "first settlement must report a new row")

	record, err := repo.GetFirstOrderBonusRecord(txCtx, invitee.ID)
	require.NoError(t, err)
	require.NotNil(t, record)
	require.Equal(t, service.FirstOrderBonusStatusVoidBelowThreshold, record.Status)
	require.Equal(t, firstOrderID, record.OrderID)
	require.InDelta(t, 5.0, record.OrderAmount, 1e-9)
	require.InDelta(t, 0.0, record.InviteeBonus, 1e-9)
	require.InDelta(t, 2.0, record.InviterBonus, 1e-9)

	// 被邀请人：余额一分未动，也没有台账行（InviteeBonus=0 时整段跳过）。
	require.InDelta(t, 1.25, querySingleFloat(t, txCtx, client,
		"SELECT balance::double precision FROM users WHERE id = $1", invitee.ID), 1e-9)
	require.InDelta(t, 0.0, querySingleFloat(t, txCtx, client,
		"SELECT total_recharged::double precision FROM users WHERE id = $1", invitee.ID), 1e-9)
	require.Empty(t, queryFirstOrderBonusLedger(t, txCtx, client, invitee.ID))

	// 邀请人：额度与台账照常。
	require.InDelta(t, 2.0, querySingleFloat(t, txCtx, client,
		"SELECT aff_quota::double precision FROM user_affiliates WHERE user_id = $1", inviter.ID), 1e-9)
	inviterLedger := queryFirstOrderBonusLedger(t, txCtx, client, inviter.ID)
	require.Len(t, inviterLedger, 1)
	require.Equal(t, "accrue", inviterLedger[0].Action)
	require.True(t, inviterLedger[0].Kind.Valid)
	require.Equal(t, service.AffiliateLedgerKindFirstOrderBonus, inviterLedger[0].Kind.String)
	require.InDelta(t, 2.0, inviterLedger[0].Amount, 1e-9)

	// 第二次：主键挡住，记录不被改写、额度不再增加。
	appliedAgain, err := repo.ApplyFirstOrderBonus(txCtx, service.AffiliateFirstOrderBonusApplyInput{
		UserID:       invitee.ID,
		InviterID:    inviter.ID,
		OrderID:      secondOrderID,
		OrderAmount:  50,
		InviteeBonus: 10,
		InviterBonus: 10,
		Status:       service.FirstOrderBonusStatusApplied,
	})
	require.NoError(t, err, "second settlement must not error out")
	require.False(t, appliedAgain, "second settlement must report false")

	unchanged, err := repo.GetFirstOrderBonusRecord(txCtx, invitee.ID)
	require.NoError(t, err)
	require.NotNil(t, unchanged)
	require.Equal(t, service.FirstOrderBonusStatusVoidBelowThreshold, unchanged.Status,
		"existing void record must not be rewritten")
	require.Equal(t, firstOrderID, unchanged.OrderID)
	require.InDelta(t, 5.0, unchanged.OrderAmount, 1e-9)
	require.InDelta(t, 2.0, unchanged.InviterBonus, 1e-9)
	require.Equal(t, 1, querySingleInt(t, txCtx, client,
		"SELECT COUNT(*) FROM user_affiliate_first_order_bonus WHERE user_id = $1", invitee.ID))
	require.InDelta(t, 1.25, querySingleFloat(t, txCtx, client,
		"SELECT balance::double precision FROM users WHERE id = $1", invitee.ID), 1e-9)
	require.InDelta(t, 2.0, querySingleFloat(t, txCtx, client,
		"SELECT aff_quota::double precision FROM user_affiliates WHERE user_id = $1", inviter.ID), 1e-9)
	require.Empty(t, queryFirstOrderBonusLedger(t, txCtx, client, invitee.ID))
	require.Len(t, queryFirstOrderBonusLedger(t, txCtx, client, inviter.ID), 1)

	// 非法入参不写库、不报错。
	skipped, err := repo.ApplyFirstOrderBonus(txCtx, service.AffiliateFirstOrderBonusApplyInput{
		UserID:    0,
		InviterID: inviter.ID,
		OrderID:   firstOrderID,
		Status:    service.FirstOrderBonusStatusVoidExpired,
	})
	require.NoError(t, err)
	require.False(t, skipped)
}

// TestAffiliateRepository_ApplyFirstOrderBonus_ZeroInviterBonusSkipsQuota 补一条
// 「邀请人那份为 0」的路径（例如后台把首单返利率设成 0，或常规返利已经反超封顶）：
// 记录照落、被邀请人照发，但不能给邀请人写台账、不能动他的额度。
func TestAffiliateRepository_ApplyFirstOrderBonus_ZeroInviterBonusSkipsQuota(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	txCtx := dbent.NewTxContext(ctx, tx)
	client := tx.Client()

	repo := NewAffiliateRepository(client, integrationDB)
	inviter, invitee := mustSetupFirstOrderBonusPair(t, txCtx, repo, client, "nozero", 0)

	paidAt := time.Now()
	orderID := mustCreateFirstOrderBonusOrder(t, txCtx, client, invitee.ID, firstOrderBonusOrderSeed{
		Amount: 200, Status: "COMPLETED", PaidAt: &paidAt,
	})

	applied, err := repo.ApplyFirstOrderBonus(txCtx, service.AffiliateFirstOrderBonusApplyInput{
		UserID:       invitee.ID,
		InviterID:    inviter.ID,
		OrderID:      orderID,
		OrderAmount:  200,
		InviteeBonus: 10,
		InviterBonus: 0,
		Status:       service.FirstOrderBonusStatusApplied,
		FreezeHours:  0,
	})
	require.NoError(t, err)
	require.True(t, applied)

	require.InDelta(t, 10.0, querySingleFloat(t, txCtx, client,
		"SELECT balance::double precision FROM users WHERE id = $1", invitee.ID), 1e-9)
	require.Len(t, queryFirstOrderBonusLedger(t, txCtx, client, invitee.ID), 1)
	require.InDelta(t, 0.0, querySingleFloat(t, txCtx, client,
		"SELECT aff_quota::double precision FROM user_affiliates WHERE user_id = $1", inviter.ID), 1e-9)
	require.Empty(t, queryFirstOrderBonusLedger(t, txCtx, client, inviter.ID),
		"inviter bonus of 0 must not write a ledger row")
}

// TestAffiliateRepository_HasEarlierOrFulfilledPaymentOrder 覆盖「首单判定」SQL 的两条路径：
// excludeOrderID <> 0（发放路径按订单判定，两个候选集合共用 (付款时间, id) 时间序）
// 与 excludeOrderID = 0（用户端只读查询，退化成「存在任何已交付订单」）。
// 这条 SQL 决定了券会不会被一笔不该算数的订单烧掉，是整个功能里最贵的一处判断。
func TestAffiliateRepository_HasEarlierOrFulfilledPaymentOrder(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	txCtx := dbent.NewTxContext(ctx, tx)
	client := tx.Client()

	repo := NewAffiliateRepository(client, integrationDB)
	now := time.Now()

	// 必须把子测试自己的 *testing.T 传进来：在子测试里对父 t 调 require.*
	// 会在别的 goroutine 上 FailNow，Go 的 testing 会把它报成 Goexit 而不是断言失败。
	newUser := func(t *testing.T, label string) *service.User {
		t.Helper()
		return mustCreateUser(t, client, &service.User{
			Email:        fmt.Sprintf("fob-first-%s-%d@example.com", label, time.Now().UnixNano()),
			PasswordHash: "hash",
			Role:         service.RoleUser,
			Status:       service.StatusActive,
			Concurrency:  5,
		})
	}

	// 守「只有自己的订单不算数」：本单被 o.id <> $2 排除，未支付/超时/失败的订单
	// 两条分支都够不着；excludeOrderID=0 的只读查询同样判 false。
	t.Run("no other order", func(t *testing.T) {
		u := newUser(t, "solo")
		paidAt := now
		targetID := mustCreateFirstOrderBonusOrder(t, txCtx, client, u.ID, firstOrderBonusOrderSeed{
			Amount: 20, Status: "RECHARGING", PaidAt: &paidAt,
		})

		has, err := repo.HasEarlierOrFulfilledPaymentOrder(txCtx, u.ID, targetID)
		require.NoError(t, err)
		require.False(t, has, "the only order must not disqualify itself")

		// 未支付 / 超时 / 失败的订单都不是「已交付」，无论早晚都不参与判定。
		noiseCreatedAt := now.Add(-3 * time.Hour)
		mustCreateFirstOrderBonusOrder(t, txCtx, client, u.ID, firstOrderBonusOrderSeed{
			Amount: 99, Status: "PENDING", CreatedAt: &noiseCreatedAt,
		})
		mustCreateFirstOrderBonusOrder(t, txCtx, client, u.ID, firstOrderBonusOrderSeed{
			Amount: 99, Status: "EXPIRED", CreatedAt: &noiseCreatedAt,
		})
		mustCreateFirstOrderBonusOrder(t, txCtx, client, u.ID, firstOrderBonusOrderSeed{
			Amount: 99, Status: "FAILED", CreatedAt: &noiseCreatedAt,
		})

		has, err = repo.HasEarlierOrFulfilledPaymentOrder(txCtx, u.ID, targetID)
		require.NoError(t, err)
		require.False(t, has, "unpaid/expired/failed orders must not count")

		// 用户端只读查询传 excludeOrderID = 0：没有任何已交付订单 → false。
		consumed, err := repo.HasEarlierOrFulfilledPaymentOrder(txCtx, u.ID, 0)
		require.NoError(t, err)
		require.False(t, consumed)
	})

	// 守「判『曾交付』看 completed_at 而不是 status」：用户自助退款把状态改成
	// REFUND_* 之后，那笔更早的订单仍然挡得住重复领券。
	t.Run("earlier completed order disqualifies", func(t *testing.T) {
		u := newUser(t, "done-earlier")
		targetPaidAt := now
		targetID := mustCreateFirstOrderBonusOrder(t, txCtx, client, u.ID, firstOrderBonusOrderSeed{
			Amount: 20, Status: "RECHARGING", PaidAt: &targetPaidAt,
		})

		otherPaidAt := now.Add(-2 * time.Hour)
		otherCompletedAt := now.Add(-1 * time.Hour)
		mustCreateFirstOrderBonusOrder(t, txCtx, client, u.ID, firstOrderBonusOrderSeed{
			Amount:      30,
			Status:      "REFUNDED",
			PaidAt:      &otherPaidAt,
			CompletedAt: &otherCompletedAt,
		})

		has, err := repo.HasEarlierOrFulfilledPaymentOrder(txCtx, u.ID, targetID)
		require.NoError(t, err)
		require.True(t, has, "an earlier order that was once fulfilled must disqualify this one")
	})

	// 守「completed_at 分支也走同一套 (付款时间, id) 时间序」：付款更晚却先完成的
	// 订单不能反过来判死真正的首单，否则两笔都判成 not_first_order、券永远发不出去。
	// 同一份数据换成 excludeOrderID=0 仍要判 true，锁住只读查询的退化语义。
	t.Run("later completed order does not disqualify the earlier one", func(t *testing.T) {
		u := newUser(t, "done-later")
		targetPaidAt := now
		targetID := mustCreateFirstOrderBonusOrder(t, txCtx, client, u.ID, firstOrderBonusOrderSeed{
			Amount: 20, Status: "RECHARGING", PaidAt: &targetPaidAt,
		})

		otherPaidAt := now.Add(2 * time.Hour)
		otherCompletedAt := now.Add(2 * time.Hour)
		mustCreateFirstOrderBonusOrder(t, txCtx, client, u.ID, firstOrderBonusOrderSeed{
			Amount:      30,
			Status:      "REFUNDED",
			PaidAt:      &otherPaidAt,
			CompletedAt: &otherCompletedAt,
		})

		has, err := repo.HasEarlierOrFulfilledPaymentOrder(txCtx, u.ID, targetID)
		require.NoError(t, err)
		require.False(t, has, "a later-paid order must not disqualify the real first order")

		consumed, err := repo.HasEarlierOrFulfilledPaymentOrder(txCtx, u.ID, 0)
		require.NoError(t, err)
		require.True(t, consumed, "excludeOrderID=0 must degrade to 'any fulfilled order'")
	})

	// 守「更早付款且在履约中的订单拥有首单决定权」，以及这条判定不依赖 id 顺序、
	// 反向查询不会互相判死（否则并发两笔会双双跳过）。
	t.Run("earlier paid order still in flight", func(t *testing.T) {
		u := newUser(t, "earlier")
		// 先建 target（id 更小），再建付款更早的那笔（id 更大）：
		// 证明元组比较里 paid_at 说了算、id 只是同刻的 tiebreaker。
		targetPaidAt := now
		targetID := mustCreateFirstOrderBonusOrder(t, txCtx, client, u.ID, firstOrderBonusOrderSeed{
			Amount: 20, Status: "RECHARGING", PaidAt: &targetPaidAt,
		})
		earlierPaidAt := now.Add(-2 * time.Hour)
		earlierID := mustCreateFirstOrderBonusOrder(t, txCtx, client, u.ID, firstOrderBonusOrderSeed{
			Amount: 30, Status: "PAID", PaidAt: &earlierPaidAt,
		})
		require.Greater(t, earlierID, targetID, "fixture must give the earlier-paid order a larger id")

		has, err := repo.HasEarlierOrFulfilledPaymentOrder(txCtx, u.ID, targetID)
		require.NoError(t, err)
		require.True(t, has, "an earlier-paid in-flight order owns the first-order decision")

		// 反过来：那笔更早的订单自己判定时看到本单付款更晚，不受影响。
		has, err = repo.HasEarlierOrFulfilledPaymentOrder(txCtx, u.ID, earlierID)
		require.NoError(t, err)
		require.False(t, has, "the earlier order must not be disqualified by the later one")
	})

	// 守 COALESCE(paid_at, created_at)：付款时间为空的订单要退回创建时间比较，
	// 否则 NULL 让整个元组比较变 NULL、更早的订单挡不住本单。
	t.Run("earlier paid order without paid_at falls back to created_at", func(t *testing.T) {
		u := newUser(t, "coalesce")
		targetPaidAt := now
		targetID := mustCreateFirstOrderBonusOrder(t, txCtx, client, u.ID, firstOrderBonusOrderSeed{
			Amount: 20, Status: "RECHARGING", PaidAt: &targetPaidAt,
		})
		earlierCreatedAt := now.Add(-6 * time.Hour)
		mustCreateFirstOrderBonusOrder(t, txCtx, client, u.ID, firstOrderBonusOrderSeed{
			Amount: 30, Status: "PAID", CreatedAt: &earlierCreatedAt,
		})

		has, err := repo.HasEarlierOrFulfilledPaymentOrder(txCtx, u.ID, targetID)
		require.NoError(t, err)
		require.True(t, has, "COALESCE(paid_at, created_at) must fall back to created_at")
	})

	// 守「更晚付款的在途订单不能抢走首单决定权」（与上一个用例互为反向）。
	t.Run("later recharging order does not count", func(t *testing.T) {
		u := newUser(t, "later")
		targetPaidAt := now
		targetID := mustCreateFirstOrderBonusOrder(t, txCtx, client, u.ID, firstOrderBonusOrderSeed{
			Amount: 20, Status: "COMPLETED", PaidAt: &targetPaidAt,
		})
		laterPaidAt := now.Add(2 * time.Hour)
		mustCreateFirstOrderBonusOrder(t, txCtx, client, u.ID, firstOrderBonusOrderSeed{
			Amount: 30, Status: "RECHARGING", PaidAt: &laterPaidAt,
		})

		has, err := repo.HasEarlierOrFulfilledPaymentOrder(txCtx, u.ID, targetID)
		require.NoError(t, err)
		require.False(t, has, "a later in-flight order must not steal the first-order decision")
	})

	// 守 o.user_id = $1 的隔离：别人的已交付订单不能烧掉本账号的券。
	t.Run("other users orders are ignored", func(t *testing.T) {
		mine := newUser(t, "mine")
		stranger := newUser(t, "stranger")

		targetPaidAt := now
		targetID := mustCreateFirstOrderBonusOrder(t, txCtx, client, mine.ID, firstOrderBonusOrderSeed{
			Amount: 20, Status: "RECHARGING", PaidAt: &targetPaidAt,
		})
		strangerPaidAt := now.Add(-4 * time.Hour)
		strangerCompletedAt := now.Add(-3 * time.Hour)
		mustCreateFirstOrderBonusOrder(t, txCtx, client, stranger.ID, firstOrderBonusOrderSeed{
			Amount: 99, Status: "COMPLETED", PaidAt: &strangerPaidAt, CompletedAt: &strangerCompletedAt,
		})

		has, err := repo.HasEarlierOrFulfilledPaymentOrder(txCtx, mine.ID, targetID)
		require.NoError(t, err)
		require.False(t, has)
	})

	// 守非法 userID 的短路：repo 层直接返回 false, nil，不发查询。
	t.Run("invalid user id", func(t *testing.T) {
		has, err := repo.HasEarlierOrFulfilledPaymentOrder(txCtx, 0, 0)
		require.NoError(t, err)
		require.False(t, has)
	})
}

// TestAffiliateRepository_GetAccruedRebateFromInvitee_ExcludesFirstOrderBonusKind 锁定
// 「首单奖励不占单人返利上限」这条口径：kind='first_order_bonus' 的 accrue 行要被剔除，
// 而历史行（kind IS NULL）与将来可能出现的其他 kind 都必须照常计入。
// SQL 里 `NULL <> 'x'` 结果是 NULL 而不是 true，漏写 `kind IS NULL OR` 会把
// 所有历史返利一次性清零 —— 这正是本用例要挡住的事故。
func TestAffiliateRepository_GetAccruedRebateFromInvitee_ExcludesFirstOrderBonusKind(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	txCtx := dbent.NewTxContext(ctx, tx)
	client := tx.Client()

	repo := NewAffiliateRepository(client, integrationDB)
	inviter, invitee := mustSetupFirstOrderBonusPair(t, txCtx, repo, client, "kind", 0)

	paidAt := time.Now()
	orderID := mustCreateFirstOrderBonusOrder(t, txCtx, client, invitee.ID, firstOrderBonusOrderSeed{
		Amount: 20, Status: "COMPLETED", PaidAt: &paidAt,
	})

	// 常规 20% 返利：走生产路径 AccrueQuota，kind 必须落 NULL。
	accrued, err := repo.AccrueQuota(txCtx, inviter.ID, invitee.ID, 4, 0, &orderID)
	require.NoError(t, err)
	require.True(t, accrued)

	// 首单奖励：同样走生产路径，kind 落 'first_order_bonus'。
	applied, err := repo.ApplyFirstOrderBonus(txCtx, service.AffiliateFirstOrderBonusApplyInput{
		UserID:       invitee.ID,
		InviterID:    inviter.ID,
		OrderID:      orderID,
		OrderAmount:  20,
		InviteeBonus: 10,
		InviterBonus: 10,
		Status:       service.FirstOrderBonusStatusApplied,
		FreezeHours:  0,
	})
	require.NoError(t, err)
	require.True(t, applied)

	// 未来新增的 kind 不应被这条过滤误伤（只排除 first_order_bonus 一个值）。
	_, err = client.ExecContext(txCtx, `
INSERT INTO user_affiliate_ledger (user_id, action, amount, source_user_id, source_order_id, kind, created_at, updated_at)
VALUES ($1, 'accrue', $2, $3, $4, 'some_future_kind', NOW(), NOW())`,
		inviter.ID, 1.0, invitee.ID, orderID)
	require.NoError(t, err)

	// 台账里三行确实都在（4 + 10 + 1）。
	require.InDelta(t, 15.0, querySingleFloat(t, txCtx, client,
		`SELECT COALESCE(SUM(amount), 0)::double precision FROM user_affiliate_ledger
WHERE user_id = $1 AND source_user_id = $2 AND action = 'accrue'`, inviter.ID, invitee.ID), 1e-9)

	// 但「单人返利上限」的统计口径只看得到 4 + 1。
	total, err := repo.GetAccruedRebateFromInvitee(txCtx, inviter.ID, invitee.ID)
	require.NoError(t, err)
	require.InDelta(t, 5.0, total, 1e-9,
		"first_order_bonus rows must be excluded while NULL/other kinds stay counted")

	// 额度本身仍然是 4 + 10 = 14（第三行是手写的纯台账行，不动额度）：
	// 首单奖励是真实累计的返利额度，照常解冻、照常转余额，只是不占单人上限。
	require.InDelta(t, 14.0, querySingleFloat(t, txCtx, client,
		"SELECT aff_quota::double precision FROM user_affiliates WHERE user_id = $1", inviter.ID), 1e-9)
}

// TestAffiliateRepository_GetUserAffiliateReadOnly_NeverInserts 锁定只读契约：
// 用户端每次 GET 券状态都会走这条路径，绝不能顺手给未绑定邀请人的用户建行
// （建了行就等于凭空造出一个「已注册邀请体系」的用户，aff_code 也会被浪费）。
func TestAffiliateRepository_GetUserAffiliateReadOnly_NeverInserts(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	txCtx := dbent.NewTxContext(ctx, tx)
	client := tx.Client()

	repo := NewAffiliateRepository(client, integrationDB)

	u := mustCreateUser(t, client, &service.User{
		Email:        fmt.Sprintf("fob-readonly-%d@example.com", time.Now().UnixNano()),
		PasswordHash: "hash",
		Role:         service.RoleUser,
		Status:       service.StatusActive,
		Concurrency:  5,
	})

	// 1) 用户存在但没有 user_affiliates 行 → nil, nil，且不建行。
	summary, err := repo.GetUserAffiliateReadOnly(txCtx, u.ID)
	require.NoError(t, err)
	require.Nil(t, summary, "missing affiliate row must return nil, nil")
	require.Equal(t, 0, querySingleInt(t, txCtx, client,
		"SELECT COUNT(*) FROM user_affiliates WHERE user_id = $1", u.ID),
		"read-only lookup must not create an affiliate row")

	// 2) 用户根本不存在 → 同样是 nil, nil（不是 ErrAffiliateProfileNotFound）。
	missing, err := repo.GetUserAffiliateReadOnly(txCtx, int64(1)<<40)
	require.NoError(t, err)
	require.Nil(t, missing)

	// 3) 非法 id 直接短路。
	invalid, err := repo.GetUserAffiliateReadOnly(txCtx, 0)
	require.NoError(t, err)
	require.Nil(t, invalid)

	// 4) 有行之后要能读出完整字段（含 inviter_id 与 created_at —— 券有效期就是
	//    从 user_affiliates.created_at 起算的，读错这一列会把有效期算歪）。
	created, err := repo.EnsureUserAffiliate(txCtx, u.ID)
	require.NoError(t, err)
	require.NotNil(t, created)

	read, err := repo.GetUserAffiliateReadOnly(txCtx, u.ID)
	require.NoError(t, err)
	require.NotNil(t, read)
	require.Equal(t, u.ID, read.UserID)
	require.Equal(t, created.AffCode, read.AffCode)
	require.Nil(t, read.InviterID, "a user without an inviter must read back nil")
	require.False(t, read.CreatedAt.IsZero(), "created_at drives the coupon expiry window")

	inviter := mustCreateUser(t, client, &service.User{
		Email:        fmt.Sprintf("fob-readonly-inviter-%d@example.com", time.Now().UnixNano()),
		PasswordHash: "hash",
		Role:         service.RoleUser,
		Status:       service.StatusActive,
		Concurrency:  5,
	})
	bound, err := repo.BindInviter(txCtx, u.ID, inviter.ID)
	require.NoError(t, err)
	require.True(t, bound)

	afterBind, err := repo.GetUserAffiliateReadOnly(txCtx, u.ID)
	require.NoError(t, err)
	require.NotNil(t, afterBind)
	require.NotNil(t, afterBind.InviterID)
	require.Equal(t, inviter.ID, *afterBind.InviterID)
}

// TestAffiliateRepository_LockUserAffiliateForUpdate 覆盖行锁的三种入参与
// 「事务外调用」这一条容易被误解的行为。
//
// 结论（与 affiliate_first_order_bonus_repo.go 的实现一致）：
//   - 事务内：拿到行锁，同一用户的并发首单被串行化；
//   - 行不存在 / userID <= 0：静默返回 nil，不报错（并发删号不该打断一笔已到账的订单）；
//   - 事务外：SELECT ... FOR UPDATE 跑在 Postgres 的隐式事务里，语句一结束锁就没了，
//     所以它既不会报错、也提供不了任何保护。真正的最后一道闸永远是新表主键。
func TestAffiliateRepository_LockUserAffiliateForUpdate(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	txCtx := dbent.NewTxContext(ctx, tx)
	client := tx.Client()

	repo := NewAffiliateRepository(client, integrationDB)

	u := mustCreateUser(t, client, &service.User{
		Email:        fmt.Sprintf("fob-lock-%d@example.com", time.Now().UnixNano()),
		PasswordHash: "hash",
		Role:         service.RoleUser,
		Status:       service.StatusActive,
		Concurrency:  5,
	})
	_, err := repo.EnsureUserAffiliate(txCtx, u.ID)
	require.NoError(t, err)

	// 事务内锁一行：成功，并且可重入（同一事务里再锁一次不会自我阻塞）。
	require.NoError(t, repo.LockUserAffiliateForUpdate(txCtx, u.ID))
	require.NoError(t, repo.LockUserAffiliateForUpdate(txCtx, u.ID))

	// 没有行可锁：不报错。
	require.NoError(t, repo.LockUserAffiliateForUpdate(txCtx, int64(1)<<40))

	// 非法 id：直接短路，不发 SQL。
	require.NoError(t, repo.LockUserAffiliateForUpdate(txCtx, 0))

	// 事务外分支必须用真正不在事务里的 client，所以这一段自己建行、自己删。
	standalone := mustCreateUser(t, integrationEntClient, &service.User{
		Email:        fmt.Sprintf("fob-lock-standalone-%d@example.com", time.Now().UnixNano()),
		PasswordHash: "hash",
		Role:         service.RoleUser,
		Status:       service.StatusActive,
		Concurrency:  5,
	})
	t.Cleanup(func() {
		if _, err := integrationEntClient.ExecContext(ctx,
			"DELETE FROM user_affiliates WHERE user_id = $1", standalone.ID); err != nil {
			t.Errorf("cleanup user_affiliates: %v", err)
		}
		if _, err := integrationEntClient.ExecContext(ctx,
			"DELETE FROM users WHERE id = $1", standalone.ID); err != nil {
			t.Errorf("cleanup users: %v", err)
		}
	})

	globalRepo := NewAffiliateRepository(integrationEntClient, integrationDB)
	_, err = globalRepo.EnsureUserAffiliate(ctx, standalone.ID)
	require.NoError(t, err)

	require.NoError(t, globalRepo.LockUserAffiliateForUpdate(ctx, standalone.ID),
		"lock outside a transaction must not error")

	// NOWAIT 会在锁仍被持有时立刻报错。它成功 = 上一句的锁已经随隐式事务释放，
	// 也就是「事务外调用拿不到任何保护」这条契约。
	probeTx, err := integrationEntClient.Tx(ctx)
	require.NoError(t, err, "begin lock probe tx")
	defer func() { _ = probeTx.Rollback() }()

	probeRows, err := probeTx.Client().QueryContext(ctx,
		"SELECT user_id FROM user_affiliates WHERE user_id = $1 FOR UPDATE NOWAIT", standalone.ID)
	require.NoError(t, err, "row must not still be locked after a non-transactional lock call")
	defer func() { _ = probeRows.Close() }()

	require.True(t, probeRows.Next(), "expected the affiliate row")
	var probedUserID int64
	require.NoError(t, probeRows.Scan(&probedUserID))
	require.NoError(t, probeRows.Err())
	require.Equal(t, standalone.ID, probedUserID)
}
