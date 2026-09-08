<template>
  <nav
    class="inline-flex max-w-full items-center gap-1 overflow-x-auto rounded-xl border border-gray-200 bg-white p-1 shadow-sm dark:border-dark-700 dark:bg-dark-800/70"
    :aria-label="t('nav.catalog')"
  >
    <RouterLink
      to="/available-channels"
      class="inline-flex min-h-9 shrink-0 items-center gap-2 rounded-lg px-3 py-2 text-sm font-medium transition-colors"
      :class="linkClass('channels')"
      :aria-current="current === 'channels' ? 'page' : undefined"
    >
      <Icon name="server" size="sm" />
      {{ t('nav.availableChannels') }}
    </RouterLink>
    <RouterLink
      :to="{ path: '/model-plaza', query: { embedded: '1' } }"
      class="inline-flex min-h-9 shrink-0 items-center gap-2 rounded-lg px-3 py-2 text-sm font-medium transition-colors"
      :class="linkClass('plaza')"
      :aria-current="current === 'plaza' ? 'page' : undefined"
    >
      <Icon name="grid" size="sm" />
      {{ t('nav.modelPlaza') }}
    </RouterLink>
  </nav>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'

const props = defineProps<{
  current: 'channels' | 'plaza'
}>()

const { t } = useI18n()

function linkClass(surface: 'channels' | 'plaza'): string {
  return props.current === surface
    ? 'bg-gray-100 text-gray-900 dark:bg-dark-700 dark:text-white'
    : 'text-gray-500 hover:bg-gray-50 hover:text-gray-900 dark:text-dark-300 dark:hover:bg-dark-700/70 dark:hover:text-white'
}
</script>
