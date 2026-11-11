<template>
  <main class="min-h-screen bg-gray-50 px-4 py-8 text-gray-900 dark:bg-dark-950 dark:text-white sm:px-6">
    <section class="mx-auto max-w-4xl">
      <header class="mb-6 flex flex-wrap items-center justify-between gap-3">
        <div>
          <h1 class="text-2xl font-bold">{{ t('channelMonitorV3.title') }}</h1>
          <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('channelMonitorV3.description') }}</p>
        </div>
        <button type="button" class="btn btn-secondary" :disabled="loading" @click="load" data-testid="refresh">
          {{ t('channelMonitorV3.refresh') }}
        </button>
      </header>

      <p v-if="loading" class="text-sm text-gray-500" data-testid="loading">{{ t('channelMonitorV3.loading') }}</p>
      <section v-else-if="error" class="rounded-2xl border border-red-200 bg-red-50 p-4 text-sm text-red-700 dark:border-red-900 dark:bg-red-950/30 dark:text-red-300" data-testid="error">
        {{ t('channelMonitorV3.loadError') }}
      </section>
      <section v-else-if="groups.length === 0" class="rounded-2xl bg-white p-8 text-center text-sm text-gray-500 shadow-sm ring-1 ring-gray-900/5 dark:bg-dark-800 dark:ring-dark-700" data-testid="empty">
        {{ t('channelMonitorV3.empty') }}
      </section>
      <section v-else class="overflow-x-auto rounded-2xl bg-white shadow-sm ring-1 ring-gray-900/5 dark:bg-dark-800 dark:ring-dark-700">
        <table class="min-w-full text-left text-sm">
          <thead class="border-b border-gray-100 text-xs uppercase tracking-wide text-gray-500 dark:border-dark-700 dark:text-gray-400">
            <tr>
              <th class="px-5 py-3">{{ t('channelMonitorV3.group') }}</th>
              <th class="px-5 py-3">{{ t('channelMonitorV3.last10') }}</th>
              <th class="px-5 py-3">{{ t('channelMonitorV3.last100') }}</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
            <tr v-for="group in groups" :key="group.group_id" data-testid="group-row">
              <td class="px-5 py-4 font-medium text-gray-900 dark:text-white">{{ group.name }}</td>
              <td class="px-5 py-4"><span class="inline-flex items-center gap-2" :aria-label="gradeAria(group.last_10, t('channelMonitorV3.last10'))"><strong class="font-semibold">{{ displayGrade(group.last_10) }}</strong><span class="text-xs text-gray-500 dark:text-gray-400">{{ t('channelMonitorV3.samples', { count: group.last_10.sample_count }) }}</span></span></td>
              <td class="px-5 py-4"><span class="inline-flex items-center gap-2" :aria-label="gradeAria(group.last_100, t('channelMonitorV3.last100'))"><strong class="font-semibold">{{ displayGrade(group.last_100) }}</strong><span class="text-xs text-gray-500 dark:text-gray-400">{{ t('channelMonitorV3.samples', { count: group.last_100.sample_count }) }}</span></span></td>
            </tr>
          </tbody>
        </table>
      </section>
    </section>
  </main>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { getPublicChannelMonitorV3, type ChannelMonitorV3Grade, type PublicChannelMonitorV3Group } from '@/api/channelMonitorV3'

const { t } = useI18n()
const groups = ref<PublicChannelMonitorV3Group[]>([])
const loading = ref(true)
const error = ref(false)

function displayGrade(grade: ChannelMonitorV3Grade): string {
  return grade.grade || t('channelMonitorV3.noData')
}

function gradeAria(grade: ChannelMonitorV3Grade, windowLabel: string): string {
  return `${windowLabel}: ${displayGrade(grade)}, ${t('channelMonitorV3.samples', { count: grade.sample_count })}`
}

async function load() {
  loading.value = true
  error.value = false
  try {
    const response = await getPublicChannelMonitorV3()
    groups.value = response.groups
  } catch {
    error.value = true
  } finally {
    loading.value = false
  }
}

onMounted(load)
</script>
