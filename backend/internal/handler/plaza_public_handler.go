package handler

import (
	"sort"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// 公开(可匿名)模型广场聚合端点。
//
// 设计要点:
//  1. 单次聚合:一次请求返回 channels + model_meta + group_rates,替代广场页原来
//     并发打三个需登录接口的做法。
//  2. 独立匿名 DTO:匿名视图用 plazaPublicChannel 等独立类型,渠道真实名称与
//     描述在类型上就不存在。渠道描述常含供应商代号与运营内部备注,靠运行时
//     置空迟早会因为「给登录态 DTO 加字段」而漏出去;类型隔离让这类泄漏变成
//     编译错误。
//  3. fail-closed:开关关闭返回 404 而不是 401 —— 前端 apiClient 对任意 401
//     都会清 token 并硬跳 /login,用 401 会把开关关闭直接变成踢人。
//  4. 匿名视图 TTL 缓存:ListAvailable 每次都是 channel 全表扫 + 逐模型查
//     LiteLLM 定价,零缓存。匿名响应与用户无关,可安全共享;登录路径完全
//     不读不写该缓存,且缓存值类型即匿名 DTO 类型,用类型系统再兜一层。

// plazaPublicGroup 匿名/登录视图共用的分组信息。
// 与 userAvailableGroup 字段一致但独立定义:两者服务不同端点,避免一侧加字段
// 时无意扩大另一侧的暴露面。
type plazaPublicGroup struct {
	ID                 int64   `json:"id"`
	Name               string  `json:"name"`
	Platform           string  `json:"platform"`
	SubscriptionType   string  `json:"subscription_type"`
	RateMultiplier     float64 `json:"rate_multiplier"`
	PeakRateEnabled    bool    `json:"peak_rate_enabled"`
	PeakStart          string  `json:"peak_start"`
	PeakEnd            string  `json:"peak_end"`
	PeakRateMultiplier float64 `json:"peak_rate_multiplier"`
	IsExclusive        bool    `json:"is_exclusive"`

	ImageRateIndependent bool    `json:"image_rate_independent"`
	ImageRateMultiplier  float64 `json:"image_rate_multiplier"`
	VideoRateIndependent bool    `json:"video_rate_independent"`
	VideoRateMultiplier  float64 `json:"video_rate_multiplier"`
}

// plazaPublicPlatformSection 单渠道内某平台的子视图。
type plazaPublicPlatformSection struct {
	Platform        string               `json:"platform"`
	Groups          []plazaPublicGroup   `json:"groups"`
	SupportedModels []userSupportedModel `json:"supported_models"`
}

// plazaPublicChannel 广场视图里的渠道条目。
//
// 刻意不含 Name / Description:广场按模型与分组组织,渠道身份对展示毫无用处,
// 而 Description 是运营内部备注的唯一泄漏通道。
type plazaPublicChannel struct {
	Platforms []plazaPublicPlatformSection `json:"platforms"`
}

// plazaPublicResponse 公开广场聚合响应。
type plazaPublicResponse struct {
	Channels  []plazaPublicChannel    `json:"channels"`
	ModelMeta *service.ModelPlazaMeta `json:"model_meta"`
	// GroupRates 用户专属倍率;匿名恒为空对象(绝不查询用户维度数据)。
	GroupRates map[int64]float64 `json:"group_rates"`
	// Authenticated 让前端明确区分「匿名视图」与「登录视图」,而不是靠猜。
	Authenticated bool `json:"authenticated"`
}

// plazaPublicAnonymousCacheTTL 匿名视图缓存时长。
// 取值权衡:公开端点无鉴权且每请求约 6-8 次 DB 查询(含 channel 全表扫),
// 仅靠限流仍可能在额度内打满;30s 足以把放大面压到每分钟 1-2 次真实计算,
// 又不会让管理员改价后长时间看不到效果。
const plazaPublicAnonymousCacheTTL = 30 * time.Second

// plazaPublicAnonymousCache 只缓存匿名视图。
// 登录视图含用户专属倍率与个人可见分组,永远不进这里。
//
// key 必须携带所有影响可见性的输入:否则管理员关掉
// model_plaza_public_include_subscription_groups 之后,旧 payload 仍会把订阅型
// 分组继续发给匿名访客,直到 TTL 到期。
type plazaPublicAnonymousCache struct {
	mu        sync.RWMutex
	key       string
	payload   *plazaPublicResponse
	expiresAt time.Time
}

func (c *plazaPublicAnonymousCache) get(key string, now time.Time) *plazaPublicResponse {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.payload == nil || c.key != key || now.After(c.expiresAt) {
		return nil
	}
	return c.payload
}

// set 以写入时刻起算 TTL。
// 不能沿用构建开始前采样的时间戳:那样有效 TTL = TTL - 构建耗时,负载升高时
// 构建变慢 → 有效 TTL 缩短 → 命中率下降 → 更多请求走构建,形成正反馈;
// 构建耗时一旦超过 TTL,写入即已过期,缓存在最需要它的时候永久失效。
func (c *plazaPublicAnonymousCache) set(key string, payload *plazaPublicResponse) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.key = key
	c.payload = payload
	c.expiresAt = time.Now().Add(plazaPublicAnonymousCacheTTL)
}

// anonCacheKey 由影响匿名可见性的开关组合而成。
func anonCacheKey(runtime service.ModelPlazaPublicRuntime) string {
	if runtime.IncludeSubscriptionGroups {
		return "anon|sub=1"
	}
	return "anon|sub=0"
}

// PlazaPublicHandler 公开模型广场 handler。
type PlazaPublicHandler struct {
	channelService *service.ChannelService
	apiKeyService  *service.APIKeyService
	settingService *service.SettingService
	anonCache      plazaPublicAnonymousCache
	anonFlight     singleflight.Group
}

// NewPlazaPublicHandler 创建公开模型广场 handler。
func NewPlazaPublicHandler(
	channelService *service.ChannelService,
	apiKeyService *service.APIKeyService,
	settingService *service.SettingService,
) *PlazaPublicHandler {
	return &PlazaPublicHandler{
		channelService: channelService,
		apiKeyService:  apiKeyService,
		settingService: settingService,
	}
}

// Get 返回模型广场聚合数据。
//
// 匿名可访问,但受 model_plaza_public_enabled 开关控制;带 Authorization 头时
// OptionalJWT 已完成严格校验,此处按登录态返回该用户的可见分组与专属倍率。
// GET /api/v1/plaza/models
func (h *PlazaPublicHandler) Get(c *gin.Context) {
	ctx := c.Request.Context()
	subject, authenticated := middleware.GetAuthSubjectFromContext(c)

	if h.settingService == nil || h.channelService == nil || h.apiKeyService == nil {
		response.NotFound(c, "Model plaza is not available")
		return
	}

	// available_channels_enabled 是「是否对外披露渠道与定价」的既有主闸,
	// 登录与匿名都必须先过它。匿名分支若只看自己的开关,会出现
	// available_channels=false + public=true 时「对互联网 200、对自己用户 404」
	// 的倒挂——而 available_channels 默认就是 false,那正是管理员只打开公开
	// 广场时会落入的状态,登录用户去掉 Authorization 头反而能拿到更多数据。
	if !h.settingService.GetAvailableChannelsRuntime(ctx).Enabled {
		response.NotFound(c, "Model plaza is not available")
		return
	}

	// 匿名再额外要求公开开关。开关只读一次并向下传递:settingRepo.GetMultiple
	// 是无缓存的真实查询,而这是可匿名访问的端点,每多读一次就是一次可被外部
	// 无限触发的 DB 查询。
	if !authenticated {
		runtime := h.settingService.GetModelPlazaPublicRuntime(ctx)
		if !runtime.Enabled {
			response.NotFound(c, "Model plaza is not available")
			return
		}

		key := anonCacheKey(runtime)
		if cached := h.anonCache.get(key, time.Now()); cached != nil {
			response.Success(c, cached)
			return
		}

		// singleflight:缓存冷启动与每次过期瞬间,并发请求会同时 miss。
		// 单次构建约 6-7 条 DB 往返(含 channel 全表扫)加逐模型定价回填,
		// 不做在途去重则 DB 放大面等于并发数而非每 TTL 一次。
		v, err, _ := h.anonFlight.Do(key, func() (any, error) {
			payload, buildErr := h.buildAnonymous(c, runtime)
			if buildErr != nil {
				return nil, buildErr
			}
			h.anonCache.set(key, payload)
			return payload, nil
		})
		if err != nil {
			response.ErrorFrom(c, err)
			return
		}
		response.Success(c, v.(*plazaPublicResponse))
		return
	}

	payload, err := h.buildAuthenticated(c, subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, payload)
}

// buildAnonymous 组装匿名视图:公开且(默认)非订阅的分组,不含任何用户维度数据。
// runtime 由调用方读取后传入,避免同一请求内重复查询 settings。
func (h *PlazaPublicHandler) buildAnonymous(
	c *gin.Context,
	runtime service.ModelPlazaPublicRuntime,
) (*plazaPublicResponse, error) {
	ctx := c.Request.Context()

	groups, err := h.apiKeyService.GetAnonymousVisibleGroups(ctx, runtime.IncludeSubscriptionGroups)
	if err != nil {
		return nil, err
	}
	allowed := groupIDSet(groups)

	channels, visibleModels, err := h.buildChannels(c, allowed)
	if err != nil {
		return nil, err
	}
	meta, err := h.visibleModelMeta(c, visibleModels)
	if err != nil {
		return nil, err
	}

	return &plazaPublicResponse{
		Channels:  channels,
		ModelMeta: meta,
		// 匿名恒为空:绝不调用 GetUserGroupRates,即便传 0 也不行。
		GroupRates:    map[int64]float64{},
		Authenticated: false,
	}, nil
}

// buildAuthenticated 组装登录视图:与 /channels/available 同一可见性口径,
// 额外附带该用户的专属倍率。
func (h *PlazaPublicHandler) buildAuthenticated(c *gin.Context, userID int64) (*plazaPublicResponse, error) {
	ctx := c.Request.Context()

	groups, err := h.apiKeyService.GetAvailableGroups(ctx, userID)
	if err != nil {
		return nil, err
	}
	allowed := groupIDSet(groups)

	channels, visibleModels, err := h.buildChannels(c, allowed)
	if err != nil {
		return nil, err
	}
	meta, err := h.visibleModelMeta(c, visibleModels)
	if err != nil {
		return nil, err
	}

	rates, err := h.apiKeyService.GetUserGroupRates(ctx, userID)
	if err != nil {
		return nil, err
	}
	if rates == nil {
		rates = map[int64]float64{}
	}

	return &plazaPublicResponse{
		Channels:      channels,
		ModelMeta:     meta,
		GroupRates:    rates,
		Authenticated: true,
	}, nil
}

// buildChannels 按 allowed 分组集合裁剪渠道，并返回裁剪后实际可见的模型名集合。
//
// 顺序很重要:先裁分组 → 按剩余分组的平台集合重算 supported_models → 空 section
// 丢弃 → 空渠道丢弃。只裁分组而保留全量模型会把专属分组独有的模型名泄漏出去,
// 等于暴露专属渠道的能力面。
func (h *PlazaPublicHandler) buildChannels(
	c *gin.Context,
	allowed map[int64]struct{},
) ([]plazaPublicChannel, map[string]struct{}, error) {
	all, err := h.channelService.ListAvailable(c.Request.Context())
	if err != nil {
		return nil, nil, err
	}

	out := make([]plazaPublicChannel, 0, len(all))
	visibleModels := make(map[string]struct{}, 64)
	for _, ch := range all {
		if ch.Status != service.StatusActive {
			continue
		}
		visibleGroups := filterPlazaPublicGroups(ch.Groups, allowed)
		if len(visibleGroups) == 0 {
			continue
		}
		sections := buildPlazaPublicSections(ch, visibleGroups)
		if len(sections) == 0 {
			continue
		}
		for _, s := range sections {
			for _, m := range s.SupportedModels {
				visibleModels[m.Name] = struct{}{}
			}
		}
		out = append(out, plazaPublicChannel{Platforms: sections})
	}
	return out, visibleModels, nil
}

// visibleModelMeta 读取端点标签元数据并按可见模型名过滤。
// 元数据缺失或读取失败不应打断广场渲染,降级为不展示端点标签。
func (h *PlazaPublicHandler) visibleModelMeta(
	c *gin.Context,
	visible map[string]struct{},
) (*service.ModelPlazaMeta, error) {
	meta, err := h.settingService.GetModelPlazaMeta(c.Request.Context())
	if err != nil {
		return emptyUserModelPlazaMeta(), nil
	}
	return filterModelPlazaMeta(meta, visible), nil
}

func groupIDSet(groups []service.Group) map[int64]struct{} {
	set := make(map[int64]struct{}, len(groups))
	for i := range groups {
		set[groups[i].ID] = struct{}{}
	}
	return set
}

// filterPlazaPublicGroups 仅保留 allowed 中的分组并转成公开 DTO。
func filterPlazaPublicGroups(
	groups []service.AvailableGroupRef,
	allowed map[int64]struct{},
) []plazaPublicGroup {
	visible := make([]plazaPublicGroup, 0, len(groups))
	for _, g := range groups {
		if _, ok := allowed[g.ID]; !ok {
			continue
		}
		visible = append(visible, plazaPublicGroup{
			ID:                   g.ID,
			Name:                 g.Name,
			Platform:             g.Platform,
			SubscriptionType:     g.SubscriptionType,
			RateMultiplier:       g.RateMultiplier,
			PeakRateEnabled:      g.PeakRateEnabled,
			PeakStart:            g.PeakStart,
			PeakEnd:              g.PeakEnd,
			PeakRateMultiplier:   g.PeakRateMultiplier,
			IsExclusive:          g.IsExclusive,
			ImageRateIndependent: g.ImageRateIndependent,
			ImageRateMultiplier:  g.ImageRateMultiplier,
			VideoRateIndependent: g.VideoRateIndependent,
			VideoRateMultiplier:  g.VideoRateMultiplier,
		})
	}
	return visible
}

// buildPlazaPublicSections 按可见分组的平台集合切分渠道，平台字母序稳定输出。
func buildPlazaPublicSections(
	ch service.AvailableChannel,
	visibleGroups []plazaPublicGroup,
) []plazaPublicPlatformSection {
	groupsByPlatform := make(map[string][]plazaPublicGroup, 4)
	for _, g := range visibleGroups {
		if g.Platform == "" {
			continue
		}
		groupsByPlatform[g.Platform] = append(groupsByPlatform[g.Platform], g)
	}
	if len(groupsByPlatform) == 0 {
		return nil
	}

	platforms := make([]string, 0, len(groupsByPlatform))
	for p := range groupsByPlatform {
		platforms = append(platforms, p)
	}
	sort.Strings(platforms)

	sections := make([]plazaPublicPlatformSection, 0, len(platforms))
	for _, platform := range platforms {
		platformSet := map[string]struct{}{platform: {}}
		sections = append(sections, plazaPublicPlatformSection{
			Platform:        platform,
			Groups:          groupsByPlatform[platform],
			SupportedModels: toUserSupportedModels(ch.SupportedModels, platformSet),
		})
	}
	return sections
}
