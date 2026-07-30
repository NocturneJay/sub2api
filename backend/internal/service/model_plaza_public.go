package service

import (
	"context"
	"fmt"
)

// 匿名(未登录)模型广场的可见性内核。
//
// 单独成文件而不是并入 api_key_service.go：后者是上游高频演进的文件，
// 把 aicat 独有逻辑放在自有文件里可以让上游同步时这部分永不冲突。

// GetAnonymousVisibleGroups 返回匿名访客可见的分组集合。
//
// 与登录态的 GetAvailableGroups 是两套独立口径，刻意不复用：
//   - 登录态按「该用户能否绑定该分组」判定，需要 user 记录与有效订阅；
//   - 匿名态没有用户，必须完全不触达 userRepo。传 userID=0 走登录态口径会
//     命中 userRepo.GetByID(0) 直接报错，把公开页打成 500。
//
// 可见性规则（fail-closed，白名单式）：
//   - 专属分组永不可见：它们是小范围授权的，连分组名都不应对匿名暴露；
//     该规则不受任何开关影响。
//   - 订阅型分组默认不可见：高峰倍率只对订阅分组生效（见 PeakMultiplierAt），
//     公开它们等于公开付费套餐的峰值定价策略；由 includeSubscription 显式放开。
//   - 其余活跃分组可见。
//
// 注意：调用方拿到分组集合后仍需按该集合重算可见模型，不能只过滤分组而保留
// 全量模型——否则专属分组独有的模型名仍会泄漏能力面。
func (s *APIKeyService) GetAnonymousVisibleGroups(
	ctx context.Context,
	includeSubscription bool,
) ([]Group, error) {
	allGroups, err := s.groupRepo.ListActive(ctx)
	if err != nil {
		return nil, fmt.Errorf("list active groups: %w", err)
	}

	visible := make([]Group, 0, len(allGroups))
	for i := range allGroups {
		g := allGroups[i]
		if g.IsExclusive {
			continue
		}
		if g.IsSubscriptionType() && !includeSubscription {
			continue
		}
		visible = append(visible, g)
	}
	return visible, nil
}
