/**
 * Admin Model Plaza API endpoints
 * 模型广场展示元数据管理:按模型配置可用端点标签,仅影响用户端展示。
 */

import { apiClient } from '../client'
import type { ModelPlazaMeta } from '../channels'

/** 全量渠道聚合出的去重模型条目(编辑页预填用)。 */
export interface PlazaModelRef {
  name: string
  platform: string
}

export async function listPlazaModels(): Promise<PlazaModelRef[]> {
  const { data } = await apiClient.get<PlazaModelRef[]>('/admin/channels/plaza-models')
  return data
}

export async function getModelPlazaMeta(): Promise<ModelPlazaMeta> {
  const { data } = await apiClient.get<ModelPlazaMeta>('/admin/settings/model-plaza-meta')
  return data
}

export async function updateModelPlazaMeta(meta: ModelPlazaMeta): Promise<ModelPlazaMeta> {
  const { data } = await apiClient.put<ModelPlazaMeta>('/admin/settings/model-plaza-meta', meta)
  return data
}

export const adminModelPlazaAPI = {
  listPlazaModels,
  getModelPlazaMeta,
  updateModelPlazaMeta
}

export default adminModelPlazaAPI
