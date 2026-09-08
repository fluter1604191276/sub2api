<template>
  <div class="min-w-[8.5rem] text-xs">
    <div v-if="loading && !quality" class="space-y-1">
      <div class="h-5 w-24 animate-pulse rounded bg-gray-200 dark:bg-gray-700"></div>
      <div class="h-4 w-32 animate-pulse rounded bg-gray-200 dark:bg-gray-700"></div>
    </div>
    <div v-else-if="error && !quality" class="text-red-500">{{ error }}</div>
    <div v-else-if="quality?.score != null" class="space-y-1" :title="scoreTitle">
      <div class="flex items-center gap-1.5 whitespace-nowrap">
        <span
          :class="[
            'inline-flex min-w-[2.25rem] justify-center rounded px-1.5 py-0.5 font-semibold',
            gradeClass(quality.grade),
          ]"
          :data-unified-quality-grade="quality.grade || undefined"
        >
          {{ quality.grade || '-' }}
        </span>
        <span class="font-semibold text-gray-800 dark:text-gray-100">
          {{ quality.score }}<span class="font-normal text-gray-400">/100</span>
        </span>
      </div>
      <div class="flex items-center gap-1 whitespace-nowrap text-[11px] text-gray-500 dark:text-gray-400">
        <span v-if="activity || activityStateOverride" :class="activityTextClass">{{ activityLabel }}</span>
        <span>{{ sourceLabel }}</span>
        <span>{{ confidenceLabel }}</span>
      </div>
    </div>
    <span v-else class="text-gray-400">-</span>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { AccountQualityActivity, AccountUnifiedQuality } from '@/types'

const props = withDefaults(defineProps<{
  quality?: AccountUnifiedQuality | null
  activity?: AccountQualityActivity | null
  activityStateOverride?: 'paused' | 'unassigned' | null
  loading?: boolean
  error?: string | null
}>(), {
  quality: null,
  activity: null,
  activityStateOverride: null,
  loading: false,
  error: null,
})

const { t } = useI18n()

const gradeClass = (grade: string | undefined): string => {
  if (!grade) return 'bg-gray-100 text-gray-500 dark:bg-gray-700 dark:text-gray-300'
  if (grade.startsWith('S')) return 'bg-emerald-100 text-emerald-700 dark:bg-emerald-900/35 dark:text-emerald-300'
  if (grade.startsWith('A')) return 'bg-blue-100 text-blue-700 dark:bg-blue-900/35 dark:text-blue-300'
  if (grade.startsWith('B')) return 'bg-amber-100 text-amber-700 dark:bg-amber-900/35 dark:text-amber-300'
  return 'bg-red-100 text-red-700 dark:bg-red-900/35 dark:text-red-300'
}

const sourceLabel = computed(() => t(`admin.accounts.quality.unified.sources.${props.quality?.source || 'unscored'}`))

const confidencePercent = computed(() => Math.round(Math.max(0, Math.min(1, props.quality?.confidence || 0)) * 100))

const confidenceLabel = computed(() => t('admin.accounts.quality.unified.confidence', {
  percent: confidencePercent.value,
}))

const resolvedActivityState = computed(() => props.activityStateOverride || props.activity?.state || 'idle')

const activityLabel = computed(() => t(`admin.accounts.quality.activity.${resolvedActivityState.value}`))

const activityTextClass = computed(() => {
  if (resolvedActivityState.value === 'active') return 'font-medium text-emerald-600 dark:text-emerald-400'
  if (resolvedActivityState.value === 'failing') return 'font-medium text-red-600 dark:text-red-400'
  if (resolvedActivityState.value === 'degraded' || resolvedActivityState.value === 'low_sample') {
    return 'font-medium text-amber-600 dark:text-amber-400'
  }
  return 'text-gray-400 dark:text-gray-500'
})

const scoreTitle = computed(() => {
  if (!props.quality || props.quality.score == null) return ''
  return t('admin.accounts.quality.unified.title', {
    grade: props.quality.grade || '-',
    score: props.quality.score,
    source: sourceLabel.value,
    confidence: confidencePercent.value,
    count: props.quality.sample_count,
    firstCount: props.quality.first_token_sample_count,
  })
})
</script>
