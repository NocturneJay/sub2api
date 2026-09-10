//go:build unit

package service

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"entgo.io/ent/dialect"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/paymentauditlog"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/stretchr/testify/require"
)

// 首单双向奖励在订单履约链路上的用例。
//
// 审计抢占的 SQL 是双方言拼出来的，本机跑不了 Postgres —— 那一份只能靠纯
// 字符串断言守住占位符编号与 args 顺序（v1 就是在这里把常规返利的审计行
// 改写掉、导致重试时 20% 返利重复发放）。

const (
	firstOrderBonusEnabledSettingValue  = `{"enabled":true,"threshold":20,"invitee_bonus":10,"inviter_rate_percent":50,"inviter_cap":10,"valid_days":30}`
	firstOrderBonusDisabledSettingValue = `{"enabled":false,"threshold":20,"invitee_bonus":10,"inviter_rate_percent":50,"inviter_cap":10,"valid_days":30}`
)

// ---------------------------------------------------------------------------
// 审计抢占 SQL：双方言字符串断言
// ---------------------------------------------------------------------------

func TestBuildAffiliateAuditClaimQueryPostgresPlaceholders(t *testing.T) {
	t.Parallel()

	query, args := buildAffiliateAuditClaimQuery(dialect.Postgres, "77", `{"baseAmount":20}`, affiliateRebateAuditClaim)

	require.Contains(t, query, "SELECT $1::text, $3::text, $2::text, 'system', NOW()")
	require.Contains(t, query, "WHERE order_id = $1::text")
	require.Contains(t, query, "AND action IN ($3::text, $4::text)")
	require.Contains(t, query, "ON CONFLICT (order_id, action) DO NOTHING")
	require.Contains(t, query, "RETURNING id")
	// 动作名一律走参数，SQL 文本里不能再出现字面量。
	require.NotContains(t, query, "AFFILIATE_REBATE_APPLIED")
	require.NotContains(t, query, "AFFILIATE_REBATE_SKIPPED")

	require.Equal(t, []any{"77", `{"baseAmount":20}`, "AFFILIATE_REBATE_APPLIED", "AFFILIATE_REBATE_SKIPPED"}, args)
}

func TestBuildAffiliateAuditClaimQuerySQLitePositionalArgsFollowQuestionMarkOrder(t *testing.T) {
	t.Parallel()

	query, args := buildAffiliateAuditClaimQuery(dialect.SQLite, "77", `{"baseAmount":20}`, affiliateFirstOrderBonusAuditClaim)

	require.Contains(t, query, "SELECT ?, ?, ?, 'system', CURRENT_TIMESTAMP")
	require.Contains(t, query, "WHERE order_id = ?")
	require.Contains(t, query, "AND action IN (?, ?)")
	require.NotContains(t, query, "AFFILIATE_FIRST_ORDER_BONUS_APPLIED")

	// sqlite 只认位置参数：顺序必须严格跟着 ? 在 SQL 里出现的先后。
	require.Equal(t, 6, strings.Count(query, "?"))
	require.Equal(t, []any{
		"77",
		"AFFILIATE_FIRST_ORDER_BONUS_APPLIED",
		`{"baseAmount":20}`,
		"77",
		"AFFILIATE_FIRST_ORDER_BONUS_APPLIED",
		"AFFILIATE_FIRST_ORDER_BONUS_SKIPPED",
	}, args)
}

func TestBuildAffiliateAuditClaimQueryKeepsTheTwoClaimsDisjoint(t *testing.T) {
	t.Parallel()

	// 两组动作名不能有任何重叠：重叠就意味着一方能抢占/改写另一方的审计行。
	require.NotEqual(t, affiliateRebateAuditClaim.appliedAction, affiliateFirstOrderBonusAuditClaim.appliedAction)
	require.NotEqual(t, affiliateRebateAuditClaim.appliedAction, affiliateFirstOrderBonusAuditClaim.skippedAction)
	require.NotEqual(t, affiliateRebateAuditClaim.skippedAction, affiliateFirstOrderBonusAuditClaim.appliedAction)
	require.NotEqual(t, affiliateRebateAuditClaim.skippedAction, affiliateFirstOrderBonusAuditClaim.skippedAction)

	// 两组各自生成的 args 里，订单号与 detail 是共享的（同一笔订单），
	// 只有动作名必须互不出现：常规返利的动作名不能进首单奖励的 args，反之亦然。
	rebateActions := []string{affiliateRebateAuditClaim.appliedAction, affiliateRebateAuditClaim.skippedAction}
	bonusActions := []string{affiliateFirstOrderBonusAuditClaim.appliedAction, affiliateFirstOrderBonusAuditClaim.skippedAction}
	for _, d := range []string{dialect.Postgres, dialect.SQLite} {
		_, rebateArgs := buildAffiliateAuditClaimQuery(d, "77", "{}", affiliateRebateAuditClaim)
		_, bonusArgs := buildAffiliateAuditClaimQuery(d, "77", "{}", affiliateFirstOrderBonusAuditClaim)
		for _, action := range rebateActions {
			require.NotContains(t, bonusArgs, action, "dialect %s: 首单奖励的 args 不该出现常规返利的动作名", d)
		}
		for _, action := range bonusActions {
			require.NotContains(t, rebateArgs, action, "dialect %s: 常规返利的 args 不该出现首单奖励的动作名", d)
		}
	}
}

// ---------------------------------------------------------------------------
// 履约链路
// ---------------------------------------------------------------------------

func newFirstOrderBonusFulfillmentOrder(t *testing.T, ctx context.Context, client *dbent.Client, userID int64, email, username string, orderType string, amount float64) *dbent.PaymentOrder {
	t.Helper()
	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	builder := client.PaymentOrder.Create().
		SetUserID(userID).
		SetUserEmail(email).
		SetUserName(username).
		SetAmount(amount).
		SetPayAmount(amount).
		SetFeeRate(0).
		SetRechargeCode("PAY-FOB-" + suffix).
		SetOutTradeNo("sub2_first_order_bonus_" + suffix).
		SetPaymentType(payment.TypeAlipay).
		SetPaymentTradeNo("trade-fob-" + suffix).
		SetOrderType(orderType).
		SetStatus(OrderStatusPaid).
		SetExpiresAt(time.Now().Add(time.Hour)).
		SetClientIP("127.0.0.1").
		SetSrcHost("api.example.com")
	if orderType == payment.OrderTypeSubscription {
		builder = builder.SetPlanID(99).SetSubscriptionGroupID(7).SetSubscriptionDays(30)
	}
	order, err := builder.Save(ctx)
	require.NoError(t, err)
	return order
}

func newFirstOrderBonusFulfillmentUser(t *testing.T, ctx context.Context, client *dbent.Client, email string) *dbent.User {
	t.Helper()
	user, err := client.User.Create().
		SetEmail(email).
		SetPasswordHash("hash").
		SetUsername(strings.SplitN(email, "@", 2)[0]).
		Save(ctx)
	require.NoError(t, err)
	return user
}

// newFirstOrderBonusFulfillmentService 组装一个订阅履约用的 PaymentService。
// bonusSetting 传空串表示 settings 里没有这个键（= 功能默认关闭）。
func newFirstOrderBonusFulfillmentService(client *dbent.Client, affiliateRepo AffiliateRepository, bonusSetting string) (*PaymentService, *subscriptionUserSubRepoStub) {
	values := map[string]string{
		SettingKeyAffiliateEnabled:           "true",
		SettingKeyAffiliateRebateRate:        "10",
		SettingKeyAffiliateRebateFreezeHours: "0",
	}
	if bonusSetting != "" {
		values[SettingKeyAffiliateFirstOrderBonus] = bonusSetting
	}
	settingSvc := NewSettingService(&paymentFulfillmentSettingRepoStub{values: values}, nil)
	groupRepo := &subscriptionGroupRepoStub{
		group: &Group{ID: 7, Status: payment.EntityStatusActive, SubscriptionType: SubscriptionTypeSubscription},
	}
	subRepo := newSubscriptionUserSubRepoStub()
	return &PaymentService{
		entClient:        client,
		groupRepo:        groupRepo,
		subscriptionSvc:  NewSubscriptionService(groupRepo, subRepo, nil, nil, nil),
		affiliateService: NewAffiliateService(affiliateRepo, settingSvc, nil, nil),
	}, subRepo
}

func countPaymentAuditAction(t *testing.T, ctx context.Context, client *dbent.Client, orderID int64, action string) int {
	t.Helper()
	count, err := client.PaymentAuditLog.Query().
		Where(paymentauditlog.OrderIDEQ(strconv.FormatInt(orderID, 10)), paymentauditlog.ActionEQ(action)).
		Count(ctx)
	require.NoError(t, err)
	return count
}

func paymentAuditActionsForOrder(t *testing.T, ctx context.Context, client *dbent.Client, orderID int64) []string {
	t.Helper()
	logs, err := client.PaymentAuditLog.Query().
		Where(paymentauditlog.OrderIDEQ(strconv.FormatInt(orderID, 10))).
		All(ctx)
	require.NoError(t, err)
	actions := make([]string, 0, len(logs))
	for _, l := range logs {
		actions = append(actions, l.Action)
	}
	return actions
}

func newFirstOrderBonusAffiliateRepoStub(inviteeUserID, inviterID int64) *paymentFulfillmentAffiliateRepoStub {
	return &paymentFulfillmentAffiliateRepoStub{
		inviteeSummary: &AffiliateSummary{
			UserID:    inviteeUserID,
			AffCode:   "INVITEE",
			InviterID: &inviterID,
			CreatedAt: time.Now().Add(-24 * time.Hour),
		},
		inviterSummary: &AffiliateSummary{
			UserID:    inviterID,
			AffCode:   "INVITER",
			CreatedAt: time.Now().Add(-48 * time.Hour),
		},
	}
}

func TestExecuteSubscriptionFulfillmentAppliesFirstOrderBonusAlongsideRebate(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	ensurePaymentAuditOrderActionUniqueIndex(t, ctx, client)

	user := newFirstOrderBonusFulfillmentUser(t, ctx, client, "first-order-bonus-applied@example.com")
	order := newFirstOrderBonusFulfillmentOrder(t, ctx, client, user.ID, user.Email, user.Username, payment.OrderTypeSubscription, 50)

	affiliateRepo := newFirstOrderBonusAffiliateRepoStub(user.ID, 9001)
	affiliateRepo.firstOrderApplyResult = true
	svc, subRepo := newFirstOrderBonusFulfillmentService(client, affiliateRepo, firstOrderBonusEnabledSettingValue)

	require.NoError(t, svc.ExecuteSubscriptionFulfillment(ctx, order.ID))

	reloaded, err := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, OrderStatusCompleted, reloaded.Status)
	require.Equal(t, 1, subRepo.createCalls)

	// 常规比例返利照常发，一行不受影响（10% × 50 = 5）。
	require.Len(t, affiliateRepo.accrueCalls, 1)
	require.InDelta(t, 5.0, affiliateRepo.accrueCalls[0].amount, 1e-8)

	// 首单奖励也发了，参数来自实时配置。
	require.Len(t, affiliateRepo.firstOrderApplyCalls, 1)
	applyIn := affiliateRepo.firstOrderApplyCalls[0]
	require.Equal(t, user.ID, applyIn.UserID)
	require.Equal(t, int64(9001), applyIn.InviterID)
	require.Equal(t, order.ID, applyIn.OrderID)
	require.InDelta(t, 50.0, applyIn.OrderAmount, 1e-8)
	require.InDelta(t, 10.0, applyIn.InviteeBonus, 1e-8)
	// 50 × 50% = 25，封顶 10；常规 10% 已发 5 → 额外 5。
	require.InDelta(t, 5.0, applyIn.InviterBonus, 1e-8)
	require.Equal(t, FirstOrderBonusStatusApplied, applyIn.Status)
	require.Equal(t, []int64{user.ID}, affiliateRepo.firstOrderLockCalls)

	require.Equal(t, 1, countPaymentAuditAction(t, ctx, client, order.ID, "AFFILIATE_REBATE_APPLIED"))
	require.Equal(t, 1, countPaymentAuditAction(t, ctx, client, order.ID, "AFFILIATE_FIRST_ORDER_BONUS_APPLIED"))
	require.Zero(t, countPaymentAuditAction(t, ctx, client, order.ID, "AFFILIATE_REBATE_SKIPPED"))
	require.Zero(t, countPaymentAuditAction(t, ctx, client, order.ID, "AFFILIATE_FIRST_ORDER_BONUS_SKIPPED"))

	applied, err := client.PaymentAuditLog.Query().
		Where(
			paymentauditlog.OrderIDEQ(strconv.FormatInt(order.ID, 10)),
			paymentauditlog.ActionEQ("AFFILIATE_FIRST_ORDER_BONUS_APPLIED"),
		).Only(ctx)
	require.NoError(t, err)
	require.Contains(t, applied.Detail, `"orderAmount":50`)
	require.Contains(t, applied.Detail, `"inviteeBonus":10`)
	require.Contains(t, applied.Detail, `"inviterBonus":5`)
	require.Contains(t, applied.Detail, `"inviteeStatus":"applied"`)
	require.Contains(t, applied.Detail, `"inviterID":9001`)

	// 常规返利那一行必须还是自己的 detail，没有被首单奖励改写。
	rebate, err := client.PaymentAuditLog.Query().
		Where(
			paymentauditlog.OrderIDEQ(strconv.FormatInt(order.ID, 10)),
			paymentauditlog.ActionEQ("AFFILIATE_REBATE_APPLIED"),
		).Only(ctx)
	require.NoError(t, err)
	require.Contains(t, rebate.Detail, `"rebateAmount":5`)
}

func TestExecuteBalanceFulfillmentVoidsInviteeButStillPaysInviterBelowThreshold(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	ensurePaymentAuditOrderActionUniqueIndex(t, ctx, client)

	user := newFirstOrderBonusFulfillmentUser(t, ctx, client, "first-order-bonus-below@example.com")
	order := newFirstOrderBonusFulfillmentOrder(t, ctx, client, user.ID, user.Email, user.Username, payment.OrderTypeBalance, 5)

	affiliateRepo := newFirstOrderBonusAffiliateRepoStub(user.ID, 9002)
	// v2：不满阈值也会落表（status=void_below_threshold），所以这里必须让抢占成功。
	affiliateRepo.firstOrderApplyResult = true
	svc, _ := newFirstOrderBonusFulfillmentService(client, affiliateRepo, firstOrderBonusEnabledSettingValue)
	// 余额单走「兑换码已使用」这条幂等分支，不必搭真的兑换链路。
	// 上游 v0.2.2（7a70de401）起 validatePaymentRedeemCode 要求已用码的 UsedBy 必须等于订单用户，夹具随之带上。
	usedBy := user.ID
	svc.redeemService = &RedeemService{redeemRepo: &redeemCodeRepoStub{codesByCode: map[string]*RedeemCode{
		order.RechargeCode: {ID: 501, Code: order.RechargeCode, Type: RedeemTypeBalance, Value: order.Amount, Status: StatusUsed, UsedBy: &usedBy},
	}}}

	require.NoError(t, svc.ExecuteBalanceFulfillment(ctx, order.ID))

	reloaded, err := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, OrderStatusCompleted, reloaded.Status)

	// v2：不到阈值只作废被邀请人那份，邀请人照样按比例拿
	// （5 × 50% = 2.5，减常规 10% 的 0.5 → 额外 2），所以走 APPLIED 分支。
	require.Len(t, affiliateRepo.firstOrderApplyCalls, 1)
	applyIn := affiliateRepo.firstOrderApplyCalls[0]
	require.Equal(t, FirstOrderBonusStatusVoidBelowThreshold, applyIn.Status)
	require.Equal(t, order.ID, applyIn.OrderID)
	require.InDelta(t, 5.0, applyIn.OrderAmount, 1e-8)
	require.InDelta(t, 0.0, applyIn.InviteeBonus, 1e-8)
	require.InDelta(t, 2.0, applyIn.InviterBonus, 1e-8)

	require.Equal(t, 1, countPaymentAuditAction(t, ctx, client, order.ID, "AFFILIATE_FIRST_ORDER_BONUS_APPLIED"))
	require.Zero(t, countPaymentAuditAction(t, ctx, client, order.ID, "AFFILIATE_FIRST_ORDER_BONUS_SKIPPED"))

	applied, err := client.PaymentAuditLog.Query().
		Where(
			paymentauditlog.OrderIDEQ(strconv.FormatInt(order.ID, 10)),
			paymentauditlog.ActionEQ("AFFILIATE_FIRST_ORDER_BONUS_APPLIED"),
		).Only(ctx)
	require.NoError(t, err)
	require.Contains(t, applied.Detail, `"reason":"below_threshold"`)
	require.Contains(t, applied.Detail, `"inviteeStatus":"void_below_threshold"`)
	require.Contains(t, applied.Detail, `"inviteeBonus":0`)
	require.Contains(t, applied.Detail, `"inviterBonus":2`)
	require.Contains(t, applied.Detail, `"orderAmount":5`)

	// 常规返利与它无关，照常发。
	require.Len(t, affiliateRepo.accrueCalls, 1)
	require.Equal(t, 1, countPaymentAuditAction(t, ctx, client, order.ID, "AFFILIATE_REBATE_APPLIED"))
}

func TestExecuteSubscriptionFulfillmentDoesNotRepeatFirstOrderBonusOnRetry(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	ensurePaymentAuditOrderActionUniqueIndex(t, ctx, client)

	user := newFirstOrderBonusFulfillmentUser(t, ctx, client, "first-order-bonus-retry@example.com")
	order := newFirstOrderBonusFulfillmentOrder(t, ctx, client, user.ID, user.Email, user.Username, payment.OrderTypeSubscription, 50)

	affiliateRepo := newFirstOrderBonusAffiliateRepoStub(user.ID, 9003)
	affiliateRepo.firstOrderApplyResult = true
	svc, _ := newFirstOrderBonusFulfillmentService(client, affiliateRepo, firstOrderBonusEnabledSettingValue)

	require.NoError(t, svc.ExecuteSubscriptionFulfillment(ctx, order.ID))
	require.Len(t, affiliateRepo.firstOrderApplyCalls, 1)

	// 模拟重试：把订单退回 PAID 再跑一遍（审计行是唯一的闸门）。
	_, err := client.PaymentOrder.UpdateOneID(order.ID).SetStatus(OrderStatusPaid).ClearCompletedAt().Save(ctx)
	require.NoError(t, err)
	require.NoError(t, svc.ExecuteSubscriptionFulfillment(ctx, order.ID))

	require.Len(t, affiliateRepo.firstOrderApplyCalls, 1, "重试不得重复发放首单奖励")
	require.Len(t, affiliateRepo.accrueCalls, 1, "重试不得重复发放常规返利")
	require.Equal(t, 1, countPaymentAuditAction(t, ctx, client, order.ID, "AFFILIATE_FIRST_ORDER_BONUS_APPLIED"))
	require.Equal(t, 1, countPaymentAuditAction(t, ctx, client, order.ID, "AFFILIATE_REBATE_APPLIED"))
}

func TestExecuteSubscriptionFulfillmentWritesNoFirstOrderAuditWhenDisabled(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	ensurePaymentAuditOrderActionUniqueIndex(t, ctx, client)

	user := newFirstOrderBonusFulfillmentUser(t, ctx, client, "first-order-bonus-disabled@example.com")
	order := newFirstOrderBonusFulfillmentOrder(t, ctx, client, user.ID, user.Email, user.Username, payment.OrderTypeSubscription, 50)

	affiliateRepo := newFirstOrderBonusAffiliateRepoStub(user.ID, 9004)
	affiliateRepo.firstOrderApplyResult = true
	svc, _ := newFirstOrderBonusFulfillmentService(client, affiliateRepo, firstOrderBonusDisabledSettingValue)

	require.NoError(t, svc.ExecuteSubscriptionFulfillment(ctx, order.ID))

	// 功能默认关闭，绝不能每笔订单都白占一行审计（唯一索引让它删不掉）。
	for _, action := range paymentAuditActionsForOrder(t, ctx, client, order.ID) {
		require.False(t, strings.Contains(action, "FIRST_ORDER_BONUS"), "unexpected audit action %s", action)
	}
	require.Empty(t, affiliateRepo.firstOrderApplyCalls)
	require.Empty(t, affiliateRepo.firstOrderLockCalls)

	// 常规返利不受影响。
	require.Len(t, affiliateRepo.accrueCalls, 1)
	require.Equal(t, 1, countPaymentAuditAction(t, ctx, client, order.ID, "AFFILIATE_REBATE_APPLIED"))
}

func TestExecuteSubscriptionFulfillmentFailsOpenWhenFirstOrderBonusErrors(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	ensurePaymentAuditOrderActionUniqueIndex(t, ctx, client)

	user := newFirstOrderBonusFulfillmentUser(t, ctx, client, "first-order-bonus-failopen@example.com")
	order := newFirstOrderBonusFulfillmentOrder(t, ctx, client, user.ID, user.Email, user.Username, payment.OrderTypeSubscription, 50)

	affiliateRepo := newFirstOrderBonusAffiliateRepoStub(user.ID, 9005)
	affiliateRepo.firstOrderApplyErr = errors.New("first order bonus table is on fire")
	svc, _ := newFirstOrderBonusFulfillmentService(client, affiliateRepo, firstOrderBonusEnabledSettingValue)

	require.NoError(t, svc.ExecuteSubscriptionFulfillment(ctx, order.ID))

	// 首充券是赠品：它炸了也不能把一笔已交付的订单打成 FAILED。
	reloaded, err := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, OrderStatusCompleted, reloaded.Status)

	require.Equal(t, 1, countPaymentAuditAction(t, ctx, client, order.ID, "AFFILIATE_FIRST_ORDER_BONUS_FAILED"))
	// 抢占行随事务一起回滚，不留半成品。
	require.Zero(t, countPaymentAuditAction(t, ctx, client, order.ID, "AFFILIATE_FIRST_ORDER_BONUS_APPLIED"))
	require.Zero(t, countPaymentAuditAction(t, ctx, client, order.ID, "AFFILIATE_FIRST_ORDER_BONUS_SKIPPED"))
	require.Zero(t, countPaymentAuditAction(t, ctx, client, order.ID, "FULFILLMENT_FAILED"))
	// 常规返利已经提交，不受牵连。
	require.Equal(t, 1, countPaymentAuditAction(t, ctx, client, order.ID, "AFFILIATE_REBATE_APPLIED"))
}
