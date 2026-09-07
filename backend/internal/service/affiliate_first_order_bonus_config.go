package service

import (
	"context"
	"encoding/json"
	"math"
	"strings"
)

// 邀请「首单双向奖励」配置（aicat 自研，2026-09）。
//
// 五个参数收成一个 JSON 键，而不是五个独立的 settings 键：设置链路
// （domain_constants / settings_view / setting_parse / setting_update / dto /
// admin handler ×3 / audit）是全仓与上游合并最热的几个文件，一个键只需要
// 每处插一行，五个键要插五行×十几处。代价是管理端表单绑定一个嵌套对象，
// 这在前端是免费的。
//
// 生产 settings 表里不会自动补写这个键（InitializeDefaultSettings 只在首次
// 初始化时写默认值），所以缺键、空串、坏 JSON、缺字段都必须回落到默认值，
// 而不是零值——否则后台显示 0 而实际按默认执行。
const (
	// SettingKeyAffiliateFirstOrderBonus 首单双向奖励配置（JSON 对象）。
	SettingKeyAffiliateFirstOrderBonus = "affiliate_first_order_bonus"

	AffiliateFirstOrderThresholdDefault    = 20.0 // 首单美元面额门槛
	AffiliateFirstOrderInviteeBonusDefault = 10.0 // 被邀请人余额奖励（美元）
	AffiliateFirstOrderInviterBonusDefault = 10.0 // 邀请人返利额度奖励（美元）
	AffiliateFirstOrderValidDaysDefault    = 30   // 首充券有效期（天），0 = 不过期
	AffiliateFirstOrderValidDaysMax        = 3650
	// AffiliateFirstOrderAmountMax 阈值与两侧奖励的上限，防止后台误填天文数字。
	AffiliateFirstOrderAmountMax = 100000.0
)

// AffiliateFirstOrderBonusConfig 是 settings 里 JSON 值的结构，也直接作为
// 管理端 / 公开设置的 JSON 形态（字段名即接口字段名）。
type AffiliateFirstOrderBonusConfig struct {
	Enabled      bool    `json:"enabled"`
	Threshold    float64 `json:"threshold"`
	InviteeBonus float64 `json:"invitee_bonus"`
	InviterBonus float64 `json:"inviter_bonus"`
	ValidDays    int     `json:"valid_days"`
}

// DefaultAffiliateFirstOrderBonusConfig 返回文档化的默认值（功能默认关闭）。
func DefaultAffiliateFirstOrderBonusConfig() AffiliateFirstOrderBonusConfig {
	return AffiliateFirstOrderBonusConfig{
		Enabled:      false,
		Threshold:    AffiliateFirstOrderThresholdDefault,
		InviteeBonus: AffiliateFirstOrderInviteeBonusDefault,
		InviterBonus: AffiliateFirstOrderInviterBonusDefault,
		ValidDays:    AffiliateFirstOrderValidDaysDefault,
	}
}

// ParseAffiliateFirstOrderBonusConfig 解析 settings 里的 JSON 值。
// 空串 / 坏 JSON → 全默认；JSON 里缺的字段保持默认；越界字段逐个归正。
func ParseAffiliateFirstOrderBonusConfig(raw string) AffiliateFirstOrderBonusConfig {
	cfg := DefaultAffiliateFirstOrderBonusConfig()
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return cfg
	}
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return DefaultAffiliateFirstOrderBonusConfig()
	}
	return cfg.Normalized()
}

// Normalized 把非法值逐字段回落到默认并做上限截断，从不报错。
func (c AffiliateFirstOrderBonusConfig) Normalized() AffiliateFirstOrderBonusConfig {
	c.Threshold = normalizeAffiliateFirstOrderAmount(c.Threshold, AffiliateFirstOrderThresholdDefault)
	c.InviteeBonus = normalizeAffiliateFirstOrderAmount(c.InviteeBonus, AffiliateFirstOrderInviteeBonusDefault)
	c.InviterBonus = normalizeAffiliateFirstOrderAmount(c.InviterBonus, AffiliateFirstOrderInviterBonusDefault)
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
