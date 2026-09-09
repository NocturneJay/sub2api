package handler

// CN 分组 /v1/messages 调度闸门回归（上游 v0.1.178 修复：正常途径创建的 CN 分组曾恒 403）：
// sanitizeGroupMessagesDispatchFields 对非 openai 平台强制 AllowMessagesDispatch=false，
// 故 CN 分组必须与 grok 一样在闸门处豁免，否则原生 Anthropic 直通（Claude Code 主用例）
// 永远不可达。
//
// 与上游同名用例的差别（改写原因，别改回去）：
// 上游把 allowOpenAICompatibleMessagesDispatch / resolveOpenAIMessagesDispatchMappedModel
// 的入参写成 (c *gin.Context, apiKey *APIKey)，读的是**复合父分组**，因此需要额外用
// ensureCompositeTargetPlatform + ResolvedTargetPlatformFromContext 把目标平台猜/查出来。
// aicat 这两个函数收的是**委托解析后的子分组**（见 §3 复合路由设计冲突），复合分组的
// 情形天然由"子分组平台"这一条覆盖，上游那段 ctx 二次查询在这里是死代码
// （其前置条件 Platform == composite 在委托解析后恒为 false）；而且 aicat 的
// ensureCompositeTargetPlatform 是空实现（路由中间件是唯一权威，不按模型名猜平台），
// 上游那两个依赖它的用例在这里根本立不住，故按 aicat 语义重写。

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/xai"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAllowOpenAICompatibleMessagesDispatch_CNProvidersExempt(t *testing.T) {
	require.True(t, allowOpenAICompatibleMessagesDispatch(nil), "无分组保持放行")

	// CN 三家与 grok 同语义：即便 AllowMessagesDispatch=false（sanitize 强制的结果）也必须放行。
	for _, platform := range []string{
		service.PlatformKimi, service.PlatformZhipu, service.PlatformDeepseek, service.PlatformMiniMax, service.PlatformGrok,
	} {
		group := &service.Group{Platform: platform, AllowMessagesDispatch: false}
		require.True(t, allowOpenAICompatibleMessagesDispatch(group),
			"%s 分组必须豁免 allow_messages_dispatch 闸门", platform)
	}

	// 非回归：openai 分组仍受开关控制。
	require.False(t, allowOpenAICompatibleMessagesDispatch(
		&service.Group{Platform: service.PlatformOpenAI, AllowMessagesDispatch: false}))
	require.True(t, allowOpenAICompatibleMessagesDispatch(
		&service.Group{Platform: service.PlatformOpenAI, AllowMessagesDispatch: true}))
}

// 复合分组委托到不同子分组后的闸门语义：等价于上游那个
// TestAllowOpenAICompatibleMessagesDispatch_CompositeResolvedTargets，
// 只是 aicat 直接传解析后的子分组，不经 ctx。
func TestAllowOpenAICompatibleMessagesDispatch_CompositeDelegatedSubgroups(t *testing.T) {
	// 委托到 grok/CN 子分组：与对应独立分组同语义豁免。
	for _, platform := range []string{
		service.PlatformGrok, service.PlatformKimi, service.PlatformZhipu, service.PlatformDeepseek,
	} {
		require.True(t, allowOpenAICompatibleMessagesDispatch(
			&service.Group{Platform: platform, AllowMessagesDispatch: false}), "platform=%s", platform)
	}

	// 委托到 openai 子分组：仍受开关控制。
	require.False(t, allowOpenAICompatibleMessagesDispatch(
		&service.Group{Platform: service.PlatformOpenAI, AllowMessagesDispatch: false}))

	// 未解析出子分组时调用方会先 503，这里再钉一层：复合父分组自身被
	// sanitize 恒置 false，落到闸门只能是拒绝，不得放宽。
	require.False(t, allowOpenAICompatibleMessagesDispatch(
		&service.Group{Platform: service.PlatformComposite, AllowMessagesDispatch: false}))
}

// 委托到 CN 子分组时，Group 级调度映射（gpt-5.x 默认值是 openai 专属）不得注入，
// 模型改写完全交给账号级 model_mapping。
//
// ⚠️ grok 分支依赖**进程级全局** xai.RuntimeModelMappingOptions().EnableCrossClientMap
// （默认 false）。必须显式设置 + t.Cleanup 还原，否则用例的结果取决于同包其它用例
// 有没有先把这个全局打开——本用例最初就漏了 setup，表现为「隔离跑红、整包跑绿」的
// 顺序依赖。同包正确写法见 openai_gateway_handler_test.go 的
// grok_group_maps_claude_cli_model_to_grok_default。
func TestResolveOpenAIMessagesDispatchMappedModel_DelegatedCNSkipsGroupMapping(t *testing.T) {
	// CN 三家与全局开关无关：ResolveMessagesDispatchModel 的 CN 分支直接返回空。
	for _, platform := range []string{
		service.PlatformKimi, service.PlatformZhipu, service.PlatformDeepseek,
	} {
		require.Empty(t,
			resolveOpenAIMessagesDispatchMappedModel(&service.Group{Platform: platform}, "claude-sonnet-4-5-20250929"),
			"platform=%s 不得注入分组级调度映射", platform)
	}

	grok := &service.Group{Platform: service.PlatformGrok}

	t.Run("grok_without_cross_client_map_stays_empty", func(t *testing.T) {
		original := xai.RuntimeModelMappingOptions()
		t.Cleanup(func() { xai.SetRuntimeModelMappingOptions(original) })
		xai.SetRuntimeModelMappingOptions(xai.ModelMappingOptions{EnableCrossClientMap: false})

		require.Empty(t,
			resolveOpenAIMessagesDispatchMappedModel(grok, "claude-sonnet-4-5-20250929"),
			"默认配置下 grok 不做跨客户端映射，应为空")
	})

	t.Run("grok_with_cross_client_map_uses_xai_mapping", func(t *testing.T) {
		original := xai.RuntimeModelMappingOptions()
		t.Cleanup(func() { xai.SetRuntimeModelMappingOptions(original) })
		xai.SetRuntimeModelMappingOptions(xai.ModelMappingOptions{EnableCrossClientMap: true})

		// 开关打开后走 xai 跨客户端映射，而**不是** openai 专属的 gpt-5.x 默认值——
		// 这才是 grok 与 CN 的真正区别所在。
		mapped := resolveOpenAIMessagesDispatchMappedModel(grok, "claude-sonnet-4-5-20250929")
		require.NotEmpty(t, mapped)
		require.NotContains(t, mapped, "gpt-", "grok 不得拿到 openai 专属默认值")
	})
}
