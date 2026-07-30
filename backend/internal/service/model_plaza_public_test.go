//go:build unit

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// plazaPublicGroupRepoStub 只实现 ListActive；其余方法由嵌入接口占位，
// 一旦被意外调用会 nil-deref panic，从而暴露「匿名路径多查了东西」的回归。
type plazaPublicGroupRepoStub struct {
	GroupRepository
	groups []Group
	err    error
	calls  int
}

func (s *plazaPublicGroupRepoStub) ListActive(context.Context) ([]Group, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	return s.groups, nil
}

func anonymousPlazaGroups() []Group {
	return []Group{
		{ID: 1, Name: "public-standard", Platform: "anthropic"},
		{ID: 2, Name: "exclusive", Platform: "anthropic", IsExclusive: true},
		{ID: 3, Name: "subscription", Platform: "openai", SubscriptionType: SubscriptionTypeSubscription},
		{ID: 4, Name: "exclusive-subscription", Platform: "openai", IsExclusive: true, SubscriptionType: SubscriptionTypeSubscription},
		{ID: 5, Name: "public-standard-2", Platform: "gemini"},
	}
}

func visibleGroupIDs(groups []Group) []int64 {
	ids := make([]int64, 0, len(groups))
	for i := range groups {
		ids = append(ids, groups[i].ID)
	}
	return ids
}

func TestGetAnonymousVisibleGroups_ExcludesExclusiveAndSubscriptionByDefault(t *testing.T) {
	repo := &plazaPublicGroupRepoStub{groups: anonymousPlazaGroups()}
	// userRepo 故意留 nil：匿名路径一旦触达用户仓储就会 panic，这正是我们要防的回归
	// （传 userID=0 走登录态口径会命中 userRepo.GetByID(0)，把公开页打成 500）。
	svc := &APIKeyService{groupRepo: repo}

	got, err := svc.GetAnonymousVisibleGroups(context.Background(), false)

	require.NoError(t, err)
	require.ElementsMatch(t, []int64{1, 5}, visibleGroupIDs(got))
	require.Equal(t, 1, repo.calls)
}

func TestGetAnonymousVisibleGroups_IncludeSubscriptionStillExcludesExclusive(t *testing.T) {
	repo := &plazaPublicGroupRepoStub{groups: anonymousPlazaGroups()}
	svc := &APIKeyService{groupRepo: repo}

	got, err := svc.GetAnonymousVisibleGroups(context.Background(), true)

	require.NoError(t, err)
	// 订阅分组放开后 3 可见；但 2 与 4 是专属分组，任何开关都不得放行。
	require.ElementsMatch(t, []int64{1, 3, 5}, visibleGroupIDs(got))
	for _, g := range got {
		require.False(t, g.IsExclusive, "exclusive group %d leaked to anonymous view", g.ID)
	}
}

func TestGetAnonymousVisibleGroups_EmptyWhenAllRestricted(t *testing.T) {
	repo := &plazaPublicGroupRepoStub{groups: []Group{
		{ID: 1, Name: "exclusive", IsExclusive: true},
		{ID: 2, Name: "subscription", SubscriptionType: SubscriptionTypeSubscription},
	}}
	svc := &APIKeyService{groupRepo: repo}

	got, err := svc.GetAnonymousVisibleGroups(context.Background(), false)

	require.NoError(t, err)
	require.Empty(t, got)
}

func TestGetAnonymousVisibleGroups_RepoErrorPropagates(t *testing.T) {
	// 仓储报错必须冒泡，由调用方 fail-closed，不能静默降级成空集合后照常渲染。
	repo := &plazaPublicGroupRepoStub{err: errors.New("db down")}
	svc := &APIKeyService{groupRepo: repo}

	got, err := svc.GetAnonymousVisibleGroups(context.Background(), false)

	require.Error(t, err)
	require.Nil(t, got)
}
