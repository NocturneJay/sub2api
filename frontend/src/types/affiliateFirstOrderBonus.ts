/**
 * 邀请「首单双向奖励」（affiliate first-order bonus）类型定义。
 *
 * 单独成文件而不是塞进 types/index.ts：index.ts 是全仓最热的合并面之一，
 * 新功能的类型集中在这里可以让上游同步时只解决一处冲突。
 */

/** 公开设置里的首单奖励配置（与后端 service.AffiliateFirstOrderBonusConfig 的 json tag 一一对应）。 */
export interface AffiliateFirstOrderBonusSettings {
  /** 公开设置里的 enabled 已经是 `affiliate_enabled && cfg.Enabled` 的与，前端不必再和总开关做一次与。 */
  enabled: boolean
  /** 首单金额阈值（美元面额，与 payment_orders.amount 同口径）。 */
  threshold: number
  /** 被邀请人可得的余额奖励。 */
  invitee_bonus: number
  /** 邀请人可得的返利额度奖励。 */
  inviter_bonus: number
  /** 券有效期天数，0 = 不过期。 */
  valid_days: number
}

/**
 * 首充券状态枚举。后端判定顺序：记录优先 → 功能开关 → 是否被邀请人 → 是否已有已交付订单 → 是否过期 → available。
 * 用联合类型 + string 兜底：后端将来加状态时前端不会因为窄类型直接编译失败。
 */
export type AffiliateFirstOrderBonusStatusCode =
  | 'available'
  | 'applied'
  | 'void_below_threshold'
  | 'void_expired'
  | 'void_consumed'
  | 'not_invitee'
  | 'disabled'

/** `GET /api/v1/user/aff/first-order-bonus` 的响应体。 */
export interface AffiliateFirstOrderBonusStatus {
  enabled: boolean
  status: AffiliateFirstOrderBonusStatusCode | string
  threshold: number
  invitee_bonus: number
  inviter_bonus: number
  valid_days: number
  /** valid_days=0（不过期）时后端会省略该字段。 */
  expires_at?: string | null
  applied_at?: string | null
  applied_order_id?: number | null
  applied_invitee_bonus?: number | null
}
