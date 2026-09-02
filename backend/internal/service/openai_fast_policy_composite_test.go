//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// aicat：复合分组下「强制 Fast」跟委托后的子分组走，父分组只是路由壳
// （规约设计冲突登记；与免费 Fast 计费、推理强度上限同一原则）。
// 上游口径读 ctx 里的分组（复合请求下是父分组），这里断言的正是被改掉的行为。
func TestApplyOpenAIFastPolicyToBody_CompositeForceFollowsDelegatedGroup(t *testing.T) {
	body := []byte(`{"model":"gpt-5.4","input":"hi"}`)
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	parentOn := &Group{ID: 7, Platform: PlatformComposite, Status: StatusActive, Hydrated: true, ForceOpenAIFast: true}
	parentOff := &Group{ID: 7, Platform: PlatformComposite, Status: StatusActive, Hydrated: true}

	svc := newOpenAIGatewayServiceWithSettings(t, DefaultOpenAIFastPolicySettings())

	// 父分组开了强制 Fast、委托的子分组没开：不生效。
	svc.groupRepo = &groupRepoStubForAdmin{getByIDByID: map[int64]*Group{
		42: {ID: 42, Platform: PlatformOpenAI, Status: StatusActive},
	}}
	ctx := WithResolvedPricingGroupID(context.WithValue(context.Background(), ctxkey.Group, parentOn), 42)
	updated, err := svc.applyOpenAIFastPolicyToBody(ctx, account, "gpt-5.4", body)
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(updated, "service_tier").Exists(), "父分组的强制 Fast 不得越过子分组")

	// 子分组开了强制 Fast、父分组没开：生效。
	svc.groupRepo = &groupRepoStubForAdmin{getByIDByID: map[int64]*Group{
		42: {ID: 42, Platform: PlatformOpenAI, Status: StatusActive, ForceOpenAIFast: true},
	}}
	ctx = WithResolvedPricingGroupID(context.WithValue(context.Background(), ctxkey.Group, parentOff), 42)
	updated, err = svc.applyOpenAIFastPolicyToBody(ctx, account, "gpt-5.4", body)
	require.NoError(t, err)
	require.Equal(t, OpenAIFastTierPriority, gjson.GetBytes(updated, "service_tier").String())

	// 无委托信息时复合父分组也不得凭自己的开关强制（aicat 复合请求必有委托，此为兜底）。
	svc.groupRepo = nil
	ctx = context.WithValue(context.Background(), ctxkey.Group, parentOn)
	updated, err = svc.applyOpenAIFastPolicyToBody(ctx, account, "gpt-5.4", body)
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(updated, "service_tier").Exists())

	// 非复合分组：无委托，退回 ctx 分组，与上游等价。
	plain := &Group{ID: 9, Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true, ForceOpenAIFast: true}
	ctx = context.WithValue(context.Background(), ctxkey.Group, plain)
	updated, err = svc.applyOpenAIFastPolicyToBody(ctx, account, "gpt-5.4", body)
	require.NoError(t, err)
	require.Equal(t, OpenAIFastTierPriority, gjson.GetBytes(updated, "service_tier").String())
}
