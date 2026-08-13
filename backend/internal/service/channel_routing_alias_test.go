package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// 这些用例钉死「渠道映射必须参与选号」这处 aicat 自研分歧。
//
// 复现的是线上真实故障：分组 40 绑定的渠道把 gpt-5.6-sol 映射到 gpt-5.6-luna，
// 两个账号的 model_mapping 里只声明了 gpt-5.6-luna（账号面向上游，本就该这么配）。
// 上游实现只用映射改写 body、选号仍用原始名，于是调度器判定「无账号支持 sol」并
// 返回 404，请求根本到不了上游：usage_logs 的 account_id 与 upstream_model 全为 null。
//
// 把 channel_routing_alias.go 的别名判定去掉，本文件必然变红。

func channelAliasTestAccount(t *testing.T, mappedModels ...string) *Account {
	t.Helper()
	mapping := make(map[string]any, len(mappedModels))
	for _, m := range mappedModels {
		mapping[m] = m
	}
	return &Account{
		ID:          9001,
		Name:        "channel-alias-account",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{"model_mapping": mapping},
	}
}

func aliasCtx(t *testing.T, requested, mapped string) context.Context {
	t.Helper()
	return WithChannelRoutingAlias(
		context.Background(),
		ChannelMappingResult{Mapped: true, MappedModel: mapped},
		requested,
	)
}

func TestWithChannelRoutingAlias_OnlyStoresARealAlias(t *testing.T) {
	base := context.Background()

	// 未命中映射：不写入。
	ctx := WithChannelRoutingAlias(base, ChannelMappingResult{MappedModel: "gpt-5.6-sol"}, "gpt-5.6-sol")
	require.Empty(t, ChannelRoutingAliasFromContext(ctx))

	// 命中但映射到同名（大小写不同也算同名）：不写入，避免白占一次 context 分配。
	ctx = WithChannelRoutingAlias(base, ChannelMappingResult{Mapped: true, MappedModel: "GPT-5.6-Sol"}, "gpt-5.6-sol")
	require.Empty(t, ChannelRoutingAliasFromContext(ctx))

	// 命中且确实换名：写入。
	ctx = WithChannelRoutingAlias(base, ChannelMappingResult{Mapped: true, MappedModel: " gpt-5.6-luna "}, "gpt-5.6-sol")
	require.Equal(t, "gpt-5.6-luna", ChannelRoutingAliasFromContext(ctx))
}

func TestAccountSupportsRoutedModel_AliasWidensNeverNarrows(t *testing.T) {
	// 账号只声明映射后的名字 —— 这就是线上分组 40 的配置，也是修复前必然 404 的那种。
	onlyMapped := channelAliasTestAccount(t, "gpt-5.6-luna")
	require.False(t, onlyMapped.IsModelSupported("gpt-5.6-sol"),
		"前置条件：账号本身不认原始名，否则这个用例证明不了什么")

	require.False(t, accountSupportsRoutedModel(context.Background(), "gpt-5.6-sol", onlyMapped.IsModelSupported),
		"没有渠道映射时不应凭空放行")
	require.True(t, accountSupportsRoutedModel(aliasCtx(t, "gpt-5.6-sol", "gpt-5.6-luna"), "gpt-5.6-sol", onlyMapped.IsModelSupported),
		"渠道已把 sol 映射到 luna，账号支持 luna，就必须可服务")

	// 账号只声明原始名 —— 这种配置修复前是能跑通的（选号用原始名、body 改写成映射名，
	// 再由账号级 mapping 兜住）。别名是加法，不能把它打断。
	onlyOriginal := channelAliasTestAccount(t, "gpt-5.6-sol")
	require.True(t, accountSupportsRoutedModel(aliasCtx(t, "gpt-5.6-sol", "gpt-5.6-luna"), "gpt-5.6-sol", onlyOriginal.IsModelSupported),
		"加了别名不能反过来让原本可用的账号落选")

	// 两个名字都不支持的账号仍然要被排除，别名不是万能放行。
	unrelated := channelAliasTestAccount(t, "claude-sonnet-4-5")
	require.False(t, accountSupportsRoutedModel(aliasCtx(t, "gpt-5.6-sol", "gpt-5.6-luna"), "gpt-5.6-sol", unrelated.IsModelSupported))
}

// TestOpenAICompatibleEligibility_HonorsChannelAlias 走的是真正的选号过滤函数，
// 不是 helper 本身 —— 修复点接错地方这个用例照样会红。
func TestOpenAICompatibleEligibility_HonorsChannelAlias(t *testing.T) {
	account := channelAliasTestAccount(t, "gpt-5.6-luna")

	require.False(t, isOpenAICompatibleAccountEligibleForRequestBeforeProfit(
		context.Background(), account, PlatformOpenAI, "gpt-5.6-sol", false, "",
	), "无渠道别名时，账号只声明 luna，请求 sol 必然落选（这正是线上 404 的成因）")

	require.True(t, isOpenAICompatibleAccountEligibleForRequestBeforeProfit(
		aliasCtx(t, "gpt-5.6-sol", "gpt-5.6-luna"), account, PlatformOpenAI, "gpt-5.6-sol", false, "",
	), "带上渠道别名后必须选得中")
}

// TestOpenAIAccountSchedulerFilter_HonorsChannelAlias 覆盖高级调度器的候选过滤，
// 它与上面那个 load-aware 过滤是两条独立代码路径，必须各钉一次。
func TestOpenAIAccountSchedulerFilter_HonorsChannelAlias(t *testing.T) {
	scheduler := &defaultOpenAIAccountScheduler{}
	account := channelAliasTestAccount(t, "gpt-5.6-luna")
	req := OpenAIAccountScheduleRequest{
		Platform:       PlatformOpenAI,
		RequestedModel: "gpt-5.6-sol",
	}

	_, reason := scheduler.isAccountRequestCompatibleReason(context.Background(), account, req)
	require.Equal(t, "model_not_supported", reason,
		"无别名时应当明确以 model_not_supported 落选")

	compatible, reason := scheduler.isAccountRequestCompatibleReason(
		aliasCtx(t, "gpt-5.6-sol", "gpt-5.6-luna"), account, req)
	require.True(t, compatible, "带别名后不应再以 %q 落选", reason)
}
