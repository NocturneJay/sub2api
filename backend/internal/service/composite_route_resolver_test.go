package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

type compositeRouteRepoStub struct {
	routes []CompositeModelRoute
}

func (s compositeRouteRepoStub) ListByGroup(ctx context.Context, groupID int64, includeDisabled bool) ([]CompositeModelRoute, error) {
	routes := make([]CompositeModelRoute, 0, len(s.routes))
	for _, route := range s.routes {
		if route.GroupID != groupID {
			continue
		}
		if !includeDisabled && !route.Enabled {
			continue
		}
		routes = append(routes, route)
	}
	return routes, nil
}

func (s compositeRouteRepoStub) Create(ctx context.Context, route *CompositeModelRoute) error {
	return nil
}

func (s compositeRouteRepoStub) Update(ctx context.Context, route *CompositeModelRoute) error {
	return nil
}

func (s compositeRouteRepoStub) Delete(ctx context.Context, id int64) error {
	return nil
}

func (s compositeRouteRepoStub) DeleteByGroup(ctx context.Context, groupID int64) error {
	return nil
}

func TestCompositeRouteResolverExplicitExactRouteRewritesModel(t *testing.T) {
	resolver := NewCompositeRouteResolver(compositeRouteRepoStub{
		routes: []CompositeModelRoute{
			{
				ID:             10,
				GroupID:        7,
				PublicModel:    "openrouter/gpt-5",
				MatchType:      CompositeRouteMatchExact,
				TargetPlatform: PlatformOpenAI,
				TargetGroupID:  i64p(42),
				UpstreamModel:  "gpt-5",
				Endpoint:       CompositeRouteEndpointAny,
				Priority:       100,
				Enabled:        true,
			},
		},
	})

	decision, err := resolver.Resolve(context.Background(), 7, "openrouter/gpt-5", CompositeRouteEndpointChatCompletions)

	require.NoError(t, err)
	require.True(t, decision.Matched)
	require.Equal(t, CompositeRouteSourceExplicit, decision.Source)
	require.Equal(t, PlatformOpenAI, decision.TargetPlatform)
	require.Equal(t, "gpt-5", decision.UpstreamModel)
	require.NotNil(t, decision.Route)
	require.Equal(t, int64(10), decision.Route.ID)
}

// Scenario: 唯一平台的精确别名可路由
func TestCompositeRouteResolverUsesAccountModelOwnershipForUnprefixedAlias(t *testing.T) {
	resolver := NewCompositeRouteResolver(nil)
	resolver.SetModelOwnershipResolver(func(_ context.Context, groupID int64, model string) (CompositeModelOwnership, error) {
		require.Equal(t, int64(7), groupID)
		require.Equal(t, "reasoning-alias", model)
		return CompositeModelOwnership{TargetPlatform: PlatformDeepseek, Matched: true}, nil
	})

	decision, err := resolver.Resolve(context.Background(), 7, "reasoning-alias", CompositeRouteEndpointChatCompletions)

	require.NoError(t, err)
	require.True(t, decision.Matched)
	require.Equal(t, CompositeRouteSourceAccount, decision.Source)
	require.Equal(t, PlatformDeepseek, decision.TargetPlatform)
	require.Equal(t, "reasoning-alias", decision.UpstreamModel)
}

func TestCompositeRouteResolverAccountOwnershipOverridesBuiltInDetector(t *testing.T) {
	resolver := NewCompositeRouteResolver(nil)
	resolver.SetModelOwnershipResolver(func(context.Context, int64, string) (CompositeModelOwnership, error) {
		return CompositeModelOwnership{TargetPlatform: PlatformDeepseek, Matched: true}, nil
	})

	decision, err := resolver.Resolve(context.Background(), 7, "gpt-5", CompositeRouteEndpointResponses)

	require.NoError(t, err)
	require.True(t, decision.Matched)
	require.Equal(t, CompositeRouteSourceAccount, decision.Source)
	require.Equal(t, PlatformDeepseek, decision.TargetPlatform)
}

// Scenario: 显式路由保持最高优先级
func TestCompositeRouteResolverExplicitRouteBeatsAccountOwnership(t *testing.T) {
	resolver := NewCompositeRouteResolver(compositeRouteRepoStub{
		routes: []CompositeModelRoute{{
			ID:             10,
			GroupID:        7,
			PublicModel:    "reasoning-alias",
			MatchType:      CompositeRouteMatchExact,
			TargetPlatform: PlatformOpenAI,
			UpstreamModel:  "gpt-5",
			Endpoint:       CompositeRouteEndpointAny,
			Enabled:        true,
		}},
	})
	resolver.SetModelOwnershipResolver(func(context.Context, int64, string) (CompositeModelOwnership, error) {
		return CompositeModelOwnership{TargetPlatform: PlatformDeepseek, Matched: true}, nil
	})

	decision, err := resolver.Resolve(context.Background(), 7, "reasoning-alias", CompositeRouteEndpointChatCompletions)

	require.NoError(t, err)
	require.True(t, decision.Matched)
	require.Equal(t, CompositeRouteSourceExplicit, decision.Source)
	require.Equal(t, PlatformOpenAI, decision.TargetPlatform)
	require.Equal(t, "gpt-5", decision.UpstreamModel)
}

// Scenario: 跨平台同名别名不被猜测
func TestCompositeRouteResolverDoesNotGuessAmbiguousAccountOwnership(t *testing.T) {
	resolver := NewCompositeRouteResolver(nil)
	resolver.SetModelOwnershipResolver(func(context.Context, int64, string) (CompositeModelOwnership, error) {
		return CompositeModelOwnership{Ambiguous: true}, nil
	})

	decision, err := resolver.Resolve(context.Background(), 7, "shared-alias", CompositeRouteEndpointChatCompletions)

	require.NoError(t, err)
	require.False(t, decision.Matched)
	require.Empty(t, decision.TargetPlatform)
	require.Equal(t, "model is exposed by multiple provider platforms", decision.Reason)
}

func TestCompositeRouteResolverOwnershipLookupErrorFallsBackOnlyForDetectableModels(t *testing.T) {
	lookupErr := errors.New("account catalog unavailable")
	resolver := NewCompositeRouteResolver(nil)
	resolver.SetModelOwnershipResolver(func(context.Context, int64, string) (CompositeModelOwnership, error) {
		return CompositeModelOwnership{}, lookupErr
	})

	detected, err := resolver.Resolve(context.Background(), 7, "gpt-5", CompositeRouteEndpointResponses)
	require.NoError(t, err)
	require.True(t, detected.Matched)
	require.Equal(t, CompositeRouteSourceDetector, detected.Source)
	require.Equal(t, PlatformOpenAI, detected.TargetPlatform)

	unknown, err := resolver.Resolve(context.Background(), 7, "company-model", CompositeRouteEndpointResponses)
	require.ErrorIs(t, err, lookupErr)
	require.False(t, unknown.Matched)
}

func TestCompositeRouteResolverPrefersEndpointSpecificLongestPrefix(t *testing.T) {
	resolver := NewCompositeRouteResolver(compositeRouteRepoStub{
		routes: []CompositeModelRoute{
			{
				ID:             1,
				GroupID:        7,
				PublicModel:    "router/",
				MatchType:      CompositeRouteMatchPrefix,
				TargetPlatform: PlatformAnthropic,
				TargetGroupID:  i64p(41),
				Endpoint:       CompositeRouteEndpointAny,
				Priority:       10,
				Enabled:        true,
			},
			{
				ID:             2,
				GroupID:        7,
				PublicModel:    "router/gpt-",
				MatchType:      CompositeRouteMatchPrefix,
				TargetPlatform: PlatformOpenAI,
				TargetGroupID:  i64p(42),
				UpstreamModel:  "gpt-family",
				Endpoint:       CompositeRouteEndpointResponses,
				Priority:       100,
				Enabled:        true,
			},
		},
	})

	decision, err := resolver.Resolve(context.Background(), 7, "router/gpt-5", CompositeRouteEndpointResponses)

	require.NoError(t, err)
	require.True(t, decision.Matched)
	require.Equal(t, CompositeRouteSourceExplicit, decision.Source)
	require.Equal(t, PlatformOpenAI, decision.TargetPlatform)
	require.Equal(t, "gpt-family", decision.UpstreamModel)
	require.NotNil(t, decision.Route)
	require.Equal(t, int64(2), decision.Route.ID)
}

func TestCompositeRouteResolverPrefixWithoutUpstreamPreservesRequestedModel(t *testing.T) {
	targetGroupID := int64(42)
	resolver := NewCompositeRouteResolver(compositeRouteRepoStub{
		routes: []CompositeModelRoute{
			{
				ID:             1,
				GroupID:        7,
				PublicModel:    "gpt",
				MatchType:      CompositeRouteMatchPrefix,
				TargetPlatform: PlatformOpenAI,
				TargetGroupID:  &targetGroupID,
				Endpoint:       CompositeRouteEndpointAny,
				Priority:       100,
				Enabled:        true,
			},
		},
	})

	decision, err := resolver.Resolve(context.Background(), 7, "gpt-5.5-codex", CompositeRouteEndpointResponses)

	require.NoError(t, err)
	require.True(t, decision.Matched)
	require.Equal(t, "gpt-5.5-codex", decision.UpstreamModel)
}

func TestCompositeRouteResolverRejectsDisabledOrUnroutedModel(t *testing.T) {
	resolver := NewCompositeRouteResolver(compositeRouteRepoStub{
		routes: []CompositeModelRoute{
			{
				ID:             1,
				GroupID:        7,
				PublicModel:    "gpt-5",
				MatchType:      CompositeRouteMatchExact,
				TargetPlatform: PlatformAnthropic,
				TargetGroupID:  i64p(41),
				UpstreamModel:  "claude-sonnet-4-6",
				Endpoint:       CompositeRouteEndpointAny,
				Priority:       100,
				Enabled:        false,
			},
		},
	})

	decision, err := resolver.Resolve(context.Background(), 7, "gpt-5", CompositeRouteEndpointAny)

	require.NoError(t, err)
	require.False(t, decision.Matched)
	require.Empty(t, decision.Source)
	require.Contains(t, decision.Reason, "explicit target-group route")
	require.Nil(t, decision.Route)
}

// 上游 v0.1.182 的同名用例断言 Kimi Code 裸模型名（k3 等）被 DetectModelPlatform
// 兜底解析（require.True(Matched) + Source==CompositeRouteSourceDetector）。
// aicat 刻意删除了猜名兜底：复合路由必须委托到具体 target_group_id，
// 未配置路由的模型一律 fail closed（见规约设计冲突第一条）。按 aicat 口径反写。
func TestCompositeRouteResolverKimiCodeBareModelsFailClosed(t *testing.T) {
	resolver := NewCompositeRouteResolver(nil)

	for _, model := range []string{"k3", "k3-256k", "kimi-code/k3"} {
		t.Run(model, func(t *testing.T) {
			decision, err := resolver.Resolve(context.Background(), 7, model, CompositeRouteEndpointMessages)

			require.NoError(t, err)
			require.False(t, decision.Matched)
			require.Empty(t, decision.Source)
		})
	}
}

func TestCompositeRouteResolverExplicitRoutesCoverBucketTwoProviders(t *testing.T) {
	resolver := NewCompositeRouteResolver(compositeRouteRepoStub{
		routes: []CompositeModelRoute{
			{
				ID:             1,
				GroupID:        7,
				PublicModel:    "all/gpt-5",
				MatchType:      CompositeRouteMatchExact,
				TargetPlatform: PlatformOpenAI,
				TargetGroupID:  i64p(41),
				UpstreamModel:  "gpt-5",
				Endpoint:       CompositeRouteEndpointResponses,
				Priority:       100,
				Enabled:        true,
			},
			{
				ID:             2,
				GroupID:        7,
				PublicModel:    "all/claude-sonnet",
				MatchType:      CompositeRouteMatchExact,
				TargetPlatform: PlatformAnthropic,
				TargetGroupID:  i64p(42),
				UpstreamModel:  "claude-sonnet-4-6",
				Endpoint:       CompositeRouteEndpointMessages,
				Priority:       100,
				Enabled:        true,
			},
			{
				ID:             3,
				GroupID:        7,
				PublicModel:    "all/gemini-pro",
				MatchType:      CompositeRouteMatchExact,
				TargetPlatform: PlatformGemini,
				TargetGroupID:  i64p(43),
				UpstreamModel:  "gemini-2.5-pro",
				Endpoint:       CompositeRouteEndpointGemini,
				Priority:       100,
				Enabled:        true,
			},
			{
				ID:             4,
				GroupID:        7,
				PublicModel:    "all/grok",
				MatchType:      CompositeRouteMatchExact,
				TargetPlatform: PlatformGrok,
				TargetGroupID:  i64p(44),
				UpstreamModel:  "grok-4.3",
				Endpoint:       CompositeRouteEndpointResponses,
				Priority:       100,
				Enabled:        true,
			},
		},
	})

	tests := []struct {
		model        string
		endpoint     string
		wantPlatform string
		wantUpstream string
	}{
		{"all/gpt-5", CompositeRouteEndpointResponses, PlatformOpenAI, "gpt-5"},
		{"all/claude-sonnet", CompositeRouteEndpointMessages, PlatformAnthropic, "claude-sonnet-4-6"},
		{"all/gemini-pro", CompositeRouteEndpointGemini, PlatformGemini, "gemini-2.5-pro"},
		{"all/grok", CompositeRouteEndpointResponses, PlatformGrok, "grok-4.3"},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			decision, err := resolver.Resolve(context.Background(), 7, tt.model, tt.endpoint)

			require.NoError(t, err)
			require.True(t, decision.Matched)
			require.Equal(t, CompositeRouteSourceExplicit, decision.Source)
			require.Equal(t, tt.wantPlatform, decision.TargetPlatform)
			require.Equal(t, tt.wantUpstream, decision.UpstreamModel)
		})
	}
}

// TestCompositeRouteResolverPrefixExplicitUpstreamStillFixed 移植自上游 v0.1.169
// 新增的覆盖，并按 aicat「路由必须委托到具体目标分组」的语义补上 TargetGroupID。
// 锁住的契约是：prefix 路由一旦写死 upstream_model，所有命中该前缀的模型都被改写成
// 同一个上游模型；与留空 upstream_model 时按请求模型透传互补（见
// TestCompositeRouteResolverPrefixWithoutUpstreamPreservesRequestedModel）。
//
// 注意上游同名文件里另一条 IgnoresDisabledRoutesAndFallsBackToDetector 没有移植：
// 它断言「路由被禁用时回落到 detector 猜平台」，而 aicat 自 455ac9c58
// "require target-group routes for pricing" 起刻意改成 fail-closed 直接拒绝——
// 猜测会把请求按错误的分组调度和计费。该行为由
// TestCompositeRouteResolverRejectsDisabledOrUnroutedModel 覆盖。
func TestCompositeRouteResolverPrefixExplicitUpstreamStillFixed(t *testing.T) {
	resolver := NewCompositeRouteResolver(compositeRouteRepoStub{
		routes: []CompositeModelRoute{
			{
				ID:             1,
				GroupID:        7,
				PublicModel:    "deepseek-v4",
				MatchType:      CompositeRouteMatchPrefix,
				TargetPlatform: PlatformOpenAI,
				TargetGroupID:  i64p(42),
				UpstreamModel:  "deepseek-chat",
				Endpoint:       CompositeRouteEndpointAny,
				Priority:       100,
				Enabled:        true,
			},
		},
	})

	for _, model := range []string{"deepseek-v4-flash", "deepseek-v4-pro"} {
		decision, err := resolver.Resolve(context.Background(), 7, model, CompositeRouteEndpointChatCompletions)
		require.NoError(t, err)
		require.True(t, decision.Matched)
		require.Equal(t, "deepseek-chat", decision.UpstreamModel)
	}
}

// TestCompositeRouteResolverRejectsRouteWithoutTargetGroup 锁定委托路由的硬前提：
// 一条路由即使 Enabled，只要没绑定 target_group_id 就不参与匹配。
//
// 这条不变量此前没有任何用例直接覆盖，而它正是同步上游时最容易被破坏的一处：
// 上游没有 target_group_id 这一层，其测试的路由 stub 一律不填该字段。v0.1.169
// 同步时本文件被误取了上游那一侧，9 个用例因此集体失配——有这条护栏就能立刻
// 定位到「路由没绑目标分组」而不是去怀疑解析器本身。
func TestCompositeRouteResolverRejectsRouteWithoutTargetGroup(t *testing.T) {
	resolver := NewCompositeRouteResolver(compositeRouteRepoStub{
		routes: []CompositeModelRoute{
			{
				ID:             1,
				GroupID:        7,
				PublicModel:    "gpt-5",
				MatchType:      CompositeRouteMatchExact,
				TargetPlatform: PlatformOpenAI,
				UpstreamModel:  "gpt-5",
				Endpoint:       CompositeRouteEndpointAny,
				Priority:       100,
				Enabled:        true,
			},
		},
	})

	decision, err := resolver.Resolve(context.Background(), 7, "gpt-5", CompositeRouteEndpointAny)

	require.NoError(t, err)
	require.False(t, decision.Matched)
	require.Contains(t, decision.Reason, "explicit target-group route")
	require.Nil(t, decision.Route)
}
