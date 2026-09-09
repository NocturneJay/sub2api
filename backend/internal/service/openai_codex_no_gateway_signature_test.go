package service

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// 回归（aicat 自研，2026-09-08）：网关注入到上游请求里的任何文本、工具名、UA 都不得带
// "sub2api" 字样——那是上游一眼可见的网关签名。以前的 <sub2api-…> 标签已改为用注入文本
// 自身的一句做幂等判定。
func TestCodexInjectedTextsCarryNoGatewaySignature(t *testing.T) {
	for name, text := range map[string]string{
		"image_bridge":   codexImageGenerationBridgeText,
		"spark_notice":   codexSparkImageUnsupportedText,
		"todo_guard":     openAICompatClaudeCodeTodoGuardText,
		"python_alias":   codexPythonToolAlias,
		"canonical_ua":   codexCLIUserAgent,
		"grok_tts_probe": defaultGrokTTSTestText,
	} {
		require.NotContains(t, strings.ToLower(text), "sub2api", name)
	}
	for name, text := range map[string]string{
		"image_bridge": codexImageGenerationBridgeText,
		"spark_notice": codexSparkImageUnsupportedText,
		"todo_guard":   openAICompatClaudeCodeTodoGuardText,
	} {
		require.NotContains(t, text, "<", name+" 不应再带标签")
		require.NotContains(t, text, "\n", name+" 应为单段文本")
	}
}

func TestCodexImageBridgeAndSparkInstructionsIdempotentWithoutMarkers(t *testing.T) {
	reqBody := map[string]any{
		"model":        "gpt-5.4",
		"instructions": "existing instructions",
		"tools":        []any{map[string]any{"type": "image_generation", "output_format": "png"}},
	}
	require.True(t, applyCodexImageGenerationBridgeInstructions(reqBody))
	instructions, _ := reqBody["instructions"].(string)
	require.True(t, strings.HasPrefix(instructions, "existing instructions\n\n"), instructions)
	require.NotContains(t, strings.ToLower(instructions), "sub2api")
	require.False(t, applyCodexImageGenerationBridgeInstructions(reqBody), "第二次不得重复追加")
	require.Equal(t, instructions, reqBody["instructions"])

	spark := map[string]any{"model": "gpt-5.3-codex-spark", "instructions": "existing instructions"}
	require.True(t, applyCodexSparkImageUnsupportedInstructions(spark))
	sparkInstructions, _ := spark["instructions"].(string)
	require.NotContains(t, strings.ToLower(sparkInstructions), "sub2api")
	require.False(t, applyCodexSparkImageUnsupportedInstructions(spark), "第二次不得重复追加")
	require.Equal(t, sparkInstructions, spark["instructions"])
}

func TestOpenAICompatTodoGuardDetectableWithoutMarker(t *testing.T) {
	reqBody := map[string]any{
		"input": []any{
			map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "hi"}}},
		},
	}
	require.True(t, appendOpenAICompatClaudeCodeTodoGuardToRequestBody(reqBody))
	require.True(t, isOpenAICompatMessagesBridgeRequestBody(reqBody), "桥请求体识别必须仍然成立")
	require.False(t, appendOpenAICompatClaudeCodeTodoGuardToRequestBody(reqBody), "第二次不得重复插入")

	raw, err := json.Marshal(reqBody)
	require.NoError(t, err)
	require.NotContains(t, strings.ToLower(string(raw)), "sub2api")
	require.True(t, isOpenAICompatMessagesBridgeBody(raw))
}
