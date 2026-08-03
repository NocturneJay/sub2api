package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type geminiImagesWaitCache struct {
	service.ConcurrencyCache
	canWait        bool
	acquire        bool
	increments     int
	decrements     int
	releases       int
	lastMaxWaiting int
}

func (c *geminiImagesWaitCache) IncrementAccountWaitCount(_ context.Context, _ int64, maxWait int) (bool, error) {
	c.increments++
	c.lastMaxWaiting = maxWait
	return c.canWait, nil
}

func (c *geminiImagesWaitCache) DecrementAccountWaitCount(context.Context, int64) error {
	c.decrements++
	return nil
}

func (c *geminiImagesWaitCache) AcquireAccountSlot(context.Context, int64, int, string) (bool, error) {
	return c.acquire, nil
}

func (c *geminiImagesWaitCache) ReleaseAccountSlot(context.Context, int64, string) error {
	c.releases++
	return nil
}

func TestOpenAIImagesSchedulingModelUsesChannelMapping(t *testing.T) {
	require.Equal(t, "gemini-3-pro-image", openAIImagesSchedulingModel(
		"Nano-Banana-Pro",
		service.ChannelMappingResult{MappedModel: "gemini-3-pro-image"},
	))
	require.Equal(t, "Nano-Banana-Pro", openAIImagesSchedulingModel(
		"Nano-Banana-Pro",
		service.ChannelMappingResult{},
	))
}

func TestAcquireGeminiImagesAccountSlotHonorsWaitQueue(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Run("queue full", func(t *testing.T) {
		cache := &geminiImagesWaitCache{canWait: false}
		h := &OpenAIGatewayHandler{concurrencyHelper: NewConcurrencyHelper(service.NewConcurrencyService(cache), SSEPingFormatNone, 0)}
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
		streamStarted := false
		release, ok := h.acquireGeminiImagesAccountSlot(c, geminiImagesWaitSelection(7, 3, time.Second), false, &streamStarted, zap.NewNop())
		require.False(t, ok)
		require.Nil(t, release)
		require.Equal(t, http.StatusTooManyRequests, recorder.Code)
		require.Equal(t, 1, cache.increments)
		require.Equal(t, 3, cache.lastMaxWaiting)
		require.Zero(t, cache.decrements)
	})

	t.Run("success removes wait count", func(t *testing.T) {
		cache := &geminiImagesWaitCache{canWait: true, acquire: true}
		h := &OpenAIGatewayHandler{concurrencyHelper: NewConcurrencyHelper(service.NewConcurrencyService(cache), SSEPingFormatNone, 0)}
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
		streamStarted := false
		release, ok := h.acquireGeminiImagesAccountSlot(c, geminiImagesWaitSelection(7, 4, time.Second), false, &streamStarted, zap.NewNop())
		require.True(t, ok)
		require.NotNil(t, release)
		require.Equal(t, 1, cache.increments)
		require.Equal(t, 1, cache.decrements)
		release()
		require.Equal(t, 1, cache.releases)
	})

	t.Run("cancellation removes wait count", func(t *testing.T) {
		cache := &geminiImagesWaitCache{canWait: true, acquire: false}
		h := &OpenAIGatewayHandler{concurrencyHelper: NewConcurrencyHelper(service.NewConcurrencyService(cache), SSEPingFormatNone, 0)}
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil).WithContext(ctx)
		streamStarted := false
		release, ok := h.acquireGeminiImagesAccountSlot(c, geminiImagesWaitSelection(7, 5, time.Second), false, &streamStarted, zap.NewNop())
		require.False(t, ok)
		require.Nil(t, release)
		require.Equal(t, 1, cache.increments)
		require.Equal(t, 1, cache.decrements)
	})
}

func geminiImagesWaitSelection(accountID int64, maxWaiting int, timeout time.Duration) *service.AccountSelectionResult {
	return &service.AccountSelectionResult{
		Account: &service.Account{ID: accountID},
		WaitPlan: &service.AccountWaitPlan{
			AccountID: accountID, MaxConcurrency: 1, MaxWaiting: maxWaiting, Timeout: timeout,
		},
	}
}
