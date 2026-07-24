<template>
  <div v-if="groupPricing.length > 0" class="space-y-1.5">
    <div class="text-xs font-medium text-gray-500 dark:text-gray-400">
      {{ t('payment.planCard.routePricing') }}
    </div>
    <div class="divide-y divide-gray-200 dark:divide-dark-600">
      <div
        v-for="group in groupPricing"
        :key="group.key"
        :data-target-group-id="group.targetGroupId"
        data-testid="composite-group-pricing-row"
        class="flex items-center justify-between gap-3 py-1.5 text-xs"
      >
        <div class="flex min-w-0 items-center gap-1.5 font-medium text-gray-700 dark:text-gray-200">
          <PlatformIcon :platform="group.targetPlatform as GroupPlatform" size="xs" />
          <span class="truncate">{{ group.targetGroupName }}</span>
        </div>
        <div class="shrink-0 font-semibold text-gray-800 dark:text-gray-100">
          ×{{ formatRate(group.rateMultiplier) }}
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import PlatformIcon from '@/components/common/PlatformIcon.vue'
import type { GroupPlatform } from '@/types'
import type { CompositeRoutePricing } from '@/types/payment'

const props = defineProps<{ routes: CompositeRoutePricing[] }>()

const { t } = useI18n()

const groupPricing = computed(() => {
  const seen = new Set<string>()

  return props.routes.flatMap((route) => {
    const key = `${route.target_group_id}:${route.rate_multiplier}`
    if (seen.has(key)) return []

    seen.add(key)
    return [{
      key,
      targetGroupId: route.target_group_id,
      targetGroupName: route.target_group_name,
      targetPlatform: route.target_platform,
      rateMultiplier: route.rate_multiplier,
    }]
  })
})

const formatRate = (rate: number) => Number(rate.toPrecision(10))
</script>
