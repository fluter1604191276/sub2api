<template>
  <div class="min-w-[10.5rem]">
    <div v-if="loading && !stats" class="space-y-1">
      <div class="h-4 w-36 animate-pulse rounded bg-gray-200 dark:bg-gray-700"></div>
      <div class="h-4 w-32 animate-pulse rounded bg-gray-200 dark:bg-gray-700"></div>
    </div>
    <div v-else-if="error && !stats" class="text-xs text-red-500">{{ error }}</div>
    <div v-else-if="stats" class="space-y-1 text-xs">
      <QualityRow :label="t('admin.accounts.quality.last10')" :window="stats.last_10" />
      <QualityRow :label="t('admin.accounts.quality.last100')" :window="stats.last_100" />
    </div>
    <span v-else class="text-xs text-gray-400">-</span>
  </div>
</template>

<script setup lang="ts">
import { defineComponent, h, type PropType } from 'vue'
import { useI18n } from 'vue-i18n'
import type { AccountQualityStats, AccountQualityWindow } from '@/types'

withDefaults(defineProps<{
  stats?: AccountQualityStats | null
  loading?: boolean
  error?: string | null
}>(), {
  stats: null,
  loading: false,
  error: null
})

const { t } = useI18n()

const formatLatency = (value: number | null): string => {
  if (value == null || !Number.isFinite(value)) return '-'
  if (value < 1000) return `${Math.round(value)}ms`
  const seconds = value / 1000
  return `${seconds < 10 ? seconds.toFixed(1) : Math.round(seconds)}s`
}

const scoreClass = (score: number | null): string => {
  if (score == null) return 'bg-gray-100 text-gray-500 dark:bg-gray-700 dark:text-gray-300'
  if (score >= 85) return 'bg-emerald-100 text-emerald-700 dark:bg-emerald-900/35 dark:text-emerald-300'
  if (score >= 70) return 'bg-blue-100 text-blue-700 dark:bg-blue-900/35 dark:text-blue-300'
  if (score >= 50) return 'bg-amber-100 text-amber-700 dark:bg-amber-900/35 dark:text-amber-300'
  return 'bg-red-100 text-red-700 dark:bg-red-900/35 dark:text-red-300'
}

const QualityRow = defineComponent({
  props: {
    label: { type: String, required: true },
    window: { type: Object as PropType<AccountQualityWindow>, required: true }
  },
  setup(props) {
    return () => h('div', { class: 'flex items-center gap-1 whitespace-nowrap' }, [
      h('span', { class: 'w-9 text-gray-500 dark:text-gray-400' }, props.label),
      h('span', {
        class: `inline-flex min-w-7 justify-center rounded px-1 py-0.5 font-semibold ${scoreClass(props.window.quality_score)}`,
        title: props.window.quality_score == null
          ? t('admin.accounts.quality.insufficientSamples', { count: props.window.sample_count })
          : t('admin.accounts.quality.scoreTitle', { score: props.window.quality_score, count: props.window.sample_count })
      }, props.window.quality_score == null ? '-' : String(props.window.quality_score)),
      h('span', { class: 'text-gray-500 dark:text-gray-400' }, t('admin.accounts.quality.firstTokenShort')),
      h('span', { class: 'font-medium text-gray-700 dark:text-gray-200' }, formatLatency(props.window.average_first_token_ms)),
      h('span', { class: 'text-gray-500 dark:text-gray-400' }, t('admin.accounts.quality.totalShort')),
      h('span', { class: 'font-medium text-gray-700 dark:text-gray-200' }, formatLatency(props.window.average_duration_ms))
    ])
  }
})
</script>
