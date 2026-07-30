package routes

import (
	"time"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/middleware"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

// plazaPublicRPM 公开广场的每 IP 每分钟请求上限。
// 取值偏宽:正常用户打开页面只发 1 次请求,这道限流是为了兜住脚本化抓取,
// 不是为了精确配额。真正压住 DB 放大面的是 handler 里的匿名视图 TTL 缓存。
const plazaPublicRPM = 60

// RegisterPlazaPublicRoutes 注册公开(可匿名)模型广场路由。
//
// 路径刻意使用 /plaza/models 而不是 /model-plaza:上游自带的广场端点占用了
// /api/v1/model-plaza,分开命名可以让两者未来并存而不产生同前缀双 group。
//
// 中间件顺序不可调换:
//  1. 限流按 IP 计数,必须在鉴权之前,否则匿名洪水会先打到 JWT 解析;
//     fail-open —— 反代未正确透传真实 IP 时宁可放行也不要误伤真实用户。
//  2. OptionalJWT 匿名放行、带 token 则严格校验并写入 role。
//  3. BackendModeUserGuard 依赖上一步写入的 role:后台模式下匿名无 role,
//     判为非管理员直接 403,与前端路由守卫口径一致。
//
// 开关本身在 handler 内 fail-closed 判定(关闭返回 404),不在这里拦截:
// 路由注册期读设置会把启动顺序和设置存储耦合起来。
func RegisterPlazaPublicRoutes(
	v1 *gin.RouterGroup,
	h *handler.Handlers,
	optionalJWT servermiddleware.OptionalJWTAuthMiddleware,
	settingService *service.SettingService,
	redisClient *redis.Client,
) {
	if h == nil || h.PlazaPublic == nil {
		return
	}

	rateLimiter := middleware.NewRateLimiter(redisClient)

	plaza := v1.Group("/plaza")
	plaza.Use(rateLimiter.LimitWithOptions("plaza-public", plazaPublicRPM, time.Minute, middleware.RateLimitOptions{
		FailureMode: middleware.RateLimitFailOpen,
	}))
	plaza.Use(gin.HandlerFunc(optionalJWT))
	plaza.Use(servermiddleware.BackendModeUserGuard(settingService))
	{
		plaza.GET("/models", h.PlazaPublic.Get)
	}
}
