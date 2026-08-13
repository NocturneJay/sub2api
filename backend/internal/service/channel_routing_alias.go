package service

import (
	"context"
	"strings"
)

// 渠道级模型映射必须参与账号调度 —— aicat 自研，上游没有这一层。
//
// 上游把渠道映射（channel model_mapping，例如 gpt-5.6-sol -> gpt-5.6-luna）只用在
// 「改写请求体」这一件事上：handler 先 ResolveChannelMappingAndRestrict 拿到映射后的
// 模型名、用它重写 body，然后仍拿**原始**模型名去 SelectAccountWithScheduler* 选号。
// 于是只要账号的 model_mapping 白名单里写的是映射后的名字（正常配置就是这样——账号
// 面向的是上游，不是客户端），调度器就判定「没有账号支持 gpt-5.6-sol」并直接返回
// 404 model_not_found，请求根本到不了上游：usage_logs 里 account_id 与 upstream_model
// 全是 null，body 改写发生在选号之后、压根没有机会执行。
//
// 修法刻意是**加法而不是替换**：把映射后的名字作为「别名」挂在请求 context 上，账号
// 只要支持原始名或别名之一即视为可服务。
//   - 替换式改法（直接把映射后的名字传给调度器）会打断另一种今天能跑通的配置：账号
//     声明的是原始名、由账号级 model_mapping 再映射一次。那种配置现在是好的，不能破坏。
//   - 加法只会放宽候选集，不会收窄，因此不存在「本来能选到的账号选不到了」。
//
// 别名只在选号与「无可用账号」的错因分类阶段消费，全程处在请求 goroutine 内，不跨
// 异步记账边界。渠道限制（checkChannelPricingRestriction）、计费、用量日志一律仍用
// 客户端请求的原始模型名 —— 渠道的定价表与映射表都以客户端请求名为键。
type channelRoutingAliasKey struct{}

// WithChannelRoutingAlias 把渠道映射后的模型名挂进 ctx。未命中映射、别名为空、或
// 别名与原始名相同时原样返回 ctx，不产生额外分配。
func WithChannelRoutingAlias(ctx context.Context, mapping ChannelMappingResult, requestedModel string) context.Context {
	if ctx == nil || !mapping.Mapped {
		return ctx
	}
	alias := strings.TrimSpace(mapping.MappedModel)
	if alias == "" || strings.EqualFold(alias, strings.TrimSpace(requestedModel)) {
		return ctx
	}
	return context.WithValue(ctx, channelRoutingAliasKey{}, alias)
}

// ChannelRoutingAliasFromContext 返回渠道映射后的模型别名，没有则返回空串。
func ChannelRoutingAliasFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	alias, _ := ctx.Value(channelRoutingAliasKey{}).(string)
	return alias
}

// accountSupportsRoutedModel 是选号阶段统一的账号模型支持判定：账号支持客户端请求的
// 原始模型名，或支持渠道映射后的别名，都算可服务。supports 由调用方传入，用于保留各
// 平台自己的归一化逻辑（Anthropic 短 ID 归一、Bedrock model id 解析、Antigravity 映射等）。
func accountSupportsRoutedModel(ctx context.Context, requestedModel string, supports func(string) bool) bool {
	if supports(requestedModel) {
		return true
	}
	alias := ChannelRoutingAliasFromContext(ctx)
	if alias == "" || alias == requestedModel {
		return false
	}
	return supports(alias)
}
