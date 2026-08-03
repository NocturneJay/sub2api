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

// TestExtractGeminiMonitorTextSkipsThoughtParts 钉死推理模型响应的文本抽取。
//
// 原实现用 candidates.0.content.parts.0.text 按下标取第一个 part。推理模型会把
// 思考过程也放进 parts、且通常排在最前面并带 "thought": true，于是抽到的是思考
// 文本或空串，challenge 比对必然失败，表现为
// `challenge mismatch (expected N, got "")`。
func TestExtractGeminiMonitorTextSkipsThoughtParts(t *testing.T) {
	resp := []byte(`{"candidates":[{"content":{"parts":[
		{"text":"Let me compute 23 plus 23 step by step...","thought":true},
		{"text":"46"}
	]}}]}`)
	require.Equal(t, "46", extractGeminiMonitorText(resp))
}

func TestExtractGeminiMonitorTextPlainResponse(t *testing.T) {
	// 非推理模型：只有一个普通 text part，行为与旧实现一致。
	resp := []byte(`{"candidates":[{"content":{"parts":[{"text":" 46 "}]}}]}`)
	require.Equal(t, "46", extractGeminiMonitorText(resp))
}

func TestExtractGeminiMonitorTextThoughtOnlyYieldsEmpty(t *testing.T) {
	// 预算被思考耗尽、没有可见文本时必须返回空串，让上层判为 challenge mismatch，
	// 而不是把思考内容当成答案去比对。
	resp := []byte(`{"candidates":[{"content":{"parts":[
		{"text":"thinking...","thought":true}
	]}}]}`)
	require.Empty(t, extractGeminiMonitorText(resp))
}

func TestExtractGeminiMonitorTextMalformed(t *testing.T) {
	require.Empty(t, extractGeminiMonitorText([]byte(`{}`)))
	require.Empty(t, extractGeminiMonitorText([]byte(`{"candidates":[]}`)))
	require.Empty(t, extractGeminiMonitorText([]byte(`not json`)))
}

// TestMonitorChallengeMaxTokensFitsReasoningModels 锁住 token 预算。
// 原值 50 对推理模型不够：2026-08-03 线上 gemini-3.6-flash 实测 out=47 顶满上限，
// 思考阶段就耗尽预算、响应里没有文本部分。
func TestMonitorChallengeMaxTokensFitsReasoningModels(t *testing.T) {
	require.GreaterOrEqual(t, monitorChallengeMaxTokens, 1024,
		"预算必须留够推理模型的思考量，否则响应里不会有可见文本")
}
