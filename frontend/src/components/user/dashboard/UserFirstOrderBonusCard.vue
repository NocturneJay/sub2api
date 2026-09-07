<template>
  <div v-if="visible" class="card p-5">
    <div class="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
      <div class="flex min-w-0 items-start gap-4">
        <div class="flex h-12 w-12 flex-shrink-0 items-center justify-center rounded-xl bg-emerald-100 dark:bg-emerald-900/30">
          <Icon name="gift" size="lg" class="text-emerald-600 dark:text-emerald-400" />
        </div>
        <div class="min-w-0">
          <p class="text-base font-semibold text-gray-900 dark:text-white">
            {{ t('dashboard.firstOrderBonus.title') }}
          </p>
          <p class="mt-1 text-sm text-gray-600 dark:text-dark-300">
            {{ t('dashboard.firstOrderBonus.rule', { threshold: formattedThreshold, bonus: formattedBonus }) }}
          </p>
          <p class="mt-1 text-xs text-gray-400 dark:text-dark-500">
            {{ expiryText }} · {{ t('dashboard.firstOrderBonus.belowThresholdHint') }}
          </p>
        </div>
      </div>
      <button class="btn btn-primary w-full sm:w-auto sm:shrink-0" @click="goToPayment">
        {{ t('dashboard.firstOrderBonus.action') }}
      </button>
    </div>
  </div>
</template>

<script setup lang="ts">
/**
 * 仪表盘「首充礼」卡片。
 *
 * 自包含：自己读公开设置开关、自己拉状态、自己决定要不要渲染。
 * 这样 DashboardView 只需要多一行组件标签，不必为一个赠品提示扩散状态与加载逻辑。
 * 只在 status === 'available' 时渲染；开关关着时连请求都不发。
 */
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import userAPI from '@/api/user'
import { useAppStore } from '@/stores/app'
import { formatCurrency, formatDate } from '@/utils/format'
import type { AffiliateFirstOrderBonusStatus } from '@/types/affiliateFirstOrderBonus'

const { t } = useI18n()
const router = useRouter()
const appStore = useAppStore()

const status = ref<AffiliateFirstOrderBonusStatus | null>(null)

// invitee_bonus=0 是管理端明说的合法配置（0 = 不给被邀请人发）。
// 此时整张卡片就是在宣传「额外获得 $0.00」，必须整体不渲染。
const visible = computed(
  () => status.value?.status === 'available' && (status.value?.invitee_bonus ?? 0) > 0
)
const formattedThreshold = computed(() => formatCurrency(status.value?.threshold ?? 0))
const formattedBonus = computed(() => formatCurrency(status.value?.invitee_bonus ?? 0))

// valid_days=0（不过期）时后端会省略 expires_at，换一条「长期有效」文案。
const expiryText = computed(() => {
  const validDays = status.value?.valid_days ?? 0
  const expiresAt = status.value?.expires_at
  if (validDays <= 0 || !expiresAt) {
    return t('dashboard.firstOrderBonus.neverExpires')
  }
  return t('dashboard.firstOrderBonus.expiresAt', {
    date: formatDate(expiresAt, { year: 'numeric', month: '2-digit', day: '2-digit' })
  })
})

function goToPayment(): void {
  // 全仓充值/订阅页的路由是 /purchase（router/index.ts），/payment 只有 qrcode/result 等子路径，
  // 直接 push('/payment') 会落到 404。
  void router.push('/purchase')
}

async function loadStatus(): Promise<void> {
  // 公开设置里的 affiliate_first_order_bonus 是可选字段（旧的注入缓存没有它）。
  if (!appStore.cachedPublicSettings?.affiliate_first_order_bonus?.enabled) {
    return
  }
  try {
    status.value = await userAPI.getAffiliateFirstOrderBonus()
  } catch {
    // 赠品提示失败就当没有，绝不打扰仪表盘主流程。
    status.value = null
  }
}

onMounted(() => {
  void loadStatus()
})
</script>
