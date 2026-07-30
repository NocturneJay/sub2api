/**
 * 公开(可匿名)模型广场聚合接口。
 *
 * 一次请求拿齐广场页需要的三份数据，替代原来并发打
 * /channels/available + /groups/rates + /channels/model-meta 的做法。
 * 未登录可访问（受 model_plaza_public_enabled 开关控制），
 * 带 token 时后端自动返回该用户的可见分组与专属倍率。
 */

import { apiClient } from './client'
import type { ModelPlazaMeta, UserAvailableGroup, UserSupportedModel } from './channels'

/** 渠道条目：刻意不含渠道名称与描述，广场按模型与分组组织，不需要渠道身份。 */
export interface PlazaPublicChannel {
  platforms: PlazaPublicPlatformSection[]
}

export interface PlazaPublicPlatformSection {
  platform: string
  groups: UserAvailableGroup[]
  supported_models: UserSupportedModel[]
}

export interface PlazaPublicResponse {
  channels: PlazaPublicChannel[]
  model_meta: ModelPlazaMeta
  /** 用户专属倍率；匿名恒为空对象。 */
  group_rates: Record<number, number>
  /** 后端判定的登录态，前端据此决定是否展示「登录后可见更多」提示。 */
  authenticated: boolean
}

/**
 * 拉取模型广场聚合数据。
 *
 * skipAuthRedirect：本页可匿名访问，浏览器里存着过期 token 的访客会命中后端
 * OptionalJWT 的严格 401；默认拦截器会清 token 并硬跳 /login，反而让公开页
 * 比全新访客更不可用。这里关掉该行为，由调用方降级为匿名视图。
 */
export async function getPlazaPublicModels(): Promise<PlazaPublicResponse> {
  const { data } = await apiClient.get<PlazaPublicResponse>('/plaza/models', {
    skipAuthRedirect: true,
  })
  return data
}

export default { getPlazaPublicModels }
