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

const gradeClass = (grade: string | undefined): string => {
  if (!grade) return 'bg-gray-100 text-gray-500 dark:bg-gray-700 dark:text-gray-300'
  if (grade.startsWith('S')) return 'bg-emerald-100 text-emerald-700 dark:bg-emerald-900/35 dark:text-emerald-300'
  if (grade.startsWith('A')) return 'bg-blue-100 text-blue-700 dark:bg-blue-900/35 dark:text-blue-300'
  if (grade.startsWith('B')) return 'bg-amber-100 text-amber-700 dark:bg-amber-900/35 dark:text-amber-300'
  return 'bg-red-100 text-red-700 dark:bg-red-900/35 dark:text-red-300'
}

const scoreLabel = (window: AccountQualityWindow): string => {
  if (window.quality_score == null) return '-'
  return `${window.quality_grade || ''} ${window.quality_score}`.trim()
}

const scoreTitle = (window: AccountQualityWindow): string => {
  if (window.quality_score == null) {
    return t('admin.accounts.quality.insufficientSamples', { count: window.sample_count })
  }
  const base = t('admin.accounts.quality.scoreTitle', {
    score: window.quality_score,
    grade: window.quality_grade || '-',
    count: window.sample_count,
    firstCount: window.first_token_sample_count,
  })
  if (window.score_basis === 'duration_only') {
    return `${base} · ${t('admin.accounts.quality.durationOnly')}`
  }
  if (window.score_basis === 'ttft_only') {
    return `${base} · ${t('admin.accounts.quality.ttftOnly')}`
  }
  return base
}

const QualityRow = defineComponent({
  props: {
    label: { type: String, required: true },
    window: { type: Object as PropType<AccountQualityWindow>, required: true }
  },
  setup(props) {
    return () => h('div', { class: 'flex items-center gap-1 whitespace-nowrap' }, [
      h('span', { class: 'w-9 text-gray-500 dark:text-gray-400' }, `${props.label} `),
      h('span', {
        class: `inline-flex min-w-[3.1rem] justify-center rounded px-1 py-0.5 font-semibold ${gradeClass(props.window.quality_grade)}`,
        'data-quality-grade': props.window.quality_grade || undefined,
        title: scoreTitle(props.window),
      }, scoreLabel(props.window)),
      h('span', { class: 'text-gray-500 dark:text-gray-400' }, `${t('admin.accounts.quality.firstTokenShort')} `),
      h('span', { class: 'font-medium text-gray-700 dark:text-gray-200' }, formatLatency(props.window.average_first_token_ms)),
      h('span', { class: 'text-gray-500 dark:text-gray-400' }, `${t('admin.accounts.quality.totalShort')} `),
      h('span', { class: 'font-medium text-gray-700 dark:text-gray-200' }, formatLatency(props.window.average_duration_ms))
    ])
  }
})
</script>
