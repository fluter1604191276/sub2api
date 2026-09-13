<template>
  <div class="flex justify-between gap-2">
    <span class="text-gray-500 dark:text-gray-400">{{ label }}</span>
    <span class="font-mono">{{ display }}</span>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { formatScaled } from '@/utils/pricing'

const props = withDefaults(
  defineProps<{
    label: string
    value: number | null
    unit: string
    scale: number
    currency?: 'USD' | 'CNY'
  }>(),
  { value: null }
)

const display = computed(() =>
  props.value == null ? '-' : `${formatScaled(props.value, props.scale, 0, props.currency)} ${props.unit}`
)
</script>
