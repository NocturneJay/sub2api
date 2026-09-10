package service

import (
	"context"
	"encoding/json"
	"math"
	"strings"
)

// 邀请「首单双向奖励」配置（aicat 自研，2026-09）。
//
// 六个参数收成一个 JSON 键，而不是六个独立的 settings 键：设置链路
// （domain_constants / settings_view / setting_parse / setting_update / dto /
// admin handler ×3 / audit）是全仓与上游合并最热的几个文件，一个键只需要
// 每处插一行，六个键要插六行×十几处。代价是管理端表单绑定一个嵌套对象，
// 这在前端是免费的。
//
// 生产 settings 表里不会自动补写这个键（InitializeDefaultSettings 只在首次
// 初始化时写默认值），所以缺键、空串、坏 JSON、缺字段都必须回落到默认值，
// 而不是零值——否则后台显示 0 而实际按默认执行。
//
// v2（2026-09-10，主人拍板「读法 B」）：邀请人那份不再是达标才发的固定金额，
// 而是好友首单金额的一个比例（默认 50%）、封顶一个金额（默认 10$），且不低于
// 常规比例返利——不看阈值，好友首单不满阈值邀请人照样按比例拿。阈值只管
// 被邀请人那份。旧键 inviter_bonus 读取时当作封顶额兼容。
const (
	// SettingKeyAffiliateFirstOrderBonus 首单双向奖励配置（JSON 对象）。
	SettingKeyAffiliateFirstOrderBonus = "affiliate_first_order_bonus"

	AffiliateFirstOrderThresholdDefault          = 20.0 // 被邀请人奖励的首单美元面额门槛
	AffiliateFirstOrderInviteeBonusDefault       = 10.0 // 被邀请人余额奖励（美元）
	AffiliateFirstOrderInviterRatePercentDefault = 50.0 // 邀请人在好友首单上的总返利率（%）
	AffiliateFirstOrderInviterCapDefault         = 10.0 // 邀请人首单总返利的封顶额（美元）
	AffiliateFirstOrderValidDaysDefault          = 30   // 首充券有效期（天），0 = 不过期
	AffiliateFirstOrderValidDaysMax              = 3650
	// AffiliateFirstOrderAmountMax 阈值与两侧金额的上限，防止后台误填天文数字。
	AffiliateFirstOrderAmountMax = 100000.0
	// AffiliateFirstOrderInviterRatePercentMax 首单返利率的上限（%）。
	AffiliateFirstOrderInviterRatePercentMax = 100.0
)

// AffiliateFirstOrderBonusConfig 是 settings 里 JSON 值的结构，也直接作为
// 管理端 / 公开设置的 JSON 形态（字段名即接口字段名）。
type AffiliateFirstOrderBonusConfig struct {
	Enabled bool `json:"enabled"`
	// Threshold 只约束被邀请人那份：首单不满则被邀请人奖励作废。
	Threshold    float64 `json:"threshold"`
	InviteeBonus float64 `json:"invitee_bonus"`
	// InviterRatePercent 邀请人在好友首单上的总返利率（%），不看阈值；
	// 常规比例返利已发的部分从中扣除，差额为负时不额外发。0 = 不发。
	InviterRatePercent float64 `json:"inviter_rate_percent"`
	// InviterCap 邀请人首单总返利的封顶额（美元）。0 = 不发（不是「不封顶」）。
	InviterCap float64 `json:"inviter_cap"`
	ValidDays  int     `json:"valid_days"`
}

// affiliateFirstOrderBonusConfigJSON 是解析用的影子结构：全部指针，用来区分
// 「字段缺失」（保持默认）与「字段为 0」（合法值）；同时接住 v1 的 inviter_bonus。
type affiliateFirstOrderBonusConfigJSON struct {
	Enabled            *bool    `json:"enabled"`
	Threshold          *float64 `json:"threshold"`
	InviteeBonus       *float64 `json:"invitee_bonus"`
	InviterRatePercent *float64 `json:"inviter_rate_percent"`
	InviterCap         *float64 `json:"inviter_cap"`
	LegacyInviterBonus *float64 `json:"inviter_bonus"`
	ValidDays          *int     `json:"valid_days"`
}

// DefaultAffiliateFirstOrderBonusConfig 返回文档化的默认值（功能默认关闭）。
func DefaultAffiliateFirstOrderBonusConfig() AffiliateFirstOrderBonusConfig {
	return AffiliateFirstOrderBonusConfig{
		Enabled:            false,
		Threshold:          AffiliateFirstOrderThresholdDefault,
		InviteeBonus:       AffiliateFirstOrderInviteeBonusDefault,
		InviterRatePercent: AffiliateFirstOrderInviterRatePercentDefault,
		InviterCap:         AffiliateFirstOrderInviterCapDefault,
		ValidDays:          AffiliateFirstOrderValidDaysDefault,
	}
}

// ParseAffiliateFirstOrderBonusConfig 解析 settings 里的 JSON 值。
// 空串 / 坏 JSON → 全默认；JSON 里缺的字段保持默认；越界字段逐个归正。
// v1 写入的 inviter_bonus 在没有 inviter_cap 时当作封顶额读取，保证升级后
// 后台显示的数字与库里一致。
func ParseAffiliateFirstOrderBonusConfig(raw string) AffiliateFirstOrderBonusConfig {
	cfg := DefaultAffiliateFirstOrderBonusConfig()
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return cfg
	}
	var in affiliateFirstOrderBonusConfigJSON
	if err := json.Unmarshal([]byte(raw), &in); err != nil {
		return DefaultAffiliateFirstOrderBonusConfig()
	}
	if in.Enabled != nil {
		cfg.Enabled = *in.Enabled
	}
	if in.Threshold != nil {
		cfg.Threshold = *in.Threshold
	}
	if in.InviteeBonus != nil {
		cfg.InviteeBonus = *in.InviteeBonus
	}
	if in.InviterRatePercent != nil {
		cfg.InviterRatePercent = *in.InviterRatePercent
	}
	switch {
	case in.InviterCap != nil:
		cfg.InviterCap = *in.InviterCap
	case in.LegacyInviterBonus != nil:
		cfg.InviterCap = *in.LegacyInviterBonus
	}
	if in.ValidDays != nil {
		cfg.ValidDays = *in.ValidDays
	}
	return cfg.Normalized()
}

// Normalized 把非法值逐字段回落到默认并做上限截断，从不报错。
func (c AffiliateFirstOrderBonusConfig) Normalized() AffiliateFirstOrderBonusConfig {
	c.Threshold = normalizeAffiliateFirstOrderAmount(c.Threshold, AffiliateFirstOrderThresholdDefault)
	c.InviteeBonus = normalizeAffiliateFirstOrderAmount(c.InviteeBonus, AffiliateFirstOrderInviteeBonusDefault)
	c.InviterRatePercent = normalizeAffiliateFirstOrderRatePercent(c.InviterRatePercent)
	c.InviterCap = normalizeAffiliateFirstOrderAmount(c.InviterCap, AffiliateFirstOrderInviterCapDefault)
	if c.ValidDays < 0 {
		c.ValidDays = AffiliateFirstOrderValidDaysDefault
	}
	if c.ValidDays > AffiliateFirstOrderValidDaysMax {
		c.ValidDays = AffiliateFirstOrderValidDaysMax
	}
	return c
}

func normalizeAffiliateFirstOrderAmount(v, def float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
		return def
	}
	if v > AffiliateFirstOrderAmountMax {
		return AffiliateFirstOrderAmountMax
	}
	return v
}

// normalizeAffiliateFirstOrderRatePercent 把返利率夹到 [0, 100]；NaN/Inf/负数回落默认。
// 0 是合法值（邀请人那份不发），不回落。
func normalizeAffiliateFirstOrderRatePercent(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
		return AffiliateFirstOrderInviterRatePercentDefault
	}
	if v > AffiliateFirstOrderInviterRatePercentMax {
		return AffiliateFirstOrderInviterRatePercentMax
	}
	return v
}

// MarshalSettingValue 序列化成写进 settings 表的字符串（先归正）。
func (c AffiliateFirstOrderBonusConfig) MarshalSettingValue() string {
	b, err := json.Marshal(c.Normalized())
	if err != nil {
		b, _ = json.Marshal(DefaultAffiliateFirstOrderBonusConfig())
	}
	return string(b)
}

// GetAffiliateFirstOrderBonusConfig 读取首单奖励配置；缺键或读取失败一律返回默认值。
// 注意：这里不判断 affiliate 总开关，调用方用 IsAffiliateFirstOrderBonusActive 判「实际生效」。
func (s *SettingService) GetAffiliateFirstOrderBonusConfig(ctx context.Context) AffiliateFirstOrderBonusConfig {
	if s == nil || s.settingRepo == nil {
		return DefaultAffiliateFirstOrderBonusConfig()
	}
	raw, err := s.settingRepo.GetValue(ctx, SettingKeyAffiliateFirstOrderBonus)
	if err != nil {
		return DefaultAffiliateFirstOrderBonusConfig()
	}
	return ParseAffiliateFirstOrderBonusConfig(raw)
}

// IsAffiliateFirstOrderBonusActive 报告首单奖励是否真正生效：
// 邀请返利总开关与本功能开关必须同时打开。
func (s *SettingService) IsAffiliateFirstOrderBonusActive(ctx context.Context) bool {
	if s == nil {
		return false
	}
	if !s.IsAffiliateEnabled(ctx) {
		return false
	}
	return s.GetAffiliateFirstOrderBonusConfig(ctx).Enabled
}
