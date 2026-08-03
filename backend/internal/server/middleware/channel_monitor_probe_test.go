//go:build unit

package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// runProbeMiddleware 跑一遍中间件，返回 handler 实际看到的探测标记、User-Agent
// 与随机数请求头。断言 handler 侧看到的值，是因为构造 UsageLog 的位置正是从这里
// 拿到的 ctx。
func runProbeMiddleware(t *testing.T, userAgent, probeHeader string) (marked bool, seenUA, seenProbe string) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.Use(ChannelMonitorProbe())
	r.GET("/x", func(c *gin.Context) {
		marked = service.IsChannelMonitorProbe(c.Request.Context())
		seenUA = c.GetHeader("User-Agent")
		seenProbe = c.GetHeader(service.ChannelMonitorProbeHeader)
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	if userAgent != "" {
		req.Header.Set("User-Agent", userAgent)
	}
	if probeHeader != "" {
		req.Header.Set(service.ChannelMonitorProbeHeader, probeHeader)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, "探测标记的校验结果不应影响请求本身")
	return marked, seenUA, seenProbe
}

const monitorUA = "Go-http-client/2.0"

func TestChannelMonitorProbeMarksRequestForValidNonce(t *testing.T) {
	nonce := service.IssueChannelMonitorProbeNonce()
	require.NotEmpty(t, nonce)

	marked, ua, probe := runProbeMiddleware(t, monitorUA, nonce)

	require.True(t, marked, "校验通过必须打上探测标记")
	require.Equal(t, monitorUA, ua, "不得改动 User-Agent：管理员可为监控配置自定义 UA")
	require.Empty(t, probe, "随机数头必须被删除，不得随请求转发到上游服务商")
}

// 本功能的安全底线：没有有效随机数就不能打标记，否则任何持有有效 API 密钥的人
// 都能把自己的用量从管理端列表里藏起来。
func TestChannelMonitorProbeRejectsForgedNonce(t *testing.T) {
	marked, _, probe := runProbeMiddleware(t, monitorUA, "forged-nonce-value")
	require.False(t, marked, "伪造的随机数不得通过")
	require.Empty(t, probe)
}

func TestChannelMonitorProbeRejectsReplayedNonce(t *testing.T) {
	nonce := service.IssueChannelMonitorProbeNonce()
	require.NotEmpty(t, nonce)

	marked, _, _ := runProbeMiddleware(t, monitorUA, nonce)
	require.True(t, marked)

	// 同一个随机数第二次使用必须失效——否则抓到一次探测请求就能长期冒充。
	marked, _, _ = runProbeMiddleware(t, monitorUA, nonce)
	require.False(t, marked, "重放必须失败")
}

func TestChannelMonitorProbeLeavesOrdinaryRequestsUntouched(t *testing.T) {
	const realUA = "Codex Desktop/0.146.0 (Windows 10.0.26200; x86_64)"

	marked, ua, probe := runProbeMiddleware(t, realUA, "")
	require.False(t, marked, "普通请求不得被标记")
	require.Equal(t, realUA, ua, "普通请求的 UA 不得被改动")
	require.Empty(t, probe)

	// 即便普通请求带了随机数头（例如被中间设备注入），也只删头、不标记。
	nonce := service.IssueChannelMonitorProbeNonce()
	marked, ua, probe = runProbeMiddleware(t, realUA, nonce)
	require.Equal(t, realUA, ua)
	require.Empty(t, probe, "随机数头始终不得外泄到上游")
	// 随机数本身有效，因此确实会被标记——标记依据是随机数而非 UA。
	// 这条用例的重点是 UA 不受影响、随机数头不外泄。
	require.True(t, marked)
}
