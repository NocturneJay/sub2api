package handler

import (
	"context"
	"net/http/httptest"
	"testing"

	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type handlerCompositeRouteRepoStub struct {
	routes []service.CompositeModelRoute
}

func (s handlerCompositeRouteRepoStub) ListByGroup(_ context.Context, groupID int64, includeDisabled bool) ([]service.CompositeModelRoute, error) {
	routes := make([]service.CompositeModelRoute, 0, len(s.routes))
	for _, route := range s.routes {
		if route.GroupID == groupID && (includeDisabled || route.Enabled) {
			routes = append(routes, route)
		}
	}
	return routes, nil
}

func (handlerCompositeRouteRepoStub) Create(context.Context, *service.CompositeModelRoute) error {
	return nil
}

func (handlerCompositeRouteRepoStub) Update(context.Context, *service.CompositeModelRoute) error {
	return nil
}

func (handlerCompositeRouteRepoStub) Delete(context.Context, int64) error { return nil }

func (handlerCompositeRouteRepoStub) DeleteByGroup(context.Context, int64) error { return nil }

func TestCompositeTargetPlatformAllowedRejectsUnroutedKnownModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/embeddings", nil)
	apiKey := &service.APIKey{Group: &service.Group{Platform: service.PlatformComposite}}

	require.False(t, compositeTargetPlatformAllowed(c, apiKey, "text-embedding-3-large", service.PlatformOpenAI))
	_, ok := service.ResolvedTargetPlatformFromContext(c.Request.Context())
	require.False(t, ok)
}

func TestOpenAICompatibleTextTargetRejectsUnroutedCompositeProviders(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// 模型表取自上游 v0.1.178（覆盖 grok + CN 三家）+ v0.1.182 新增的 Kimi Code k3；
	// 但断言方向与上游相反：上游那版 TestOpenAICompatibleTextTargetAllowsCompositeProviders
	// 断言这些模型会被放行并「解析出」平台，靠的正是 aicat 刻意删除的 DetectModelPlatform
	// 猜名兜底。aicat 口径：复合分组未配置路由 = fail closed，既不放行也不解析出平台。
	models := []string{"grok-4.3", "kimi-k2-thinking", "k3", "glm-5.2", "deepseek-v3.2"}
	for _, path := range []string{"/v1/messages", "/v1/chat/completions", "/v1/responses", "/v1/responses/input_tokens", "/v1/messages/count_tokens"} {
		for _, model := range models {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", path, nil)
			apiKey := &service.APIKey{Group: &service.Group{Platform: service.PlatformComposite}}

			require.False(t, openAICompatibleTextTargetAllowed(c, apiKey, model), "path=%s model=%s", path, model)
			_, ok := service.ResolvedTargetPlatformFromContext(c.Request.Context())
			require.False(t, ok, "path=%s model=%s", path, model)
		}
	}
}

// WS ingress 对 CN 账号既过不了 transport 过滤、HTTP 桥也没有 Responses 转换，
// 放行只会把明确的策略拒绝换成 "no available account"，因此 WS 白名单保持 openai+grok。
// 本用例与路由哲学无关，原样采纳上游。
func TestResponsesWebSocketCompositePlatformGuardKeepsOpenAIAndGrokOnly(t *testing.T) {
	require.True(t, isResponsesWebSocketCompositePlatform(service.PlatformOpenAI))
	require.True(t, isResponsesWebSocketCompositePlatform(service.PlatformGrok))
	for _, platform := range []string{
		service.PlatformKimi, service.PlatformZhipu, service.PlatformDeepseek,
		service.PlatformAnthropic, service.PlatformGemini,
	} {
		require.False(t, isResponsesWebSocketCompositePlatform(platform), "platform=%s", platform)
	}
}

func TestCompositeTargetPlatformAllowedRejectsWrongOrUnknownModel(t *testing.T) {
	gin.SetMode(gin.TestMode)

	for _, tc := range []struct {
		name  string
		model string
	}{
		{name: "wrong provider", model: "claude-sonnet-4-5"},
		{name: "unknown provider", model: "llama-4-maverick"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/v1/embeddings", nil)
			apiKey := &service.APIKey{Group: &service.Group{Platform: service.PlatformComposite}}

			require.False(t, compositeTargetPlatformAllowed(c, apiKey, tc.model, service.PlatformOpenAI))
		})
	}
}

func TestCompositeTargetPlatformResolvedRejectsUnknownModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
	apiKey := &service.APIKey{Group: &service.Group{Platform: service.PlatformComposite}}

	require.False(t, compositeTargetPlatformResolved(c, apiKey, "llama-4-maverick"))
	_, ok := service.ResolvedTargetPlatformFromContext(c.Request.Context())
	require.False(t, ok)
}

func TestCompositeTargetPlatformResolvedAllowsConcreteGroupWithoutResolution(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
	apiKey := &service.APIKey{Group: &service.Group{Platform: service.PlatformAnthropic}}

	require.True(t, compositeTargetPlatformResolved(c, apiKey, "llama-4-maverick"))
}

// 上游原版断言的是「复合**父**分组上的 MaxReasoningEffort 生效」。aicat 自
// 38a96eaa6 起父分组只是路由壳，推理上限跟委托后的子分组走（理由见
// composite_platform.go 上的注释），故这里改为断言子分组口径：父分组配了值也
// 不生效，子分组配的才生效。
func TestOpenAIReasoningEffortPolicyForCompositeTarget(t *testing.T) {
	gin.SetMode(gin.TestMode)
	parentGroup := &service.Group{
		Platform:           service.PlatformComposite,
		MaxReasoningEffort: "low", // 父分组上的值必须**不**生效
		ReasoningEffortMappings: []service.ReasoningEffortMapping{
			{From: "max", To: "minimal"},
		},
	}
	targetGroup := &service.Group{
		Platform:           service.PlatformOpenAI,
		MaxReasoningEffort: "medium",
		ReasoningEffortMappings: []service.ReasoningEffortMapping{
			{From: "max", To: "xhigh"},
		},
	}
	apiKey := &service.APIKey{Group: parentGroup}
	body := []byte(`{"reasoning":{"effort":"max"}}`)

	openAICtx, _ := gin.CreateTestContext(httptest.NewRecorder())
	openAICtx.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	openAICtx.Request = openAICtx.Request.WithContext(service.WithResolvedTargetPlatform(openAICtx.Request.Context(), service.PlatformOpenAI))
	got, changed, err := applyOpenAIReasoningEffortPolicyForGroup(openAICtx, apiKey, targetGroup, body)
	require.NoError(t, err)
	require.True(t, changed)
	require.JSONEq(t, `{"reasoning":{"effort":"medium"}}`, string(got))
	requested := service.RequestedReasoningEffortFromContext(openAICtx.Request.Context())
	require.NotNil(t, requested)
	require.Equal(t, "max", *requested)

	bindOpenAIReasoningEffortPolicyForMessagesRequest(openAICtx, apiKey, targetGroup, []byte(`{"output_config":{"effort":"max"}}`))
	bound, changed, err := service.ApplyOpenAIReasoningEffortPolicyFromContext(openAICtx.Request.Context(), body)
	require.NoError(t, err)
	require.True(t, changed)
	require.JSONEq(t, `{"reasoning":{"effort":"medium"}}`, string(bound))

	// output_config.effort 缺省时不绑定策略：Messages 桥自己会合成一个默认
	// effort，绑上上限会连那个默认值一起改掉。
	omittedCtx, _ := gin.CreateTestContext(httptest.NewRecorder())
	omittedCtx.Request = httptest.NewRequest("POST", "/v1/messages", nil)
	omittedCtx.Request = omittedCtx.Request.WithContext(service.WithResolvedTargetPlatform(omittedCtx.Request.Context(), service.PlatformOpenAI))
	bindOpenAIReasoningEffortPolicyForMessagesRequest(omittedCtx, apiKey, targetGroup, []byte(`{"model":"gpt-5"}`))
	omitted, changed, err := service.ApplyOpenAIReasoningEffortPolicyFromContext(omittedCtx.Request.Context(), body)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, body, omitted)

	// v0.2.0 超限动作同样取子分组：子分组配 deny，映射后仍超上限 → 拒绝。
	denyTarget := *targetGroup
	denyTarget.MaxReasoningEffortOverLimit = service.ReasoningEffortOverLimitDeny
	denyCtx, _ := gin.CreateTestContext(httptest.NewRecorder())
	denyCtx.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	denyCtx.Request = denyCtx.Request.WithContext(service.WithResolvedTargetPlatform(denyCtx.Request.Context(), service.PlatformOpenAI))
	_, _, err = applyOpenAIReasoningEffortPolicyForGroup(denyCtx, apiKey, &denyTarget, body)
	require.Error(t, err)
	var overLimit *service.ReasoningEffortOverLimitError
	require.ErrorAs(t, err, &overLimit)

	// 只有父分组配了 deny、子分组没配 —— 父分组的 deny 必须**不**生效。
	denyParent := *parentGroup
	denyParent.MaxReasoningEffortOverLimit = service.ReasoningEffortOverLimitDeny
	denyParentCtx, _ := gin.CreateTestContext(httptest.NewRecorder())
	denyParentCtx.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	denyParentCtx.Request = denyParentCtx.Request.WithContext(service.WithResolvedTargetPlatform(denyParentCtx.Request.Context(), service.PlatformOpenAI))
	got, changed, err = applyOpenAIReasoningEffortPolicyForGroup(denyParentCtx, &service.APIKey{Group: &denyParent}, &service.Group{Platform: service.PlatformOpenAI}, body)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, body, got)

	// 委托到非 OpenAI 子分组时不生效。
	grokCtx, _ := gin.CreateTestContext(httptest.NewRecorder())
	grokCtx.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	grokCtx.Request = grokCtx.Request.WithContext(service.WithResolvedTargetPlatform(grokCtx.Request.Context(), service.PlatformGrok))
	got, changed, err = applyOpenAIReasoningEffortPolicyForGroup(grokCtx, apiKey, &service.Group{Platform: service.PlatformGrok}, body)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, body, got)

	// 关键差异用例：只有父分组配了上限、子分组没配 —— 上游口径会把 effort
	// 压成 low，aicat 口径必须**原样放行**。
	parentOnlyCtx, _ := gin.CreateTestContext(httptest.NewRecorder())
	parentOnlyCtx.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	parentOnlyCtx.Request = parentOnlyCtx.Request.WithContext(service.WithResolvedTargetPlatform(parentOnlyCtx.Request.Context(), service.PlatformOpenAI))
	got, changed, err = applyOpenAIReasoningEffortPolicyForGroup(parentOnlyCtx, apiKey, &service.Group{Platform: service.PlatformOpenAI}, body)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, body, got)
}

func TestClientRequestedModelUsesCompositePublicModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	targetGroupID := int64(42)
	c.Request = c.Request.WithContext(service.WithCompositeRouteDecision(c.Request.Context(), service.CompositeRouteDecision{
		Matched:        true,
		Source:         service.CompositeRouteSourceExplicit,
		PublicModel:    "public-alias",
		TargetPlatform: service.PlatformOpenAI,
		TargetGroupID:  &targetGroupID,
		UpstreamModel:  "gpt-5",
	}))

	input := buildContentModerationInput(c, nil, middleware2.AuthSubject{UserID: 42}, service.ContentModerationProtocolOpenAIChat, "gpt-5", nil)
	require.Equal(t, "public-alias", input.Model)
	require.Equal(t, service.PlatformOpenAI, input.Provider)

	fields := clientRequestedUsageFields(c, service.ChannelMappingResult{MappedModel: "gpt-5"}, "gpt-5", "gpt-5")
	require.Equal(t, "public-alias", fields.OriginalModel)
	require.Equal(t, "public-alias", fields.ChannelMappedModel)
	require.Equal(t, "public-alias\u2192gpt-5", fields.ModelMappingChain)
}

func TestResolveCompositeWebSocketRouteUsesExplicitDelegatedGroup(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/v1/responses", nil)
	targetGroupID := int64(42)
	rateMultiplier := 1.75
	resolver := service.NewCompositeRouteResolver(handlerCompositeRouteRepoStub{routes: []service.CompositeModelRoute{
		{
			ID: 1, GroupID: 7, PublicModel: "public-grok", MatchType: service.CompositeRouteMatchExact,
			TargetPlatform: service.PlatformGrok, TargetGroupID: &targetGroupID, RateMultiplier: &rateMultiplier,
			UpstreamModel: "grok-4.3", Endpoint: service.CompositeRouteEndpointResponses,
			Priority: 100, Enabled: true,
		},
	}})
	h := &OpenAIGatewayHandler{compositeResolver: resolver}
	compositeGroupID := int64(7)
	apiKey := &service.APIKey{
		GroupID: &compositeGroupID,
		Group:   &service.Group{ID: compositeGroupID, Platform: service.PlatformComposite},
	}
	payload := []byte(`{"type":"response.create","model":"public-grok","input":"hi"}`)

	rewritten, model, err := h.resolveCompositeWebSocketRoute(c, apiKey, payload, "public-grok")

	require.NoError(t, err)
	require.Equal(t, "grok-4.3", model)
	require.JSONEq(t, `{"type":"response.create","model":"grok-4.3","input":"hi"}`, string(rewritten))
	resolvedGroupID, ok := service.ResolvedPricingGroupIDFromContext(c.Request.Context())
	require.True(t, ok)
	require.Equal(t, targetGroupID, resolvedGroupID)
	resolvedMultiplier, ok := service.ResolvedRateMultiplierFromContext(c.Request.Context())
	require.True(t, ok)
	require.Equal(t, rateMultiplier, resolvedMultiplier)
}
