package middleware

import (
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// ChannelMonitorProbe 校验渠道监控探测标记。
//
// 渠道监控通过本站网关发真实请求做健康检查，会在 usage_logs 里留下记录，混在
// 用户真实用量里。管理端「使用记录」需要能把这类记录排除掉，因此探测请求会带一个
// 一次性随机数；本中间件校验并消费它，通过后把标记写进 request context，
// 最终由构造 UsageLog 的位置落到 usage_logs.is_channel_monitor 列上。
//
// 校验失败不拒绝请求——探测标记只影响管理端列表的展示，不涉及鉴权、配额或计费，
// 为它中断一次正常请求是不成比例的。失败时不写标记即可，该请求会以普通记录落库。
//
// 不修改 User-Agent：管理员可以给监控配自定义 UA，而 UA 既可能被上游校验，
// 也参与本站的客户端识别（checkClaudeCodeRestriction）。标记走独立通道，
// 不影响请求的任何既有行为。
//
// 无论校验结果如何都删除随机数请求头，避免它随请求继续转发到上游服务商。
func ChannelMonitorProbe() gin.HandlerFunc {
	return func(c *gin.Context) {
		nonce := c.GetHeader(service.ChannelMonitorProbeHeader)
		if nonce == "" {
			c.Next()
			return
		}
		c.Request.Header.Del(service.ChannelMonitorProbeHeader)
		if service.ConsumeChannelMonitorProbeNonce(nonce) {
			c.Request = c.Request.WithContext(service.WithChannelMonitorProbe(c.Request.Context()))
		}
		c.Next()
	}
}
