//go:build unit

package handler

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

type openAIImagesFailoverAccountRepo struct {
	service.AccountRepository
	accounts []service.Account
}

func (r openAIImagesFailoverAccountRepo) GetByID(_ context.Context, id int64) (*service.Account, error) {
	for i := range r.accounts {
		if r.accounts[i].ID == id {
			account := r.accounts[i]
			return &account, nil
		}
	}
	return nil, service.ErrNoAvailableAccounts
}

func (r openAIImagesFailoverAccountRepo) ListSchedulableByGroupIDAndPlatform(_ context.Context, _ int64, platform string) ([]service.Account, error) {
	return r.accountsForPlatform(platform), nil
}

func (r openAIImagesFailoverAccountRepo) ListSchedulableByPlatform(_ context.Context, platform string) ([]service.Account, error) {
	return r.accountsForPlatform(platform), nil
}

func (r openAIImagesFailoverAccountRepo) ListSchedulableUngroupedByPlatform(_ context.Context, platform string) ([]service.Account, error) {
	return r.accountsForPlatform(platform), nil
}

func (r openAIImagesFailoverAccountRepo) accountsForPlatform(platform string) []service.Account {
	out := make([]service.Account, 0, len(r.accounts))
	for _, account := range r.accounts {
		if account.Platform == platform {
			out = append(out, account)
		}
	}
	return out
}

type openAIImagesFailoverHTTPUpstream struct {
	service.HTTPUpstream
	mu         sync.Mutex
	accountIDs []int64
}

type geminiImagesPartialTokenCache struct{}

func (geminiImagesPartialTokenCache) GetAccessToken(context.Context, string) (string, error) {
	return "vertex-token", nil
}
func (geminiImagesPartialTokenCache) SetAccessToken(context.Context, string, string, time.Duration) error {
	return nil
}
func (geminiImagesPartialTokenCache) DeleteAccessToken(context.Context, string) error { return nil }
func (geminiImagesPartialTokenCache) AcquireRefreshLock(context.Context, string, time.Duration) (bool, error) {
	return true, nil
}
func (geminiImagesPartialTokenCache) ReleaseRefreshLock(context.Context, string) error { return nil }

type geminiImagesPartialHTTPUpstream struct {
	service.HTTPUpstream
	mu         sync.Mutex
	accountIDs []int64
}

func (u *geminiImagesPartialHTTPUpstream) Do(_ *http.Request, _ string, accountID int64, _ int) (*http.Response, error) {
	u.mu.Lock()
	u.accountIDs = append(u.accountIDs, accountID)
	call := len(u.accountIDs)
	u.mu.Unlock()
	status := http.StatusOK
	body := `{"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"image/png","data":"aW1hZ2U="}}]}}],"usageMetadata":{"promptTokenCount":7,"candidatesTokenCount":3,"candidatesTokensDetails":[{"modality":"IMAGE","tokenCount":1290}]}}`
	if call == 2 {
		status = http.StatusUnauthorized
		body = `{"error":{"code":401,"message":"account unavailable","status":"UNAUTHENTICATED"}}`
	}
	return &http.Response{
		StatusCode: status,
		Header: http.Header{
			"Content-Type":      []string{"application/json"},
			"X-Goog-Request-Id": []string{"gemini-partial"},
		},
		Body: io.NopCloser(bytes.NewBufferString(body)),
	}, nil
}

func (u *geminiImagesPartialHTTPUpstream) calls() []int64 {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]int64(nil), u.accountIDs...)
}

type geminiImagesPartialUsageRepo struct {
	service.UsageLogRepository
	last *service.UsageLog
}

func (r *geminiImagesPartialUsageRepo) Create(_ context.Context, log *service.UsageLog) (bool, error) {
	r.last = log
	return true, nil
}

func (u *openAIImagesFailoverHTTPUpstream) Do(_ *http.Request, _ string, accountID int64, _ int) (*http.Response, error) {
	u.mu.Lock()
	u.accountIDs = append(u.accountIDs, accountID)
	u.mu.Unlock()
	return &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type": []string{"text/event-stream"},
			"X-Request-Id": []string{"req_img_failover"},
		},
		Body: io.NopCloser(bytes.NewBufferString(
			"data: {\"type\":\"error\",\"error\":{\"type\":\"server_error\",\"code\":\"server_error\",\"message\":\"image backend unavailable\"}}\n\n",
		)),
	}, nil
}

func (u *openAIImagesFailoverHTTPUpstream) calls() []int64 {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]int64(nil), u.accountIDs...)
}

func TestOpenAIGatewayHandlerImages_ServerErrorFailsOverAndReturnsClearErrorWhenExhausted(t *testing.T) {
	gin.SetMode(gin.TestMode)
	groupID := int64(3130)
	accounts := []service.Account{
		{
			ID:          1,
			Name:        "image-account-1",
			Platform:    service.PlatformOpenAI,
			Type:        service.AccountTypeOAuth,
			Status:      service.StatusActive,
			Schedulable: true,
			Concurrency: 0,
			Priority:    0,
			Credentials: map[string]any{"access_token": "token-1"},
		},
		{
			ID:          2,
			Name:        "image-account-2",
			Platform:    service.PlatformOpenAI,
			Type:        service.AccountTypeOAuth,
			Status:      service.StatusActive,
			Schedulable: true,
			Concurrency: 0,
			Priority:    1,
			Credentials: map[string]any{"access_token": "token-2"},
		},
	}
	accountRepo := openAIImagesFailoverAccountRepo{accounts: accounts}
	upstream := &openAIImagesFailoverHTTPUpstream{}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	gatewayService := service.NewOpenAIGatewayService(
		accountRepo,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		cfg,
		nil,
		nil,
		nil,
		nil,
		nil,
		upstream,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
	)
	billingService := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billingService.Stop)
	concurrencyService := service.NewConcurrencyService(nil)
	handler := NewOpenAIGatewayHandler(
		gatewayService,
		concurrencyService,
		billingService,
		service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg),
		nil,
		nil,
		nil,
		nil,
		cfg,
	)
	handler.maxAccountSwitches = 10

	body := []byte(`{"model":"gpt-image-2","prompt":"draw a cat","quality":"high","size":"1536x1024"}`)
	core, observedLogs := observer.New(zap.DebugLevel)
	requestCtx := logger.IntoContext(context.Background(), zap.New(core))
	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body)).WithContext(requestCtx)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = req
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{
		ID:      99,
		GroupID: &groupID,
		Group: &service.Group{
			ID:                   groupID,
			AllowImageGeneration: true,
		},
		User: &service.User{ID: 100},
	})
	c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 100, Concurrency: 0})

	handler.Images(c)

	accountSelectingLogs := observedLogs.FilterMessage("openai.images.account_selecting").All()
	require.NotEmpty(t, accountSelectingLogs)
	loggedFields := make(map[string]string)
	for _, field := range accountSelectingLogs[0].Context {
		loggedFields[field.Key] = field.String
	}
	require.Equal(t, "high", loggedFields["img_quality"])
	require.Equal(t, "1536x1024", loggedFields["img_size"])
	require.NotContains(t, loggedFields, "prompt")

	require.Equal(t, []int64{1, 2}, upstream.calls())
	require.Equal(t, http.StatusBadGateway, rec.Code)
	require.Equal(t, "upstream_error", gjson.GetBytes(rec.Body.Bytes(), "error.type").String())
	require.Equal(t, "Upstream service temporarily unavailable", gjson.GetBytes(rec.Body.Bytes(), "error.message").String())

	rawEvents, ok := c.Get(service.OpsUpstreamErrorsKey)
	require.True(t, ok)
	events, ok := rawEvents.([]*service.OpsUpstreamErrorEvent)
	require.True(t, ok)
	require.Len(t, events, 2)
	require.Equal(t, "failover", events[0].Kind)
	require.Equal(t, "failover", events[1].Kind)
}

func TestOpenAIGatewayHandlerImages_GeminiPartialNDoesNotFailOverAndBillsActualCount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	groupID := int64(3131)
	zeroPrice := 0.0
	accounts := []service.Account{
		{
			ID: 11, Name: "gemini-image-1", Platform: service.PlatformGemini,
			Type: service.AccountTypeServiceAccount, Status: service.StatusActive, Schedulable: true, Priority: 0,
			Credentials: map[string]any{
				"service_account_json": map[string]any{
					"type": "service_account", "project_id": "vertex-project", "private_key_id": "kid",
					"private_key": "cached-token-does-not-use-key", "client_email": "svc@vertex-project.iam.gserviceaccount.com",
				},
				"location": "global", "model_mapping": map[string]any{"Nano-Banana-Pro": "gemini-3-pro-image"},
			},
		},
		{
			ID: 12, Name: "gemini-image-2", Platform: service.PlatformGemini,
			Type: service.AccountTypeServiceAccount, Status: service.StatusActive, Schedulable: true, Priority: 1,
			Credentials: map[string]any{
				"service_account_json": map[string]any{
					"type": "service_account", "project_id": "vertex-project-2", "private_key_id": "kid-2",
					"private_key": "cached-token-does-not-use-key", "client_email": "svc@vertex-project-2.iam.gserviceaccount.com",
				},
				"location": "global", "model_mapping": map[string]any{"Nano-Banana-Pro": "gemini-3-pro-image"},
			},
		},
	}
	accountRepo := openAIImagesFailoverAccountRepo{accounts: accounts}
	usageRepo := &geminiImagesPartialUsageRepo{}
	upstream := &geminiImagesPartialHTTPUpstream{}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Default.RateMultiplier = 1
	billingService := service.NewBillingService(cfg, nil)
	billingCache := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billingCache.Stop)
	openAIService := service.NewOpenAIGatewayService(
		accountRepo, usageRepo, nil, nil, nil, nil, nil, cfg, nil, nil,
		billingService, nil, billingCache, nil, nil, nil, nil, nil, nil, nil, nil, nil,
	)
	platformService := service.NewGatewayService(
		accountRepo, nil, nil, nil, nil, nil, nil, nil, cfg, nil, nil,
		billingService, nil, billingCache, nil, upstream, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
	)
	tokenProvider := service.NewGeminiTokenProvider(accountRepo, geminiImagesPartialTokenCache{}, nil)
	geminiService := service.NewGeminiMessagesCompatService(accountRepo, nil, nil, nil, tokenProvider, nil, upstream, nil, cfg)
	h := NewOpenAIGatewayHandler(
		openAIService, service.NewConcurrencyService(nil), billingCache,
		service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg,
	)
	h.SetGeminiImagesDependencies(platformService, geminiService)
	h.maxAccountSwitchesGemini = 10

	body := []byte(`{"model":"Nano-Banana-Pro","prompt":"draw","n":2,"size":"auto"}`)
	requestCtx := context.WithValue(context.Background(), ctxkey.ForcePlatform, service.PlatformGemini)
	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body)).WithContext(requestCtx)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = req
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{
		ID: 100, GroupID: &groupID,
		Group: &service.Group{
			ID: groupID, Platform: service.PlatformGemini, Status: service.StatusActive,
			AllowImageGeneration: true, RateMultiplier: 1, ImagePrice1K: &zeroPrice,
		},
		User: &service.User{ID: 101, Status: service.StatusActive, Balance: 100},
	})
	c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 101, Concurrency: 0})

	h.Images(c)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Len(t, gjson.GetBytes(rec.Body.Bytes(), "data").Array(), 1)
	require.Equal(t, []int64{11, 11}, upstream.calls(), "a partial n response must not switch accounts and regenerate completed images")
	require.NotNil(t, usageRepo.last)
	require.Equal(t, 1, usageRepo.last.ImageCount)
	require.Equal(t, 7, usageRepo.last.InputTokens)
	require.Equal(t, 3, usageRepo.last.OutputTokens)
	require.Equal(t, 1290, usageRepo.last.ImageOutputTokens)
	require.NotNil(t, usageRepo.last.ImageSize)
	require.Equal(t, service.ImageBillingSize1K, *usageRepo.last.ImageSize)
}
