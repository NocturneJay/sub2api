//go:build unit

package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 公开广场端点的可见性与开关矩阵。
//
// 这里只测不依赖 DB 的纯逻辑与 fail-closed 分支：涉及真实 service 的路径由
// 集成测试覆盖。重点是「关闭时不能泄漏」「匿名 DTO 结构上不含渠道身份」。

func plazaPublicRequest(t *testing.T, h *PlazaPublicHandler, authed bool) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/plaza/models", nil)
	if authed {
		c.Set(string(servermiddleware.ContextKeyUser), servermiddleware.AuthSubject{UserID: 7})
	}
	h.Get(c)
	return w
}

func TestPlazaPublic_NilServicesFailClosed(t *testing.T) {
	// 依赖未注入（wire 漏接线）时必须 404，而不是 panic 或空响应 200。
	h := &PlazaPublicHandler{}

	w := plazaPublicRequest(t, h, false)

	require.Equal(t, http.StatusNotFound, w.Code)
}

func TestPlazaPublicChannelDTO_HasNoChannelIdentity(t *testing.T) {
	// 结构性防泄漏的回归闸门：渠道真实名称与描述（含运营内部备注、供应商代号）
	// 必须在匿名 DTO 上根本不存在。有人日后给 DTO 加回 name/description 时，
	// 这个断言会立刻失败。
	payload := plazaPublicResponse{
		Channels: []plazaPublicChannel{{
			Platforms: []plazaPublicPlatformSection{{
				Platform: "anthropic",
				Groups:   []plazaPublicGroup{{ID: 1, Name: "public", Platform: "anthropic"}},
			}},
		}},
		ModelMeta:     emptyUserModelPlazaMeta(),
		GroupRates:    map[int64]float64{},
		Authenticated: false,
	}

	raw, err := json.Marshal(payload)
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(raw, &decoded))
	channels, ok := decoded["channels"].([]any)
	require.True(t, ok)
	require.Len(t, channels, 1)
	ch, ok := channels[0].(map[string]any)
	require.True(t, ok)

	require.NotContains(t, ch, "name")
	require.NotContains(t, ch, "description")
	require.Contains(t, ch, "platforms")
}

func TestFilterPlazaPublicGroups_KeepsOnlyAllowedAndCarriesRateFields(t *testing.T) {
	groups := []service.AvailableGroupRef{
		{ID: 1, Name: "allowed", Platform: "anthropic", ImageRateIndependent: true, ImageRateMultiplier: 0.5},
		{ID: 2, Name: "denied", Platform: "anthropic"},
		{ID: 3, Name: "allowed-video", Platform: "openai", VideoRateIndependent: true, VideoRateMultiplier: 0.25},
	}
	allowed := map[int64]struct{}{1: {}, 3: {}}

	got := filterPlazaPublicGroups(groups, allowed)

	require.Len(t, got, 2)
	require.Equal(t, int64(1), got[0].ID)
	require.True(t, got[0].ImageRateIndependent)
	require.InDelta(t, 0.5, got[0].ImageRateMultiplier, 1e-9)
	require.Equal(t, int64(3), got[1].ID)
	require.True(t, got[1].VideoRateIndependent)
	require.InDelta(t, 0.25, got[1].VideoRateMultiplier, 1e-9)
}

func TestBuildPlazaPublicSections_RecomputesModelsFromVisibleGroups(t *testing.T) {
	// 只裁分组而保留全量模型会泄漏专属分组独有模型的能力面：
	// 这里可见分组只覆盖 anthropic，openai 模型必须整条消失。
	ch := service.AvailableChannel{
		Name: "internal-vendor-codename",
		SupportedModels: []service.SupportedModel{
			{Name: "claude-sonnet-4-6", Platform: "anthropic"},
			{Name: "exclusive-openai-model", Platform: "openai"},
		},
	}
	visible := []plazaPublicGroup{{ID: 1, Name: "public", Platform: "anthropic"}}

	sections := buildPlazaPublicSections(ch, visible)

	require.Len(t, sections, 1)
	require.Equal(t, "anthropic", sections[0].Platform)
	require.Len(t, sections[0].SupportedModels, 1)
	require.Equal(t, "claude-sonnet-4-6", sections[0].SupportedModels[0].Name)
	// 渠道名不出现在任何 section 里（它压根没有承载字段）。
	raw, err := json.Marshal(sections)
	require.NoError(t, err)
	require.False(t, strings.Contains(string(raw), "internal-vendor-codename"))
}

func TestBuildPlazaPublicSections_NoVisibleGroupsYieldsNil(t *testing.T) {
	ch := service.AvailableChannel{
		SupportedModels: []service.SupportedModel{{Name: "m", Platform: "anthropic"}},
	}

	require.Nil(t, buildPlazaPublicSections(ch, nil))
	// 平台为空的分组不参与切分，等价于没有可见分组。
	require.Nil(t, buildPlazaPublicSections(ch, []plazaPublicGroup{{ID: 1, Platform: ""}}))
}

func TestPlazaPublicAnonymousCache_ExpiresAndIsolates(t *testing.T) {
	var cache plazaPublicAnonymousCache
	const key = "anon|sub=0"

	require.Nil(t, cache.get(key, time.Now()), "空缓存必须 miss")

	payload := &plazaPublicResponse{Authenticated: false}
	cache.set(key, payload)
	writtenAt := time.Now()

	require.Same(t, payload, cache.get(key, writtenAt))
	require.Nil(t, cache.get(key, writtenAt.Add(plazaPublicAnonymousCacheTTL+time.Second)),
		"过期后必须 miss")
}

func TestPlazaPublicAnonymousCache_KeyedByVisibilitySwitch(t *testing.T) {
	// 缓存必须按影响可见性的开关分键：管理员关掉「匿名含订阅分组」后，
	// 不能再从缓存里把含订阅分组的旧 payload 发出去。
	var cache plazaPublicAnonymousCache
	withSub := &plazaPublicResponse{Authenticated: false}

	cache.set(anonCacheKey(service.ModelPlazaPublicRuntime{IncludeSubscriptionGroups: true}), withSub)

	now := time.Now()
	require.Same(t, withSub,
		cache.get(anonCacheKey(service.ModelPlazaPublicRuntime{IncludeSubscriptionGroups: true}), now))
	require.Nil(t,
		cache.get(anonCacheKey(service.ModelPlazaPublicRuntime{IncludeSubscriptionGroups: false}), now),
		"开关变化后必须 miss，而不是复用旧可见性下构建的 payload")
}

func TestAnonCacheKey_DistinguishesSwitch(t *testing.T) {
	on := anonCacheKey(service.ModelPlazaPublicRuntime{IncludeSubscriptionGroups: true})
	off := anonCacheKey(service.ModelPlazaPublicRuntime{IncludeSubscriptionGroups: false})
	require.NotEqual(t, on, off)
}
