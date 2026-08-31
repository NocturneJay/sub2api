package handler

import (
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// Route middleware is the only authority allowed to resolve a composite target.
// Handler call sites keep this hook for compatibility, but it must never infer
// a provider from the requested model.
func ensureCompositeTargetPlatform(_ *gin.Context, _ *service.APIKey, _ string) {}

func compositeTargetPlatformAllowed(c *gin.Context, apiKey *service.APIKey, model string, allowed ...string) bool {
	if c == nil || c.Request == nil || apiKey == nil || apiKey.Group == nil || apiKey.Group.Platform != service.PlatformComposite {
		return true
	}
	platform, ok := service.ResolvedTargetPlatformFromContext(c.Request.Context())
	if !ok {
		return false
	}
	for _, allowedPlatform := range allowed {
		if platform == allowedPlatform {
			return true
		}
	}
	return false
}

func compositeTargetPlatformResolved(c *gin.Context, apiKey *service.APIKey, model string) bool {
	if c == nil || c.Request == nil || apiKey == nil || apiKey.Group == nil || apiKey.Group.Platform != service.PlatformComposite {
		return true
	}
	_, ok := service.ResolvedTargetPlatformFromContext(c.Request.Context())
	return ok
}

func effectiveAPIKeyPlatform(c *gin.Context, apiKey *service.APIKey) string {
	if c != nil && c.Request != nil {
		if platform, ok := service.ResolvedTargetPlatformFromContext(c.Request.Context()); ok {
			return platform
		}
	}
	if apiKey == nil || apiKey.Group == nil {
		return ""
	}
	return apiKey.Group.Platform
}

// openAIReasoningEffortPolicyForGroup 取本次请求生效的推理强度上限与映射。
//
// **与上游的差异是设计冲突，同步时勿改回。** 上游这三个函数读的是
// `apiKey.Group`，即复合分组的**父分组**；aicat 自 `38a96eaa6`「复合路由委托
// 到目标分组」起，父分组只是路由壳——定价、调度、计费全部跟委托后的子分组
// 走，推理上限同理：一个可以横跨 OpenAI / Anthropic / Grok 子分组的父分组，
// 挂一个 OpenAI 专用的推理上限没有意义，而子分组才是真正提供算力的那一层。
// 这与「利润管控闸门必须装在委托解析之后」是同一条原则。
//
// 因此这里改为接收**已解析的** requestGroup（调用方用
// `resolveCompositeRequestGroup` 得到，并自行处理解析失败）。非复合分组下两者
// 完全等价：无委托时 `resolveCompositeRequestGroup` 原样返回 `apiKey.Group`。
func openAIReasoningEffortPolicyForGroup(
	c *gin.Context,
	apiKey *service.APIKey,
	requestGroup *service.Group,
) (string, []service.ReasoningEffortMapping, bool) {
	if apiKey == nil || apiKey.Group == nil {
		return "", nil, false
	}
	if apiKey.Group.Platform != service.PlatformOpenAI && apiKey.Group.Platform != service.PlatformComposite {
		return "", nil, false
	}
	if effectiveAPIKeyPlatform(c, apiKey) != service.PlatformOpenAI {
		return "", nil, false
	}
	if requestGroup == nil || requestGroup.Platform != service.PlatformOpenAI {
		return "", nil, false
	}
	return requestGroup.MaxReasoningEffort, requestGroup.ReasoningEffortMappings, true
}

func bindRequestedReasoningEffort(c *gin.Context, body []byte, model string) {
	if c == nil || c.Request == nil {
		return
	}
	effort := service.CanonicalRequestedReasoningEffort(body, model)
	if effort == nil {
		return
	}
	c.Request = c.Request.WithContext(service.WithRequestedReasoningEffort(c.Request.Context(), *effort))
}

func stampOpenAIRequestedReasoningEffort(result *service.OpenAIForwardResult, c *gin.Context) {
	if result == nil || result.RequestedReasoningEffort != nil {
		return
	}
	if c == nil || c.Request == nil {
		return
	}
	result.RequestedReasoningEffort = service.RequestedReasoningEffortFromContext(c.Request.Context())
}

func stampForwardRequestedReasoningEffort(result *service.ForwardResult, requested *string) {
	if result == nil || result.RequestedReasoningEffort != nil {
		return
	}
	result.RequestedReasoningEffort = requested
}

// aicat：上游 v0.1.184 的同名函数是 *ForRequest(c, apiKey) 签名（读复合父分组），
// 此处保持 *ForGroup 收委托后子分组（规约设计冲突第二条）；上游新增的
// requested_reasoning_effort 落库链（bindRequestedReasoningEffort 等三个 helper）
// 原样采纳并在此接入。
func applyOpenAIReasoningEffortPolicyForGroup(
	c *gin.Context,
	apiKey *service.APIKey,
	requestGroup *service.Group,
	body []byte,
) ([]byte, bool) {
	bindRequestedReasoningEffort(c, body, strings.TrimSpace(gjson.GetBytes(body, "model").String()))
	maxEffort, mappings, ok := openAIReasoningEffortPolicyForGroup(c, apiKey, requestGroup)
	if !ok {
		return body, false
	}
	return service.ApplyOpenAIReasoningEffortPolicy(body, maxEffort, mappings)
}

func bindOpenAIReasoningEffortPolicyForMessagesRequest(
	c *gin.Context,
	apiKey *service.APIKey,
	requestGroup *service.Group,
	body []byte,
) {
	if c == nil || c.Request == nil {
		return
	}
	bindRequestedReasoningEffort(c, body, strings.TrimSpace(gjson.GetBytes(body, "model").String()))
	// The Messages bridge synthesizes a default OpenAI effort when
	// output_config.effort is omitted. Bind the group policy only for an
	// explicit client value so the ceiling does not alter that default.
	effort := gjson.GetBytes(body, "output_config.effort")
	if !effort.Exists() || effort.Type != gjson.String || strings.TrimSpace(effort.String()) == "" {
		return
	}
	maxEffort, mappings, ok := openAIReasoningEffortPolicyForGroup(c, apiKey, requestGroup)
	if !ok {
		return
	}
	c.Request = c.Request.WithContext(service.WithOpenAIReasoningEffortPolicy(c.Request.Context(), maxEffort, mappings))
}
