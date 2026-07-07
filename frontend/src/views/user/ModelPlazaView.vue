<template>
  <AppLayout>
    <div class="space-y-4">
      <!-- 筛选区 -->
      <div class="card space-y-4 p-4 sm:p-5">
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
              :placeholder="t('modelPlaza.searchPlaceholder')"
              class="input pl-10"
            />
          </div>

          <div class="flex flex-wrap items-center gap-2">
            <!-- 原价 / 倍率后 切换 -->
            <div
              class="inline-flex items-center rounded-lg border border-gray-200 bg-gray-50 p-0.5 text-xs font-medium dark:border-dark-600 dark:bg-dark-800"
              role="group"
              :aria-label="t('modelPlaza.priceModeLabel')"
            >
              <button
                @click="actualPrice = false"
                :class="segBtnClass(!actualPrice)"
              >
                {{ t('modelPlaza.priceOriginal') }}
              </button>
              <button
                @click="actualPrice = true"
                :class="segBtnClass(actualPrice)"
              >
                {{ t('modelPlaza.priceActual') }}
              </button>
            </div>

            <!-- 卡片 / 列表 视图切换 -->
            <div
              class="inline-flex items-center rounded-lg border border-gray-200 bg-gray-50 p-0.5 dark:border-dark-600 dark:bg-dark-800"
              role="group"
              :aria-label="t('modelPlaza.viewModeLabel')"
            >
              <button
                @click="viewMode = 'card'"
                :class="segBtnClass(viewMode === 'card')"
                :title="t('modelPlaza.viewCards')"
                :aria-label="t('modelPlaza.viewCards')"
              >
                <Icon name="grid" size="sm" />
              </button>
              <button
                @click="viewMode = 'list'"
                :class="segBtnClass(viewMode === 'list')"
                :title="t('modelPlaza.viewList')"
                :aria-label="t('modelPlaza.viewList')"
              >
                <Icon name="menu" size="sm" />
              </button>
            </div>

            <button
              @click="load"
              :disabled="loading"
              class="btn btn-secondary"
              :title="t('common.refresh', 'Refresh')"
            >
              <Icon name="refresh" size="md" :class="loading ? 'animate-spin' : ''" />
            </button>
          </div>
        </div>

        <!-- 供应商筛选 -->
        <div>
          <div class="mb-2 flex items-center gap-1.5 text-xs font-medium text-gray-500 dark:text-gray-400">
            <Icon name="globe" size="xs" class="h-3.5 w-3.5" />
            {{ t('modelPlaza.provider') }}
          </div>
          <div class="flex flex-wrap gap-2">
            <button @click="selectedPlatform = 'all'" :class="chipClass(selectedPlatform === 'all')">
              {{ t('modelPlaza.all') }}
              <span class="text-[11px] opacity-60">{{ models.length }}</span>
            </button>
            <button
              v-for="p in platforms"
              :key="p"
              @click="selectedPlatform = p"
              :class="chipClass(selectedPlatform === p)"
            >
              <PlatformIcon :platform="p as GroupPlatform" size="xs" />
              <span class="uppercase">{{ p }}</span>
              <span class="text-[11px] opacity-60">{{ platformCounts[p] || 0 }}</span>
            </button>
          </div>
        </div>

        <!-- 分组筛选 -->
        <div>
          <div class="mb-2 flex items-center gap-1.5 text-xs font-medium text-gray-500 dark:text-gray-400">
            <Icon name="users" size="xs" class="h-3.5 w-3.5" />
            {{ t('modelPlaza.group') }}
            <span class="font-normal text-gray-400 dark:text-gray-500">{{ t('modelPlaza.groupRateHint') }}</span>
          </div>
          <div class="flex flex-wrap gap-2">
            <button @click="selectedGroupId = 0" :class="chipClass(selectedGroupId === 0)">
              {{ t('modelPlaza.all') }}
            </button>
            <button
              v-for="g in groups"
              :key="g.id"
              @click="selectedGroupId = g.id"
              :class="chipClass(selectedGroupId === g.id)"
              :title="chipTitle(g)"
            >
              <Icon
                v-if="g.is_exclusive"
                name="shield"
                size="xs"
                class="h-3 w-3 text-purple-500 dark:text-purple-400"
              />
              <PlatformIcon :platform="g.platform as GroupPlatform" size="xs" />
              {{ g.name }}
              <span class="rounded bg-black/10 px-1 py-0.5 text-[10px] font-semibold dark:bg-white/10">
                <template v-if="hasCustomRate(g)">
                  <span class="mr-0.5 line-through opacity-50">{{ g.rate_multiplier }}x</span>
                  <span>{{ userGroupRates[g.id] }}x</span>
                </template>
                <template v-else>{{ g.rate_multiplier }}x</template>
              </span>
              <span
                v-if="g.image_rate_independent"
                class="rounded bg-purple-100 px-1 py-0.5 text-[10px] font-semibold text-purple-700 dark:bg-purple-900/30 dark:text-purple-300"
              >
                {{ t('modelPlaza.imageRateBadge', { rate: g.image_rate_multiplier }) }}
              </span>
              <Icon v-if="groupHasPeak(g)" name="clock" size="xs" class="h-3 w-3 text-amber-500" />
              <span class="text-[11px] opacity-60">{{ groupCounts[g.id] || 0 }}</span>
            </button>
          </div>
        </div>
      </div>

      <!-- 数量统计 -->
      <div class="text-sm text-gray-500 dark:text-gray-400">
        {{ t('modelPlaza.totalModels', { count: filteredModels.length }) }}
      </div>

      <!-- 加载中 -->
      <div v-if="loading" class="flex items-center justify-center py-16">
        <Icon name="refresh" size="lg" class="animate-spin text-gray-400" />
      </div>

      <!-- 空状态 -->
      <div v-else-if="filteredModels.length === 0" class="card flex flex-col items-center justify-center py-16">
        <Icon name="inbox" size="xl" class="mb-3 h-12 w-12 text-gray-400" />
        <p class="text-sm text-gray-500 dark:text-gray-400">{{ t('modelPlaza.empty') }}</p>
      </div>

      <!-- 卡片视图 -->
      <div v-else-if="viewMode === 'card'" class="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-3">
        <div
          v-for="m in filteredModels"
          :key="`${m.platform}-${m.name}`"
          class="card flex flex-col p-5"
        >
          <!-- 卡片头 -->
          <div class="mb-1 flex items-start justify-between gap-2">
            <div class="flex min-w-0 items-center gap-2">
              <PlatformIcon :platform="m.platform as GroupPlatform" size="sm" />
              <span class="truncate font-mono text-sm font-semibold text-gray-900 dark:text-white" :title="m.name">
                {{ m.name }}
              </span>
            </div>
            <button
              @click="copyModelName(m.name)"
              class="flex-shrink-0 rounded p-1 text-gray-400 transition-colors hover:bg-gray-100 hover:text-gray-600 dark:hover:bg-dark-700 dark:hover:text-gray-300"
              :aria-label="t('modelPlaza.copyName')"
              :title="t('modelPlaza.copyName')"
            >
              <Icon name="copy" size="sm" class="h-4 w-4" />
            </button>
          </div>
          <div class="mb-3 text-xs uppercase text-gray-400 dark:text-gray-500">{{ m.platform }}</div>

          <!-- 徽章 -->
          <div class="mb-3 flex flex-wrap items-center gap-1.5">
            <span :class="billingBadgeClass(m)">{{ billingLabel(m) }}</span>
            <span
              v-if="isFreeModel(m)"
              class="rounded-md bg-amber-100 px-2 py-0.5 text-[11px] font-medium text-amber-700 dark:bg-amber-900/30 dark:text-amber-300"
            >
              {{ t('modelPlaza.free') }}
            </span>
            <span
              v-if="hasIntervals(m)"
              class="rounded-md bg-purple-100 px-2 py-0.5 text-[11px] font-medium text-purple-700 dark:bg-purple-900/30 dark:text-purple-300"
              :title="intervalsTitle(m)"
            >
              {{ t('modelPlaza.intervals') }}
            </span>
          </div>

          <!-- 可用端点 -->
          <div v-if="endpointsOf(m).length > 0" class="mb-3 flex flex-wrap items-center gap-1.5">
            <span
              v-for="ep in endpointsOf(m)"
              :key="ep"
              class="rounded-md bg-gray-100 px-2 py-0.5 font-mono text-[11px] text-gray-600 dark:bg-dark-700 dark:text-gray-300"
              :title="t('modelPlaza.endpoints')"
            >
              {{ ep }}
            </span>
          </div>

          <!-- 价格区 -->
          <div class="mt-auto">
            <!-- 指定分组:价格明细 -->
            <template v-if="selectedGroupId !== 0">
              <div v-for="line in priceLines(m)" :key="line.group.id" class="space-y-1 text-sm">
                <template v-if="!line.pricing">
                  <div class="text-xs text-gray-400">{{ t('modelPlaza.noPricing') }}</div>
                </template>
                <template v-else-if="line.pricing.billing_mode === BILLING_MODE_TOKEN">
                  <div class="flex justify-between">
                    <span class="text-gray-500 dark:text-gray-400">{{ t('modelPlaza.input') }}</span>
                    <span class="font-medium text-gray-900 dark:text-white">
                      {{ fmtTok(line.pricing.input_price, line.rate) }}<span class="text-xs font-normal text-gray-400"> /1M</span>
                    </span>
                  </div>
                  <div class="flex justify-between">
                    <span class="text-gray-500 dark:text-gray-400">{{ t('modelPlaza.output') }}</span>
                    <span class="font-medium text-gray-900 dark:text-white">
                      {{ fmtTok(line.pricing.output_price, line.rate) }}<span class="text-xs font-normal text-gray-400"> /1M</span>
                    </span>
                  </div>
                  <div
                    v-if="line.pricing.cache_read_price != null || line.pricing.cache_write_price != null"
                    class="pt-0.5 text-[11px] text-gray-400 dark:text-gray-500"
                  >
                    {{ t('modelPlaza.cacheRead') }} {{ fmtTok(line.pricing.cache_read_price, line.rate) }}
                    · {{ t('modelPlaza.cacheWrite') }} {{ fmtTok(line.pricing.cache_write_price, line.rate) }}
                  </div>
                </template>
                <template v-else>
                  <div class="flex justify-between">
                    <span class="text-gray-500 dark:text-gray-400">{{ perUnitLabel(line.pricing) }}</span>
                    <span class="font-medium text-gray-900 dark:text-white">{{ fmtPer(line.pricing, line.rate) }}</span>
                  </div>
                </template>
              </div>
            </template>

            <!-- 全部分组:按分组逐行 -->
            <div v-else class="space-y-1.5">
              <div
                v-for="line in priceLines(m)"
                :key="line.group.id"
                class="flex items-center justify-between gap-2 text-xs"
              >
                <span class="truncate text-gray-500 dark:text-gray-400" :title="line.group.name">
                  {{ line.group.name }}
                </span>
                <span class="flex-shrink-0 font-medium text-gray-900 dark:text-white">
                  <template v-if="!line.pricing">
                    <span class="font-normal text-gray-400">{{ t('modelPlaza.noPricing') }}</span>
                  </template>
                  <template v-else-if="line.pricing.billing_mode === BILLING_MODE_TOKEN">
                    {{ fmtTok(line.pricing.input_price, line.rate) }} / {{ fmtTok(line.pricing.output_price, line.rate) }}
                  </template>
                  <template v-else>{{ fmtPer(line.pricing, line.rate) }}</template>
                </span>
              </div>
              <div class="pt-1 text-[10px] text-gray-400 dark:text-gray-500">
                {{ t('modelPlaza.inOutHint') }}
              </div>
            </div>
          </div>
        </div>
      </div>

      <!-- 列表视图 -->
      <div v-else class="card overflow-x-auto">
        <table class="w-full border-collapse text-sm">
          <thead>
            <tr class="border-b border-gray-100 bg-gray-50/50 text-xs font-medium uppercase tracking-wide text-gray-500 dark:border-dark-700 dark:bg-dark-800/50 dark:text-gray-400">
              <th class="px-4 py-3 text-left">{{ t('modelPlaza.columns.model') }}</th>
              <th class="px-4 py-3 text-left">{{ t('modelPlaza.columns.platform') }}</th>
              <th class="px-4 py-3 text-left">{{ t('modelPlaza.columns.billing') }}</th>
              <th class="px-4 py-3 text-left">{{ t('modelPlaza.columns.input') }}</th>
              <th class="px-4 py-3 text-left">{{ t('modelPlaza.columns.output') }}</th>
              <th class="px-4 py-3 text-left">{{ t('modelPlaza.columns.extra') }}</th>
            </tr>
          </thead>
          <tbody>
            <tr
              v-for="m in filteredModels"
              :key="`${m.platform}-${m.name}`"
              class="border-b border-gray-100 transition-colors last:border-b-0 hover:bg-gray-50/40 dark:border-dark-700 dark:hover:bg-dark-800/40"
            >
              <td class="px-4 py-3">
                <div class="flex items-center gap-1.5">
                  <span class="font-mono text-sm font-medium text-gray-900 dark:text-white">{{ m.name }}</span>
                  <button
                    @click="copyModelName(m.name)"
                    class="rounded p-0.5 text-gray-400 transition-colors hover:text-gray-600 dark:hover:text-gray-300"
                    :aria-label="t('modelPlaza.copyName')"
                    :title="t('modelPlaza.copyName')"
                  >
                    <Icon name="copy" size="xs" class="h-3.5 w-3.5" />
                  </button>
                  <span
                    v-if="isFreeModel(m)"
                    class="rounded bg-amber-100 px-1.5 py-0.5 text-[10px] font-medium text-amber-700 dark:bg-amber-900/30 dark:text-amber-300"
                  >
                    {{ t('modelPlaza.free') }}
                  </span>
                </div>
                <div v-if="endpointsOf(m).length > 0" class="mt-1 flex flex-wrap gap-1">
                  <span
                    v-for="ep in endpointsOf(m)"
                    :key="ep"
                    class="rounded bg-gray-100 px-1.5 py-0.5 font-mono text-[10px] text-gray-500 dark:bg-dark-700 dark:text-gray-400"
                  >
                    {{ ep }}
                  </span>
                </div>
              </td>
              <td class="px-4 py-3">
                <span
                  :class="[
                    'inline-flex items-center gap-1 rounded-md border px-2 py-0.5 text-[11px] font-medium uppercase',
                    platformBadgeClass(m.platform),
                  ]"
                >
                  <PlatformIcon :platform="m.platform as GroupPlatform" size="xs" />
                  {{ m.platform }}
                </span>
              </td>
              <td class="px-4 py-3">
                <span :class="billingBadgeClass(m)">{{ billingLabel(m) }}</span>
              </td>
              <td class="px-4 py-3 align-top">
                <div v-for="line in priceLines(m)" :key="`in-${line.group.id}`" class="whitespace-nowrap py-0.5 text-xs">
                  <span v-if="selectedGroupId === 0" class="text-gray-400 dark:text-gray-500">{{ line.group.name }}: </span>
                  <span class="font-medium text-gray-900 dark:text-white">{{ tableCellIn(line) }}</span>
                </div>
              </td>
              <td class="px-4 py-3 align-top">
                <div v-for="line in priceLines(m)" :key="`out-${line.group.id}`" class="whitespace-nowrap py-0.5 text-xs">
                  <span v-if="selectedGroupId === 0" class="text-gray-400 dark:text-gray-500">{{ line.group.name }}: </span>
                  <span class="font-medium text-gray-900 dark:text-white">{{ tableCellOut(line) }}</span>
                </div>
              </td>
              <td class="px-4 py-3 align-top text-[11px] text-gray-400 dark:text-gray-500">
                <div v-for="line in priceLines(m)" :key="`ex-${line.group.id}`" class="whitespace-nowrap py-0.5">
                  {{ tableCellExtra(line) }}
                </div>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import PlatformIcon from '@/components/common/PlatformIcon.vue'
import userChannelsAPI, {
  type ModelPlazaMeta,
  type UserAvailableGroup,
  type UserPricingInterval,
  type UserSupportedModelPricing,
} from '@/api/channels'
import userGroupsAPI from '@/api/groups'
import { formatScaled } from '@/utils/pricing'
import {
  BILLING_MODE_TOKEN,
  BILLING_MODE_PER_REQUEST,
  BILLING_MODE_IMAGE,
} from '@/constants/channel'
import { platformBadgeClass } from '@/utils/platformColors'
import { hasPeakRate as groupHasPeakRate, formatPeakRateWindow, serverTimezoneLabel } from '@/utils/peak-rate'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'
import type { GroupPlatform } from '@/types'

const { t } = useI18n()
const appStore = useAppStore()

// ── 数据加载 ──────────────────────────────────────────────

/** 模型为中心的透视结构:同一模型在不同分组可能来自不同渠道、定价不同,按分组分别记录。 */
interface PlazaModel {
  name: string
  platform: string
  /** group_id → 该分组下此模型的定价(来自包含该分组的渠道 section)。 */
  groupPricing: Map<number, UserSupportedModelPricing | null>
}

const loading = ref(false)
const groups = ref<UserAvailableGroup[]>([])
const models = ref<PlazaModel[]>([])
const userGroupRates = ref<Record<number, number>>({})
const plazaMeta = ref<ModelPlazaMeta>({ models: {} })

async function load() {
  loading.value = true
  try {
    // 端点元数据失败不阻塞主列表:降级为不显示端点标签。
    const [list, rates, meta] = await Promise.all([
      userChannelsAPI.getAvailable(),
      userGroupsAPI.getUserGroupRates().catch(() => ({}) as Record<number, number>),
      userChannelsAPI.getModelMeta().catch(() => ({ models: {} }) as ModelPlazaMeta),
    ])
    userGroupRates.value = rates
    plazaMeta.value = meta?.models ? meta : { models: {} }

    const groupMap = new Map<number, UserAvailableGroup>()
    const modelMap = new Map<string, PlazaModel>()
    for (const ch of list) {
      for (const section of ch.platforms) {
        for (const g of section.groups) {
          if (!groupMap.has(g.id)) groupMap.set(g.id, g)
        }
        for (const sm of section.supported_models) {
          const platform = sm.platform || section.platform
          const key = `${platform}|${sm.name}`
          let entry = modelMap.get(key)
          if (!entry) {
            entry = { name: sm.name, platform, groupPricing: new Map() }
            modelMap.set(key, entry)
          }
          for (const g of section.groups) {
            if (!entry.groupPricing.has(g.id)) {
              entry.groupPricing.set(g.id, sm.pricing)
            }
          }
        }
      }
    }
    groups.value = Array.from(groupMap.values())
    models.value = Array.from(modelMap.values()).sort((a, b) => a.name.localeCompare(b.name))
  } catch (err: unknown) {
    appStore.showError(extractApiErrorMessage(err, t('common.error')))
  } finally {
    loading.value = false
  }
}

onMounted(load)

// ── 筛选状态 ──────────────────────────────────────────────

const searchQuery = ref('')
const selectedPlatform = ref<string>('all')
const selectedGroupId = ref<number>(0)
const actualPrice = ref(localStorage.getItem('modelPlaza.actualPrice') !== '0')
const viewMode = ref<'card' | 'list'>(localStorage.getItem('modelPlaza.viewMode') === 'list' ? 'list' : 'card')

watch(actualPrice, (v) => localStorage.setItem('modelPlaza.actualPrice', v ? '1' : '0'))
watch(viewMode, (v) => localStorage.setItem('modelPlaza.viewMode', v))

const platforms = computed(() => {
  const seen = new Set<string>()
  for (const m of models.value) seen.add(m.platform)
  return Array.from(seen)
})

const platformCounts = computed(() => {
  const counts: Record<string, number> = {}
  for (const m of models.value) counts[m.platform] = (counts[m.platform] || 0) + 1
  return counts
})

const groupCounts = computed(() => {
  const counts: Record<number, number> = {}
  for (const m of models.value) {
    for (const gid of m.groupPricing.keys()) counts[gid] = (counts[gid] || 0) + 1
  }
  return counts
})

const filteredModels = computed(() => {
  const q = searchQuery.value.trim().toLowerCase()
  return models.value.filter((m) => {
    if (selectedPlatform.value !== 'all' && m.platform !== selectedPlatform.value) return false
    if (q && !m.name.toLowerCase().includes(q)) return false
    // 选中具体分组时只展示该分组实际支持的模型
    if (selectedGroupId.value !== 0 && !m.groupPricing.has(selectedGroupId.value)) return false
    return true
  })
})

// ── 价格计算 ──────────────────────────────────────────────

interface PriceLine {
  group: UserAvailableGroup
  pricing: UserSupportedModelPricing | null
  /** 展示倍率:原价模式恒为 1,倍率后模式取用户专属倍率(有)或分组默认倍率。 */
  rate: number
}

function effectiveRate(g: UserAvailableGroup): number {
  return userGroupRates.value[g.id] ?? g.rate_multiplier
}

/**
 * 展示倍率,与计费侧 resolveImageRateMultiplier 保持一致:
 * 按图计费且分组开启图片独立倍率时,用 image_rate_multiplier
 * (忽略通用倍率与用户专属倍率);其余情况用通用有效倍率。
 * 原价模式恒为 1。
 */
function displayRate(g: UserAvailableGroup, pricing: UserSupportedModelPricing | null): number {
  if (!actualPrice.value) return 1
  if (pricing?.billing_mode === BILLING_MODE_IMAGE && g.image_rate_independent) {
    return g.image_rate_multiplier < 0 ? 0 : g.image_rate_multiplier
  }
  return effectiveRate(g)
}

function priceLines(m: PlazaModel): PriceLine[] {
  if (selectedGroupId.value !== 0) {
    const g = groups.value.find((x) => x.id === selectedGroupId.value)
    if (!g || !m.groupPricing.has(g.id)) return []
    const pricing = m.groupPricing.get(g.id) ?? null
    return [{ group: g, pricing, rate: displayRate(g, pricing) }]
  }
  return groups.value
    .filter((g) => m.groupPricing.has(g.id))
    .map((g) => {
      const pricing = m.groupPricing.get(g.id) ?? null
      return { group: g, pricing, rate: displayRate(g, pricing) }
    })
}

function fmtTok(v: number | null, rate: number): string {
  return formatScaled(v == null ? null : v * rate, 1_000_000)
}

function fmtPer(p: UserSupportedModelPricing, rate: number): string {
  if (p.billing_mode === BILLING_MODE_IMAGE || p.billing_mode === BILLING_MODE_PER_REQUEST) {
    const unit =
      p.billing_mode === BILLING_MODE_IMAGE ? t('modelPlaza.unitPerImage') : t('modelPlaza.unitPerRequest')
    const tier = firstPerRequestInterval(p)
    if (tier) {
      return `${intervalLabel(tier)} ${formatPerUnit(tier.per_request_price, rate)}${unit}`
    }
    return `${formatPerUnit(p.per_request_price, rate)}${unit}`
  }
  return `${formatPerUnit(p.per_request_price, rate)}${t('modelPlaza.unitPerRequest')}`
}

function perUnitLabel(p: UserSupportedModelPricing): string {
  return p.billing_mode === BILLING_MODE_IMAGE ? t('modelPlaza.perImage') : t('modelPlaza.perRequest')
}

function formatPerUnit(v: number | null | undefined, rate: number): string {
  return formatScaled(v == null ? null : v * rate, 1)
}

function firstPerRequestInterval(p: UserSupportedModelPricing): UserPricingInterval | null {
  return p.intervals?.find((iv) => iv.per_request_price != null) ?? null
}

function intervalLabel(iv: UserPricingInterval): string {
  return iv.tier_label || `(${iv.min_tokens}, ${iv.max_tokens == null ? '∞' : iv.max_tokens}]`
}

// ── 展示辅助 ──────────────────────────────────────────────

function endpointsOf(m: PlazaModel): string[] {
  return plazaMeta.value.models[m.name]?.endpoints ?? []
}

function firstPricing(m: PlazaModel): UserSupportedModelPricing | null {
  for (const p of m.groupPricing.values()) {
    if (p) return p
  }
  return null
}

function billingLabel(m: PlazaModel): string {
  const p = firstPricing(m)
  if (!p) return t('modelPlaza.noPricing')
  switch (p.billing_mode) {
    case BILLING_MODE_PER_REQUEST:
      return t('modelPlaza.billingModePerRequest')
    case BILLING_MODE_IMAGE:
      return t('modelPlaza.billingModeImage')
    default:
      return t('modelPlaza.billingModeToken')
  }
}

function billingBadgeClass(m: PlazaModel): string {
  const base = 'rounded-md px-2 py-0.5 text-[11px] font-medium'
  const p = firstPricing(m)
  if (!p) return `${base} bg-gray-100 text-gray-500 dark:bg-dark-700 dark:text-gray-400`
  switch (p.billing_mode) {
    case BILLING_MODE_PER_REQUEST:
      return `${base} bg-blue-100 text-blue-700 dark:bg-blue-900/30 dark:text-blue-300`
    case BILLING_MODE_IMAGE:
      return `${base} bg-purple-100 text-purple-700 dark:bg-purple-900/30 dark:text-purple-300`
    default:
      return `${base} bg-green-100 text-green-700 dark:bg-green-900/30 dark:text-green-300`
  }
}

function isFreeModel(m: PlazaModel): boolean {
  const p = firstPricing(m)
  if (!p) return false
  if (p.billing_mode === BILLING_MODE_TOKEN) {
    return (p.input_price ?? 0) === 0 && (p.output_price ?? 0) === 0
  }
  const prices = [p.per_request_price, ...(p.intervals || []).map((iv) => iv.per_request_price)].filter(
    (v): v is number => v != null,
  )
  return prices.length > 0 && prices.every((v) => v === 0)
}

function hasIntervals(m: PlazaModel): boolean {
  const p = firstPricing(m)
  return Boolean(p?.intervals && p.intervals.length > 0)
}

function intervalsTitle(m: PlazaModel): string {
  const p = firstPricing(m)
  if (!p?.intervals) return ''
  return p.intervals
    .map((iv) => {
      const range = intervalLabel(iv)
      if (p.billing_mode === BILLING_MODE_TOKEN) {
        return `${range}: ${formatScaled(iv.input_price, 1_000_000)} / ${formatScaled(iv.output_price, 1_000_000)}`
      }
      return `${range}: ${formatScaled(iv.per_request_price, 1)}`
    })
    .join('\n')
}

function tableCellIn(line: PriceLine): string {
  if (!line.pricing) return t('modelPlaza.noPricing')
  if (line.pricing.billing_mode === BILLING_MODE_TOKEN) {
    return `${fmtTok(line.pricing.input_price, line.rate)}/1M`
  }
  return fmtPer(line.pricing, line.rate)
}

function tableCellOut(line: PriceLine): string {
  if (!line.pricing) return '-'
  if (line.pricing.billing_mode === BILLING_MODE_TOKEN) {
    return `${fmtTok(line.pricing.output_price, line.rate)}/1M`
  }
  return '-'
}

function tableCellExtra(line: PriceLine): string {
  if (!line.pricing || line.pricing.billing_mode !== BILLING_MODE_TOKEN) return '-'
  if (line.pricing.cache_read_price == null && line.pricing.cache_write_price == null) return '-'
  return `${t('modelPlaza.cacheRead')} ${fmtTok(line.pricing.cache_read_price, line.rate)} · ${t('modelPlaza.cacheWrite')} ${fmtTok(line.pricing.cache_write_price, line.rate)}`
}

// ── 分组芯片辅助 ────────────────────────────────────────

function hasCustomRate(g: UserAvailableGroup): boolean {
  const custom = userGroupRates.value[g.id]
  return custom != null && custom !== g.rate_multiplier
}

function groupHasPeak(g: UserAvailableGroup): boolean {
  return groupHasPeakRate(g)
}

function chipTitle(g: UserAvailableGroup): string {
  const parts: string[] = []
  if (g.is_exclusive) parts.push(t('modelPlaza.exclusiveTooltip'))
  if (g.image_rate_independent) {
    parts.push(t('modelPlaza.imageRateTooltip', { rate: g.image_rate_multiplier }))
  }
  if (groupHasPeak(g)) {
    const window = formatPeakRateWindow(g, serverTimezoneLabel(appStore.cachedPublicSettings?.server_utc_offset))
    parts.push(t('common.peakRateTooltip', { window }))
  }
  return parts.join('\n')
}

// ── 样式辅助 ──────────────────────────────────────────────

function chipClass(active: boolean): string {
  const base =
    'inline-flex items-center gap-1.5 rounded-lg border px-3 py-1.5 text-xs font-medium transition-colors'
  return active
    ? `${base} border-blue-300 bg-blue-50 text-blue-700 dark:border-blue-700 dark:bg-blue-900/20 dark:text-blue-300`
    : `${base} border-gray-200 bg-white text-gray-600 hover:bg-gray-50 dark:border-dark-600 dark:bg-dark-800 dark:text-gray-400 dark:hover:bg-dark-700`
}

function segBtnClass(active: boolean): string {
  const base = 'rounded-md px-2.5 py-1 transition-colors'
  return active
    ? `${base} bg-white text-gray-900 shadow-sm dark:bg-dark-600 dark:text-white`
    : `${base} text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-300`
}

// ── 复制 ──────────────────────────────────────────────────

async function copyModelName(name: string) {
  try {
    await navigator.clipboard.writeText(name)
    appStore.showSuccess(t('modelPlaza.copySuccess'))
  } catch {
    appStore.showError(t('common.error'))
  }
}
</script>
