<template>
  <AppLayout>
    <div class="space-y-4">
      <!-- 说明 + 工具栏 -->
      <div class="card space-y-4 p-4 sm:p-5">
        <div class="rounded-lg bg-blue-50 px-4 py-3 text-xs leading-relaxed text-blue-700 dark:bg-blue-900/15 dark:text-blue-300">
          {{ t('admin.modelPlaza.hint') }}
        </div>

        <div class="flex flex-col gap-3 lg:flex-row lg:items-center lg:justify-between">
          <div class="relative w-full sm:w-80">
            <Icon
              name="search"
              size="md"
              class="absolute left-3 top-1/2 -translate-y-1/2 text-gray-400 dark:text-gray-500"
            />
            <input
              v-model="searchQuery"
              type="text"
              :placeholder="t('admin.modelPlaza.searchPlaceholder')"
              class="input pl-10"
            />
          </div>

          <div class="flex flex-wrap items-center gap-2">
            <button
              @click="load"
              :disabled="loading || saving"
              class="btn btn-secondary"
              :title="t('common.refresh', 'Refresh')"
            >
              <Icon name="refresh" size="md" :class="loading ? 'animate-spin' : ''" />
            </button>
            <button
              @click="save"
              :disabled="saving || loading || !dirty"
              class="btn btn-primary"
            >
              {{ saving ? t('common.saving', '...') : t('admin.modelPlaza.save') }}
            </button>
          </div>
        </div>

        <!-- 常用端点快捷模板 -->
        <div>
          <div class="mb-2 text-xs font-medium text-gray-500 dark:text-gray-400">
            {{ t('admin.modelPlaza.presetHint') }}
          </div>
          <div class="flex flex-wrap gap-2">
            <span
              v-for="p in ENDPOINT_PRESETS"
              :key="p"
              class="cursor-default rounded-md bg-gray-100 px-2 py-1 font-mono text-[11px] text-gray-600 dark:bg-dark-700 dark:text-gray-300"
            >
              {{ p }}
            </span>
          </div>
        </div>
      </div>

      <!-- 数量统计 -->
      <div class="flex items-center justify-between text-sm text-gray-500 dark:text-gray-400">
        <span>{{ t('admin.modelPlaza.totalModels', { count: filteredRows.length }) }}</span>
        <span v-if="dirty" class="text-amber-600 dark:text-amber-400">{{ t('admin.modelPlaza.unsaved') }}</span>
      </div>

      <!-- 加载中 -->
      <div v-if="loading" class="flex items-center justify-center py-16">
        <Icon name="refresh" size="lg" class="animate-spin text-gray-400" />
      </div>

      <!-- 编辑表格 -->
      <div v-else class="card overflow-x-auto">
        <table class="w-full border-collapse text-sm">
          <thead>
            <tr class="border-b border-gray-100 bg-gray-50/50 text-xs font-medium uppercase tracking-wide text-gray-500 dark:border-dark-700 dark:bg-dark-800/50 dark:text-gray-400">
              <th class="w-[280px] px-4 py-3 text-left">{{ t('admin.modelPlaza.columns.model') }}</th>
              <th class="w-[140px] px-4 py-3 text-left">{{ t('admin.modelPlaza.columns.platform') }}</th>
              <th class="px-4 py-3 text-left">{{ t('admin.modelPlaza.columns.endpoints') }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-if="filteredRows.length === 0">
              <td colspan="3" class="py-12 text-center">
                <Icon name="inbox" size="xl" class="mx-auto mb-3 h-12 w-12 text-gray-400" />
                <p class="text-sm text-gray-500 dark:text-gray-400">{{ t('admin.modelPlaza.empty') }}</p>
              </td>
            </tr>
            <tr
              v-for="row in filteredRows"
              :key="row.name"
              class="border-b border-gray-100 last:border-b-0 dark:border-dark-700"
            >
              <td class="px-4 py-3 align-middle">
                <span class="font-mono text-sm font-medium text-gray-900 dark:text-white">{{ row.name }}</span>
              </td>
              <td class="px-4 py-3 align-middle">
                <span
                  v-for="p in row.platforms"
                  :key="p"
                  :class="[
                    'mr-1 inline-flex items-center gap-1 rounded-md border px-2 py-0.5 text-[11px] font-medium uppercase',
                    platformBadgeClass(p),
                  ]"
                >
                  <PlatformIcon :platform="p as GroupPlatform" size="xs" />
                  {{ p }}
                </span>
              </td>
              <td class="px-4 py-3 align-middle">
                <input
                  v-model="edits[row.name]"
                  type="text"
                  :placeholder="t('admin.modelPlaza.endpointsPlaceholder')"
                  class="input font-mono text-xs"
                  @input="dirty = true"
                />
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import PlatformIcon from '@/components/common/PlatformIcon.vue'
import adminModelPlazaAPI, { type PlazaModelRef } from '@/api/admin/modelPlaza'
import type { ModelPlazaModelMeta } from '@/api/channels'
import { platformBadgeClass } from '@/utils/platformColors'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'
import type { GroupPlatform } from '@/types'

const { t } = useI18n()
const appStore = useAppStore()

/** 常用端点,展示给管理员照抄用(逗号分隔填进输入框)。 */
const ENDPOINT_PRESETS = [
  '/v1/chat/completions',
  '/v1/responses',
  '/v1/messages',
  '/v1/images/generations',
  '/v1/embeddings',
]

interface PlazaRow {
  name: string
  platforms: string[]
}

const loading = ref(false)
const saving = ref(false)
const dirty = ref(false)
const searchQuery = ref('')
const rows = ref<PlazaRow[]>([])
/** 模型名 → 逗号分隔的端点字符串(编辑态)。 */
const edits = ref<Record<string, string>>({})

async function load() {
  loading.value = true
  try {
    const [models, meta] = await Promise.all([
      adminModelPlazaAPI.listPlazaModels(),
      adminModelPlazaAPI.getModelPlazaMeta(),
    ])

    // 同名模型可能挂在多个平台下:按名字合并成一行,平台并列展示。
    const byName = new Map<string, PlazaRow>()
    for (const m of models as PlazaModelRef[]) {
      const row = byName.get(m.name)
      if (row) {
        if (!row.platforms.includes(m.platform)) row.platforms.push(m.platform)
      } else {
        byName.set(m.name, { name: m.name, platforms: [m.platform] })
      }
    }
    // meta 里已配置、但渠道里已不存在的模型也列出来,方便管理员清理旧配置。
    for (const name of Object.keys(meta.models || {})) {
      if (!byName.has(name)) byName.set(name, { name, platforms: [] })
    }
    rows.value = Array.from(byName.values()).sort((a, b) => a.name.localeCompare(b.name))

    const nextEdits: Record<string, string> = {}
    for (const row of rows.value) {
      nextEdits[row.name] = (meta.models?.[row.name]?.endpoints || []).join(', ')
    }
    edits.value = nextEdits
    dirty.value = false
  } catch (err: unknown) {
    appStore.showError(extractApiErrorMessage(err, t('common.error')))
  } finally {
    loading.value = false
  }
}

onMounted(load)

const filteredRows = computed(() => {
  const q = searchQuery.value.trim().toLowerCase()
  if (!q) return rows.value
  return rows.value.filter(
    (r) => r.name.toLowerCase().includes(q) || (edits.value[r.name] || '').toLowerCase().includes(q),
  )
})

async function save() {
  saving.value = true
  try {
    const models: Record<string, ModelPlazaModelMeta> = {}
    for (const [name, raw] of Object.entries(edits.value)) {
      const endpoints = raw
        .split(/[,，\s]+/)
        .map((s) => s.trim())
        .filter((s) => s.length > 0)
      if (endpoints.length > 0) {
        models[name] = { endpoints }
      }
    }
    await adminModelPlazaAPI.updateModelPlazaMeta({ models })
    appStore.showSuccess(t('admin.modelPlaza.saved'))
    await load()
  } catch (err: unknown) {
    appStore.showError(extractApiErrorMessage(err, t('common.error')))
  } finally {
    saving.value = false
  }
}
</script>
