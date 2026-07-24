<template>
  <div v-if="routes.length > 0" class="space-y-1.5">
    <div class="flex items-center justify-between gap-2 text-xs">
      <span class="font-medium text-gray-500 dark:text-gray-400">
        {{ t('payment.planCard.routePricing') }}
      </span>
      <span class="text-[11px] text-gray-400 dark:text-gray-500">
        {{ t('payment.planCard.routeCount', { count: routes.length }) }}
      </span>
    </div>
    <div class="divide-y divide-gray-200 dark:divide-dark-600">
      <div
        v-for="route in routes"
        :key="`${route.public_model}:${route.endpoint}:${route.target_group_id}`"
        class="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-3 py-1.5 text-xs"
      >
        <div class="min-w-0">
          <div class="break-all font-medium text-gray-700 dark:text-gray-200">
            {{ route.public_model }}<span v-if="route.match_type === 'prefix'">*</span>
          </div>
          <div class="mt-0.5 flex min-w-0 items-center gap-1 text-[11px] text-gray-400 dark:text-gray-500">
            <PlatformIcon :platform="route.target_platform as GroupPlatform" size="xs" />
            <span class="truncate">{{ route.target_group_name }}</span>
            <span v-if="route.endpoint !== 'any'" class="shrink-0">
              {{ t('payment.planCard.endpoint') }}: {{ route.endpoint }}
            </span>
          </div>
        </div>
        <div class="text-right">
          <div class="font-semibold text-gray-800 dark:text-gray-100">
            ×{{ formatRate(route.rate_multiplier) }}
          </div>
          <div class="text-[10px] text-gray-400 dark:text-gray-500">
            {{
              route.rate_source === 'route'
                ? t('payment.planCard.routeOverride')
                : t('payment.planCard.targetGroupRate')
            }}
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import PlatformIcon from '@/components/common/PlatformIcon.vue'
import type { GroupPlatform } from '@/types'
import type { CompositeRoutePricing } from '@/types/payment'

defineProps<{ routes: CompositeRoutePricing[] }>()

const { t } = useI18n()

const formatRate = (rate: number) => Number(rate.toPrecision(10))
</script>
