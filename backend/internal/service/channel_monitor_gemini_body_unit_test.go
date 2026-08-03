//go:build unit

package service

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestMonitorGeminiBodyIncludesContentRole 钉死 Gemini 渠道监控请求体里的
// contents[].role。
//
// 公开 Gemini API（generativelanguage）单轮请求缺省 role 会默认按 user 处理，
// 所以漏写也测不出来；但 Vertex AI 强制校验该字段，缺失会直接返回
//
//	400 INVALID_ARGUMENT: Please use a valid role: user, model.
//
// 表现是「账号测试通过、渠道监控恒为 error」——因为 account_test_service.go
// 一直有带 role，两条路径的请求体不一致。2026-08-03 线上新建 gemini 分组监控时
// 正是踩到这个，指向 Vertex Service Account 账号的探测全部 400。
func TestMonitorGeminiBodyIncludesContentRole(t *testing.T) {
	adapter, _, ok := providerAdapterFor(MonitorProviderGemini, "")
	require.True(t, ok, "gemini adapter must exist")

	raw, err := adapter.buildBody("gemini-3.6-flash", "ping")
	require.NoError(t, err)

	var body struct {
		Contents []struct {
			Role  string `json:"role"`
			Parts []struct {
				Text string `json:"text"`
			} `json:"parts"`
		} `json:"contents"`
	}
	require.NoError(t, json.Unmarshal(raw, &body))
	require.Len(t, body.Contents, 1)
	require.Equal(t, "user", body.Contents[0].Role,
		"contents[].role 必填：Vertex AI 缺失该字段会返回 400 INVALID_ARGUMENT")
	require.Len(t, body.Contents[0].Parts, 1)
	require.Equal(t, "ping", body.Contents[0].Parts[0].Text)
}

// TestMonitorGeminiBodyMatchesAccountTestShape 保证监控与账号测试用同一种请求形状。
// 两者形状一旦分叉，就会出现「一个通过、另一个失败」而难以定位的情况。
func TestMonitorGeminiBodyMatchesAccountTestShape(t *testing.T) {
	adapter, _, ok := providerAdapterFor(MonitorProviderGemini, "")
	require.True(t, ok)

	raw, err := adapter.buildBody("gemini-3.6-flash", "ping")
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal(raw, &got))

	contents, ok := got["contents"].([]any)
	require.True(t, ok, "contents 必须是数组")
	first, ok := contents[0].(map[string]any)
	require.True(t, ok)
	// 与 account_test_service.go 的 Gemini 文本测试体同构：role + parts[].text
	require.Contains(t, first, "role")
	require.Contains(t, first, "parts")
}
