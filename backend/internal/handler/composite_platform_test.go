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

func TestCompositeTargetPlatformAllowedResolvesKnownAllowedModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/embeddings", nil)
	apiKey := &service.APIKey{Group: &service.Group{Platform: service.PlatformComposite}}

	require.True(t, compositeTargetPlatformAllowed(c, apiKey, "text-embedding-3-large", service.PlatformOpenAI))
	platform, ok := service.ResolvedTargetPlatformFromContext(c.Request.Context())
	require.True(t, ok)
	require.Equal(t, service.PlatformOpenAI, platform)
}

func TestOpenAICompatibleTextTargetAllowsCompositeGrokModel(t *testing.T) {
	gin.SetMode(gin.TestMode)

	for _, path := range []string{"/v1/messages", "/v1/chat/completions"} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("POST", path, nil)
		apiKey := &service.APIKey{Group: &service.Group{Platform: service.PlatformComposite}}

		require.True(t, openAICompatibleTextTargetAllowed(c, apiKey, "grok-4.3"), "path=%s", path)
		platform, ok := service.ResolvedTargetPlatformFromContext(c.Request.Context())
		require.True(t, ok, "path=%s", path)
		require.Equal(t, service.PlatformGrok, platform, "path=%s", path)
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

func TestClientRequestedModelUsesCompositePublicModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	c.Request = c.Request.WithContext(service.WithCompositeRouteDecision(c.Request.Context(), service.CompositeRouteDecision{
		Matched:        true,
		Source:         service.CompositeRouteSourceExplicit,
		PublicModel:    "public-alias",
		TargetPlatform: service.PlatformOpenAI,
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
