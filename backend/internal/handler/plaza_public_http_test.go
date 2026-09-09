//go:build unit

package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 公开广场端点的 HTTP 端到端验证。
//
// 与 plaza_public_handler_test.go 的纯函数测试不同，这里装配真实的
// SettingService / ChannelService / APIKeyService（仓储用内存假实现），
// 通过真实 gin 路由发真实请求，断言响应状态码与响应体。
//
// 假仓储用「嵌入接口 + 只覆盖被调方法」的写法：任何未预期的仓储调用会
// 因为嵌入的 nil 接口而 panic，从而暴露出代码路径的意外扩张。

type plazaFakeGroupRepo struct {
	service.GroupRepository
	groups []service.Group
}

func (f *plazaFakeGroupRepo) ListActive(context.Context) ([]service.Group, error) {
	return f.groups, nil
}

type plazaFakeChannelRepo struct {
	service.ChannelRepository
	channels []service.Channel
}

func (f *plazaFakeChannelRepo) ListAll(context.Context) ([]service.Channel, error) {
	return f.channels, nil
}

type plazaFakeSettingRepo struct {
	service.SettingRepository
	values map[string]string
}

func (f *plazaFakeSettingRepo) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	out := make(map[string]string, len(keys))
	for _, k := range keys {
		if v, ok := f.values[k]; ok {
			out[k] = v
		}
	}
	return out, nil
}

func (f *plazaFakeSettingRepo) GetValue(_ context.Context, key string) (string, error) {
	if v, ok := f.values[key]; ok {
		return v, nil
	}
	return "", service.ErrSettingNotFound
}

// plazaHTTPFixture 组装一套「一个公开分组 + 一个专属分组 + 一个订阅分组」的数据，
// 三个分组各挂一个独有模型，便于断言裁剪是否连模型一起生效。
func plazaHTTPFixture(t *testing.T, settings map[string]string) *gin.Engine {
	t.Helper()

	groups := []service.Group{
		{ID: 1, Name: "public-group", Platform: "anthropic", RateMultiplier: 1.5},
		{ID: 2, Name: "exclusive-group", Platform: "openai", RateMultiplier: 1.0, IsExclusive: true},
		{ID: 3, Name: "subscription-group", Platform: "gemini", RateMultiplier: 0.8, SubscriptionType: "subscription"},
	}
	// 每个平台各挂一个独有模型，与上面三个分组的平台一一对应：
	// 这样「分组被裁掉时其独有模型是否也消失」才能被真实断言。
	channels := []service.Channel{
		{
			ID:          10,
			Name:        "vendor-codename-internal",
			Description: "内部备注：供应商 X，折扣渠道",
			Status:      service.StatusActive,
			GroupIDs:    []int64{1, 2, 3},
			ModelMapping: map[string]map[string]string{
				"anthropic": {"public-model": "public-model"},
				"openai":    {"exclusive-model": "exclusive-model"},
				"gemini":    {"subscription-model": "subscription-model"},
			},
		},
	}

	groupRepo := &plazaFakeGroupRepo{groups: groups}
	channelRepo := &plazaFakeChannelRepo{channels: channels}
	settingRepo := &plazaFakeSettingRepo{values: settings}

	cfg := &config.Config{}
	settingService := service.NewSettingService(settingRepo, cfg)
	channelService := service.NewChannelService(channelRepo, groupRepo, nil, nil, nil)
	apiKeyService := service.NewAPIKeyService(nil, nil, groupRepo, nil, nil, nil, cfg)

	h := NewPlazaPublicHandler(channelService, apiKeyService, settingService)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/api/v1/plaza/models", h.Get)
	return r
}

func plazaHTTPGet(t *testing.T, r *gin.Engine, authed bool) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/plaza/models", nil)
	w := httptest.NewRecorder()
	if authed {
		// 直接注入 AuthSubject，等价于 OptionalJWT 校验通过后的状态。
		r2 := gin.New()
		r2.Use(func(c *gin.Context) {
			c.Set(string(servermiddleware.ContextKeyUser), servermiddleware.AuthSubject{UserID: 7})
			c.Next()
		})
		for _, route := range r.Routes() {
			r2.Handle(route.Method, route.Path, route.HandlerFunc)
		}
		r2.ServeHTTP(w, req)
	} else {
		r.ServeHTTP(w, req)
	}

	var body map[string]any
	if w.Body.Len() > 0 {
		_ = json.Unmarshal(w.Body.Bytes(), &body)
	}
	return w, body
}

func TestPlazaPublicHTTP_SwitchMatrix(t *testing.T) {
	cases := []struct {
		name             string
		availableChannel string
		publicEnabled    string
		wantAnonStatus   int
	}{
		{"两个开关都关(默认态)", "false", "false", http.StatusNotFound},
		{"只开公开开关——主闸未开必须仍然 404", "false", "true", http.StatusNotFound},
		{"只开主闸——匿名仍需公开开关", "true", "false", http.StatusNotFound},
		{"两个都开", "true", "true", http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := plazaHTTPFixture(t, map[string]string{
				service.SettingKeyAvailableChannelsEnabled: tc.availableChannel,
				service.SettingKeyModelPlazaPublicEnabled:  tc.publicEnabled,
			})
			w, _ := plazaHTTPGet(t, r, false)
			require.Equal(t, tc.wantAnonStatus, w.Code)
		})
	}
}

func TestPlazaPublicHTTP_NoInversionAgainstLoggedInUser(t *testing.T) {
	// 倒挂回归闸门：主闸关闭时，匿名不得比登录用户看到更多。
	r := plazaHTTPFixture(t, map[string]string{
		service.SettingKeyAvailableChannelsEnabled: "false",
		service.SettingKeyModelPlazaPublicEnabled:  "true",
	})

	anon, _ := plazaHTTPGet(t, r, false)
	require.Equal(t, http.StatusNotFound, anon.Code,
		"主闸关闭时匿名必须 404，否则「去掉 Authorization 头反而能看到更多」")
}

func TestPlazaPublicHTTP_AnonymousResponseHidesSensitiveData(t *testing.T) {
	r := plazaHTTPFixture(t, map[string]string{
		service.SettingKeyAvailableChannelsEnabled: "true",
		service.SettingKeyModelPlazaPublicEnabled:  "true",
	})

	w, body := plazaHTTPGet(t, r, false)
	require.Equal(t, http.StatusOK, w.Code)

	raw := w.Body.String()

	// 渠道身份不得出现。
	require.NotContains(t, raw, "vendor-codename-internal", "渠道真实名称泄漏")
	require.NotContains(t, raw, "内部备注", "渠道描述(运营内部备注)泄漏")

	// 专属分组连名字都不能出现。
	require.NotContains(t, raw, "exclusive-group", "专属分组名泄漏")
	require.NotContains(t, raw, "exclusive-model", "专属分组独有模型泄漏(能力面)")

	// 订阅分组默认不可见。
	require.NotContains(t, raw, "subscription-group", "订阅分组在未开启开关时泄漏")
	require.NotContains(t, raw, "subscription-model", "订阅分组独有模型泄漏")

	// 公开分组应当可见。
	require.Contains(t, raw, "public-group")

	// 匿名视图不得携带用户维度数据。
	data, _ := body["data"].(map[string]any)
	require.NotNil(t, data)
	require.Equal(t, false, data["authenticated"])
	rates, ok := data["group_rates"].(map[string]any)
	require.True(t, ok)
	require.Empty(t, rates, "匿名 group_rates 必须为空")
}

func TestPlazaPublicHTTP_SubscriptionGroupsGatedBySwitch(t *testing.T) {
	r := plazaHTTPFixture(t, map[string]string{
		service.SettingKeyAvailableChannelsEnabled:                  "true",
		service.SettingKeyModelPlazaPublicEnabled:                   "true",
		service.SettingKeyModelPlazaPublicIncludeSubscriptionGroups: "true",
	})

	w, _ := plazaHTTPGet(t, r, false)
	require.Equal(t, http.StatusOK, w.Code)
	raw := w.Body.String()

	require.Contains(t, raw, "subscription-group", "显式开启后订阅分组应可见")
	// 专属分组不受该开关影响，任何情况下都不可见。
	require.NotContains(t, raw, "exclusive-group",
		"专属分组不得因订阅开关而变为可见")
	require.False(t, strings.Contains(raw, "vendor-codename-internal"))
}
