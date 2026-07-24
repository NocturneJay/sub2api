//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// 委托到子分组：路由解析出的 decision 应携带 TargetGroupID 与倍率覆盖。
func TestCompositeRouteResolverCarriesTargetGroup(t *testing.T) {
	gid := int64(42)
	mult := 1.5
	resolver := NewCompositeRouteResolver(compositeRouteRepoStub{
		routes: []CompositeModelRoute{{
			ID: 1, GroupID: 7, PublicModel: "gpt", MatchType: CompositeRouteMatchPrefix,
			TargetPlatform: PlatformOpenAI, TargetGroupID: &gid, RateMultiplier: &mult,
			Endpoint: CompositeRouteEndpointAny, Priority: 100, Enabled: true,
		}},
	})

	decision, err := resolver.Resolve(context.Background(), 7, "gpt-4o", CompositeRouteEndpointChatCompletions)

	require.NoError(t, err)
	require.True(t, decision.Matched)
	require.NotNil(t, decision.TargetGroupID)
	require.Equal(t, int64(42), *decision.TargetGroupID)
	require.NotNil(t, decision.RateMultiplier)
	require.Equal(t, 1.5, *decision.RateMultiplier)
	require.Equal(t, "gpt-4o", decision.UpstreamModel) // 空 upstream => 用公开模型名
}

// WithCompositeRouteDecision 应把定价分组与倍率覆盖写入 context，供计费与重试链路复用。
func TestResolvedPricingGroupContextRoundTrip(t *testing.T) {
	gid := int64(42)
	mult := 2.0
	ctx := WithCompositeRouteDecision(context.Background(), CompositeRouteDecision{
		Matched: true, TargetPlatform: PlatformOpenAI, UpstreamModel: "gpt-5",
		TargetGroupID: &gid, RateMultiplier: &mult,
	})

	id, ok := ResolvedPricingGroupIDFromContext(ctx)
	require.True(t, ok)
	require.Equal(t, int64(42), id)

	m, ok := ResolvedRateMultiplierFromContext(ctx)
	require.True(t, ok)
	require.Equal(t, 2.0, m)
}

// 无委托时不写入定价分组。
func TestResolvedPricingGroupAbsentForPlatformRoute(t *testing.T) {
	ctx := WithCompositeRouteDecision(context.Background(), CompositeRouteDecision{
		Matched: true, TargetPlatform: PlatformOpenAI, UpstreamModel: "gpt-5",
	})
	_, ok := ResolvedPricingGroupIDFromContext(ctx)
	require.False(t, ok)
}

// billingPricingGroupID：委托时用子分组，否则用 apiKey.Group。
func TestBillingPricingGroupIDPrefersDelegatedGroup(t *testing.T) {
	apiKey := &APIKey{Group: &Group{ID: 7, Platform: PlatformComposite}}

	require.Equal(t, int64(7), billingPricingGroupID(context.Background(), apiKey))

	ctx := WithResolvedPricingGroupID(context.Background(), 42)
	require.Equal(t, int64(42), billingPricingGroupID(ctx, apiKey))
}

func TestCompositeDelegatedPricingAPIKeyUsesTargetGroupWithoutMutatingAccountingKey(t *testing.T) {
	groupRepo := &groupRepoStubForAdmin{
		getByIDByID: map[int64]*Group{
			42: {ID: 42, Platform: PlatformOpenAI, RateMultiplier: 3.0, Status: StatusActive},
		},
	}
	accountingGroupID := int64(7)
	accountingKey := &APIKey{
		GroupID: &accountingGroupID,
		Group:   &Group{ID: accountingGroupID, Platform: PlatformComposite, RateMultiplier: 1.0},
	}
	targetGroupID := int64(42)
	ctx := WithCompositeRouteDecision(context.Background(), CompositeRouteDecision{
		Matched: true, TargetPlatform: PlatformOpenAI, TargetGroupID: &targetGroupID,
	})

	pricingKey, delegated, err := compositeDelegatedPricingAPIKey(ctx, accountingKey, groupRepo)

	require.NoError(t, err)
	require.True(t, delegated)
	require.NotSame(t, accountingKey, pricingKey)
	require.Equal(t, targetGroupID, *pricingKey.GroupID)
	require.Equal(t, 3.0, pricingKey.Group.RateMultiplier)
	require.Equal(t, accountingGroupID, *accountingKey.GroupID)
	require.Equal(t, PlatformComposite, accountingKey.Group.Platform)
}

func TestOpenAICompositeRequestGroupUsesDelegatedTargetPolicies(t *testing.T) {
	targetGroupID := int64(42)
	target := &Group{
		ID: targetGroupID, Platform: PlatformOpenAI, Status: StatusActive,
		AllowMessagesDispatch: true, MaxReasoningEffort: "medium",
	}
	svc := &OpenAIGatewayService{groupRepo: &groupRepoStubForAdmin{getByIDByID: map[int64]*Group{
		targetGroupID: target,
	}}}
	compositeGroupID := int64(7)
	apiKey := &APIKey{
		GroupID: &compositeGroupID,
		Group:   &Group{ID: compositeGroupID, Platform: PlatformComposite, Status: StatusActive},
	}
	ctx := WithCompositeRouteDecision(context.Background(), CompositeRouteDecision{
		Matched: true, TargetPlatform: PlatformOpenAI, TargetGroupID: &targetGroupID,
	})

	got, err := svc.ResolveCompositeRequestGroup(ctx, apiKey)

	require.NoError(t, err)
	require.Same(t, target, got)
	require.True(t, got.AllowMessagesDispatch)
	require.Equal(t, "medium", got.MaxReasoningEffort)

	got, err = svc.ResolveCompositeRequestGroup(context.Background(), apiKey)
	require.NoError(t, err)
	require.Same(t, apiKey.Group, got)
}

func TestResolveCompositeDelegatedGroupRejectsInactiveOrChangedPlatform(t *testing.T) {
	targetGroupID := int64(42)
	ctx := WithCompositeRouteDecision(context.Background(), CompositeRouteDecision{
		Matched: true, TargetPlatform: PlatformOpenAI, TargetGroupID: &targetGroupID,
	})

	inactiveRepo := &groupRepoStubForAdmin{getByIDByID: map[int64]*Group{
		42: {ID: 42, Platform: PlatformOpenAI, Status: StatusDisabled},
	}}
	_, _, err := resolveCompositeDelegatedGroup(ctx, inactiveRepo)
	require.ErrorIs(t, err, ErrNoAvailableAccounts)

	changedRepo := &groupRepoStubForAdmin{getByIDByID: map[int64]*Group{
		42: {ID: 42, Platform: PlatformGrok, Status: StatusActive},
	}}
	_, _, err = resolveCompositeDelegatedGroup(ctx, changedRepo)
	require.ErrorIs(t, err, ErrNoAvailableAccounts)
}

func TestOpenAISchedulerUsesCompositeDelegatedGroupPool(t *testing.T) {
	compositeGroupID := int64(7)
	targetGroupID := int64(42)
	ctx := WithCompositeRouteDecision(context.Background(), CompositeRouteDecision{
		Matched: true, TargetPlatform: PlatformOpenAI, TargetGroupID: &targetGroupID,
	})
	groupRepo := &groupRepoStubForAdmin{getByIDByID: map[int64]*Group{
		42: {ID: 42, Platform: PlatformOpenAI, Status: StatusActive},
	}}
	accounts := []Account{
		{ID: 701, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, GroupIDs: []int64{compositeGroupID}},
		{ID: 4201, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, GroupIDs: []int64{targetGroupID}},
	}
	cfg := &config.Config{}
	cfg.Gateway.Scheduling.LoadBatchEnabled = false
	svc := &OpenAIGatewayService{
		accountRepo: schedulerGroupAwareOpenAIAccountRepo{schedulerTestOpenAIAccountRepo{accounts: accounts}},
		groupRepo:   groupRepo,
		cfg:         cfg,
	}

	selection, _, err := svc.SelectAccountWithScheduler(
		ctx, &compositeGroupID, "", "", "gpt-5", nil, OpenAIUpstreamTransportAny, false,
	)

	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Account)
	require.Equal(t, int64(4201), selection.Account.ID)
}

func TestGrokSchedulerUsesCompositeDelegatedGroupPool(t *testing.T) {
	compositeGroupID := int64(7)
	targetGroupID := int64(43)
	ctx := WithCompositeRouteDecision(context.Background(), CompositeRouteDecision{
		Matched: true, TargetPlatform: PlatformGrok, TargetGroupID: &targetGroupID,
	})
	groupRepo := &groupRepoStubForAdmin{getByIDByID: map[int64]*Group{
		43: {ID: 43, Platform: PlatformGrok, Status: StatusActive},
	}}
	accounts := []Account{
		{ID: 701, Platform: PlatformGrok, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, GroupIDs: []int64{compositeGroupID}},
		{ID: 4301, Platform: PlatformGrok, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, GroupIDs: []int64{targetGroupID}},
	}
	cfg := &config.Config{}
	cfg.Gateway.Scheduling.LoadBatchEnabled = false
	svc := &OpenAIGatewayService{
		accountRepo: schedulerGroupAwareOpenAIAccountRepo{schedulerTestOpenAIAccountRepo{accounts: accounts}},
		groupRepo:   groupRepo,
		cache:       &schedulerTestGatewayCache{},
		cfg:         cfg,
	}

	selection, _, err := svc.SelectAccountWithSchedulerForCapability(
		ctx, &compositeGroupID, "", "", "grok-4.3", nil,
		OpenAIUpstreamTransportAny, OpenAIEndpointCapabilityChatCompletions,
		false, false, false, PlatformGrok,
	)

	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Account)
	require.Equal(t, int64(4301), selection.Account.ID)
}

func TestGatewaySchedulerUsesCompositeDelegatedGroupPool(t *testing.T) {
	compositeGroupID := int64(7)
	targetGroupID := int64(42)
	ctx := WithCompositeRouteDecision(context.Background(), CompositeRouteDecision{
		Matched: true, PublicModel: "gpt-public", TargetPlatform: PlatformOpenAI,
		TargetGroupID: &targetGroupID, UpstreamModel: "gpt-5",
	})
	groupRepo := &groupRepoStubForAdmin{getByIDByID: map[int64]*Group{
		7:  {ID: 7, Platform: PlatformComposite, Status: StatusActive},
		42: {ID: 42, Platform: PlatformOpenAI, Status: StatusActive},
	}}
	accounts := []Account{
		{ID: 701, Platform: PlatformOpenAI, Status: StatusActive, Schedulable: true, Concurrency: 1, AccountGroups: []AccountGroup{{GroupID: compositeGroupID}}},
		{ID: 4201, Platform: PlatformOpenAI, Status: StatusActive, Schedulable: true, Concurrency: 1, AccountGroups: []AccountGroup{{GroupID: targetGroupID}}},
	}
	svc := &GatewayService{
		accountRepo: newGroupAwareMockRepo(accounts),
		groupRepo:   groupRepo,
		cache:       &mockGatewayCacheForPlatform{},
		cfg:         testConfig(),
	}

	account, err := svc.SelectAccountForModelWithExclusions(ctx, &compositeGroupID, "", "gpt-public", nil)

	require.NoError(t, err)
	require.NotNil(t, account)
	require.Equal(t, int64(4201), account.ID)
}

func TestOpenAIRecordUsagePricesByDelegatedGroupButLogsCompositeGroup(t *testing.T) {
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	billingRepo := &openAIRecordUsageBillingRepoStub{result: &UsageBillingApplyResult{Applied: true}}
	svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(
		usageRepo,
		billingRepo,
		&openAIRecordUsageUserRepoStub{},
		&openAIRecordUsageSubRepoStub{},
		nil,
	)
	svc.groupRepo = &groupRepoStubForAdmin{getByIDByID: map[int64]*Group{
		42: {ID: 42, Platform: PlatformOpenAI, Status: StatusActive, RateMultiplier: 2.0},
	}}
	compositeGroupID := int64(7)
	targetGroupID := int64(42)
	ctx := WithCompositeRouteDecision(context.Background(), CompositeRouteDecision{
		Matched: true, TargetPlatform: PlatformOpenAI, TargetGroupID: &targetGroupID,
	})
	usage := OpenAIUsage{InputTokens: 1000, OutputTokens: 100}

	err := svc.RecordUsage(ctx, &OpenAIRecordUsageInput{
		Result: &OpenAIForwardResult{
			RequestID: "resp_composite_delegated",
			Model:     "gpt-5.1",
			Usage:     usage,
		},
		APIKey: &APIKey{
			ID:      10,
			GroupID: &compositeGroupID,
			Group:   &Group{ID: compositeGroupID, Platform: PlatformComposite, Status: StatusActive, RateMultiplier: 0.5},
		},
		User:    &User{ID: 20},
		Account: &Account{ID: 30, Platform: PlatformOpenAI, Type: AccountTypeAPIKey},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.lastLog)
	require.NotNil(t, usageRepo.lastLog.GroupID)
	require.Equal(t, compositeGroupID, *usageRepo.lastLog.GroupID)
	require.Equal(t, 2.0, usageRepo.lastLog.RateMultiplier)
	expected := expectedOpenAICost(t, svc, "gpt-5.1", usage, 2.0)
	require.InDelta(t, expected.TotalCost, usageRepo.lastLog.TotalCost, 1e-12)
}

func TestOpenAIRecordUsagePrefersDelegatedRouteMultiplier(t *testing.T) {
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	billingRepo := &openAIRecordUsageBillingRepoStub{result: &UsageBillingApplyResult{Applied: true}}
	svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(
		usageRepo,
		billingRepo,
		&openAIRecordUsageUserRepoStub{},
		&openAIRecordUsageSubRepoStub{},
		nil,
	)
	svc.groupRepo = &groupRepoStubForAdmin{getByIDByID: map[int64]*Group{
		42: {ID: 42, Platform: PlatformOpenAI, Status: StatusActive, RateMultiplier: 2.0},
	}}
	compositeGroupID := int64(7)
	targetGroupID := int64(42)
	routeMultiplier := 3.5
	ctx := WithCompositeRouteDecision(context.Background(), CompositeRouteDecision{
		Matched: true, TargetPlatform: PlatformOpenAI, TargetGroupID: &targetGroupID,
		RateMultiplier: &routeMultiplier,
	})
	usage := OpenAIUsage{InputTokens: 1000, OutputTokens: 100}

	err := svc.RecordUsage(ctx, &OpenAIRecordUsageInput{
		Result: &OpenAIForwardResult{
			RequestID: "resp_composite_delegated_route_multiplier",
			Model:     "gpt-5.1",
			Usage:     usage,
		},
		APIKey: &APIKey{
			ID:      10,
			GroupID: &compositeGroupID,
			Group:   &Group{ID: compositeGroupID, Platform: PlatformComposite, Status: StatusActive, RateMultiplier: 0.5},
		},
		User:    &User{ID: 20},
		Account: &Account{ID: 30, Platform: PlatformOpenAI, Type: AccountTypeAPIKey},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.lastLog)
	require.Equal(t, routeMultiplier, usageRepo.lastLog.RateMultiplier)
	expected := expectedOpenAICost(t, svc, "gpt-5.1", usage, routeMultiplier)
	require.InDelta(t, expected.TotalCost, usageRepo.lastLog.TotalCost, 1e-12)
}

func TestGatewayRecordUsagePricesByDelegatedGroupButLogsCompositeGroup(t *testing.T) {
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	billingRepo := &openAIRecordUsageBillingRepoStub{result: &UsageBillingApplyResult{Applied: true}}
	svc := newGatewayRecordUsageServiceWithBillingRepoForTest(
		usageRepo,
		billingRepo,
		&openAIRecordUsageUserRepoStub{},
		&openAIRecordUsageSubRepoStub{},
	)
	svc.groupRepo = &groupRepoStubForAdmin{getByIDByID: map[int64]*Group{
		42: {ID: 42, Platform: PlatformAnthropic, Status: StatusActive, RateMultiplier: 2.0},
	}}
	compositeGroupID := int64(7)
	targetGroupID := int64(42)
	ctx := WithCompositeRouteDecision(context.Background(), CompositeRouteDecision{
		Matched: true, TargetPlatform: PlatformAnthropic, TargetGroupID: &targetGroupID,
	})
	usage := ClaudeUsage{InputTokens: 1000, OutputTokens: 100}

	err := svc.RecordUsage(ctx, &RecordUsageInput{
		Result: &ForwardResult{
			RequestID: "msg_composite_delegated",
			Model:     "claude-sonnet-4",
			Usage:     usage,
		},
		APIKey: &APIKey{
			ID:      10,
			GroupID: &compositeGroupID,
			Group:   &Group{ID: compositeGroupID, Platform: PlatformComposite, Status: StatusActive, RateMultiplier: 0.5},
		},
		User:    &User{ID: 20},
		Account: &Account{ID: 30, Platform: PlatformAnthropic},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.lastLog)
	require.NotNil(t, usageRepo.lastLog.GroupID)
	require.Equal(t, compositeGroupID, *usageRepo.lastLog.GroupID)
	require.Equal(t, 2.0, usageRepo.lastLog.RateMultiplier)
	expected, err := svc.billingService.CalculateCost("claude-sonnet-4", UsageTokens{
		InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens,
	}, 2.0)
	require.NoError(t, err)
	require.InDelta(t, expected.TotalCost, usageRepo.lastLog.TotalCost, 1e-12)
}

func TestCompositeDelegationUsesTargetGroupChannelMapping(t *testing.T) {
	repo := &mockChannelRepository{
		listAllFn: func(context.Context) ([]Channel, error) {
			return []Channel{
				{ID: 1, Status: StatusActive, GroupIDs: []int64{7}, ModelMapping: map[string]map[string]string{
					PlatformOpenAI: {"gpt-public": "wrong-composite-model"},
				}},
				{ID: 2, Status: StatusActive, GroupIDs: []int64{42}, ModelMapping: map[string]map[string]string{
					PlatformOpenAI: {"gpt-public": "target-group-model"},
				}},
			}, nil
		},
		getGroupPlatformsFn: func(context.Context, []int64) (map[int64]string, error) {
			return map[int64]string{7: PlatformComposite, 42: PlatformOpenAI}, nil
		},
	}
	channelService := NewChannelService(repo, nil, nil, nil)
	targetGroupID := int64(42)
	ctx := WithCompositeRouteDecision(context.Background(), CompositeRouteDecision{
		Matched: true, TargetPlatform: PlatformOpenAI, TargetGroupID: &targetGroupID,
	})

	gatewayResult, _ := (&GatewayService{channelService: channelService}).ResolveChannelMappingAndRestrict(ctx, i64p(7), "gpt-public")
	openAIResult, _ := (&OpenAIGatewayService{channelService: channelService}).ResolveChannelMappingAndRestrict(ctx, i64p(7), "gpt-public")

	require.Equal(t, "target-group-model", gatewayResult.MappedModel)
	require.Equal(t, int64(2), gatewayResult.ChannelID)
	require.Equal(t, "target-group-model", openAIResult.MappedModel)
	require.Equal(t, int64(2), openAIResult.ChannelID)
}

// admin 校验：委托到具体子分组时用子分组平台预填 target_platform。
func TestPrepareCompositeRouteTargetFillsPlatform(t *testing.T) {
	groupRepo := &groupRepoStubForAdmin{
		getByIDByID: map[int64]*Group{
			42: {ID: 42, Platform: PlatformGemini, Status: StatusActive},
		},
	}
	svc := &adminServiceImpl{groupRepo: groupRepo}
	gid := int64(42)

	out, err := svc.prepareCompositeRouteTarget(context.Background(), 7, CompositeRouteInput{
		PublicModel: "gpt", MatchType: CompositeRouteMatchPrefix, TargetGroupID: &gid,
	})

	require.NoError(t, err)
	require.Equal(t, PlatformGemini, out.TargetPlatform)
	require.NotNil(t, out.TargetGroupID)
	require.Equal(t, int64(42), *out.TargetGroupID)
}

// admin 校验：拒绝 composite 目标 / 自身 / 未启用。
func TestPrepareCompositeRouteTargetRejectsInvalid(t *testing.T) {
	groupRepo := &groupRepoStubForAdmin{
		getByIDByID: map[int64]*Group{
			7:  {ID: 7, Platform: PlatformComposite, Status: StatusActive},
			50: {ID: 50, Platform: PlatformComposite, Status: StatusActive},
			60: {ID: 60, Platform: PlatformOpenAI, Status: StatusDisabled},
		},
	}
	svc := &adminServiceImpl{groupRepo: groupRepo}

	self := int64(7)
	_, err := svc.prepareCompositeRouteTarget(context.Background(), 7, CompositeRouteInput{
		PublicModel: "gpt", TargetGroupID: &self,
	})
	require.Error(t, err)

	comp := int64(50)
	_, err = svc.prepareCompositeRouteTarget(context.Background(), 7, CompositeRouteInput{
		PublicModel: "gpt", TargetGroupID: &comp,
	})
	require.Error(t, err)

	inactive := int64(60)
	_, err = svc.prepareCompositeRouteTarget(context.Background(), 7, CompositeRouteInput{
		PublicModel: "gpt", TargetGroupID: &inactive,
	})
	require.Error(t, err)
}
