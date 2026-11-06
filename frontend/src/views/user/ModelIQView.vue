<template>
  <AppLayout>
    <div class="mx-auto w-full max-w-[1600px] space-y-6 pb-8">
      <header
        class="rounded-2xl bg-white p-5 shadow-sm ring-1 ring-gray-900/5 dark:bg-dark-800 dark:ring-dark-700 sm:p-6"
      >
        <div class="flex flex-wrap items-start justify-between gap-5">
          <div class="min-w-0 max-w-2xl">
            <h1 class="text-xl font-black text-gray-900 dark:text-white">
              {{ t('modelIQ.title') }}
            </h1>
            <p class="mt-2 text-sm text-gray-500 dark:text-gray-400">
              {{ t('modelIQ.description') }}
            </p>
          </div>
          <div class="flex flex-wrap items-center justify-end gap-2">
            <div
              v-if="data"
              class="grid grid-cols-2 gap-2 sm:grid-cols-4"
            >
              <div
                v-for="item in summaryCards"
                :key="item.key"
                class="min-w-20 rounded-xl bg-gray-50 px-3 py-2 text-center dark:bg-dark-900/50"
              >
                <p class="text-xs text-gray-500 dark:text-gray-400">
                  {{ item.label }}
                </p>
                <p
                  class="mt-1 text-lg font-bold"
                  :class="item.className"
                >
                  {{ item.value }}
                </p>
              </div>
            </div>
            <button
              type="button"
              class="btn btn-secondary btn-sm"
              :disabled="loading"
              @click="load"
            >
              {{ t('common.refresh') }}
            </button>
          </div>
        </div>
        <div
          v-if="data"
          class="mt-5 flex flex-wrap items-center gap-x-4 gap-y-2 border-t border-gray-100 pt-4 text-sm text-gray-500 dark:border-dark-700 dark:text-gray-400"
        >
          <span
            class="rounded-full px-3 py-1 font-medium"
            :class="data.enabled ? 'bg-emerald-50 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300' : 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-gray-300'"
          >
            {{ data.enabled ? t('common.enabled') : t('common.disabled') }}
          </span>
          <span>{{ t('modelIQ.model', { model: data.model }) }}</span>
          <span>{{ t('modelIQ.interval', { seconds: data.interval_seconds }) }}</span>
        </div>
      </header>
      <div
        v-if="loading"
        class="rounded-2xl bg-white px-6 py-16 text-center text-gray-500 shadow-sm dark:bg-dark-800"
      >
        {{ t('common.loading') }}
      </div>
      <div
        v-else-if="loadError"
        class="rounded-2xl border border-red-200 bg-red-50 px-6 py-10 text-center text-red-800 dark:border-red-900/50 dark:bg-red-950/30 dark:text-red-200"
      >
        <p>{{ loadError }}</p>
        <button
          type="button"
          class="btn btn-secondary btn-sm mt-4"
          @click="load"
        >
          {{ t('common.tryAgain') }}
        </button>
      </div>
      <div
        v-else-if="!data || data.results.length === 0"
        class="rounded-2xl bg-white px-6 py-16 text-center text-gray-500 shadow-sm dark:bg-dark-800"
      >
        {{ t('modelIQ.noResults') }}
      </div>
      <section
        v-else
        class="grid gap-4 sm:grid-cols-2 lg:grid-cols-3 2xl:grid-cols-4"
      >
        <article
          v-for="result in data.results"
          :key="`${result.model}-${result.group_id}`"
          class="overflow-hidden rounded-2xl border-t-4 bg-white shadow-sm ring-1 ring-gray-900/5 dark:bg-dark-800 dark:ring-dark-700"
          :class="result.status === 'correct' ? 'border-emerald-500' : result.status === 'incorrect' ? 'border-red-500' : 'border-gray-300'"
        >
          <div class="p-5">
            <div class="flex items-start justify-between gap-3">
              <div class="min-w-0">
                <h2 class="truncate text-base font-bold text-gray-900 dark:text-white">
                  {{ result.group_name }}
                </h2>
                <p class="mt-1 truncate text-xs text-gray-500 dark:text-gray-400">
                  {{ result.model }}
                </p>
              </div>
              <span
                class="shrink-0 rounded-full bg-gray-100 px-2.5 py-1 text-xs font-semibold text-gray-700 dark:bg-dark-700 dark:text-gray-200"
              >
                ×{{ result.rate_multiplier }}
              </span>
            </div>
            <div
              v-if="result.status !== 'no_results'"
              class="mt-5 grid grid-cols-[1fr_auto_1fr] items-stretch gap-2"
            >
              <div class="rounded-xl bg-gray-50 p-3 dark:bg-dark-900/50">
                <p class="text-xs text-gray-500 dark:text-gray-400">
                  {{ t('modelIQ.expectedAnswer') }}
                </p>
                <p class="mt-2 break-words text-lg font-bold text-gray-800 dark:text-gray-100">
                  {{ result.expected_answer }}
                </p>
              </div>
              <div class="flex min-w-16 flex-col items-center justify-center text-center">
                <span
                  class="text-2xl"
                  :class="result.status === 'correct' ? 'text-emerald-500' : 'text-red-500'"
                >
                  {{ result.status === 'correct' ? '✓' : '×' }}
                </span>
                <span
                  class="mt-1 text-xs font-semibold"
                  :class="statusTextClass(result.status)"
                >
                  {{ statusLabel(result.status) }}
                </span>
              </div>
              <div
                class="rounded-xl p-3"
                :class="result.status === 'correct' ? 'bg-emerald-50 dark:bg-emerald-900/20' : 'bg-red-50 dark:bg-red-900/20'"
              >
                <p class="text-xs text-gray-500 dark:text-gray-400">
                  {{ t('modelIQ.modelAnswer') }}
                </p>
                <p class="mt-2 break-words text-lg font-bold text-gray-800 dark:text-gray-100">
                  {{ result.model_answer || t('common.notAvailable') }}
                </p>
              </div>
            </div>
            <p v-else class="mt-5 rounded-xl bg-gray-50 p-4 text-sm text-gray-500 dark:bg-dark-900/50 dark:text-gray-400">{{ t('modelIQ.noResultsForGroup') }}</p>
            <details v-if="result.question" class="mt-4">
              <summary class="cursor-pointer text-sm font-medium text-primary-600 dark:text-primary-400">{{ t('modelIQ.viewQuestion') }}</summary>
              <p class="mt-3 whitespace-pre-wrap text-sm text-gray-700 dark:text-gray-200">{{ result.question }}</p>
            </details>
          </div>
          <div
            v-if="result.output"
            class="border-t border-gray-100 px-5 py-4 dark:border-dark-700"
          >
            <p class="line-clamp-3 whitespace-pre-wrap text-sm text-gray-600 dark:text-gray-300">
              {{ result.output }}
            </p>
            <details class="mt-3">
              <summary class="cursor-pointer text-sm font-medium text-primary-600 dark:text-primary-400">
                {{ t('modelIQ.fullOutput') }}
              </summary>
              <pre class="mt-3 overflow-x-auto whitespace-pre-wrap font-sans text-sm text-gray-700 dark:text-gray-200">{{ result.output }}</pre>
            </details>
          </div>
          <footer
            v-if="result.status !== 'no_results' && result.checked_at"
            class="border-t border-gray-100 bg-gray-50 px-5 py-3 text-xs text-gray-500 dark:border-dark-700 dark:bg-dark-900/30 dark:text-gray-400"
          >
            {{ t('modelIQ.checkedAt') }}
            {{ formatDate(result.checked_at) }}
          </footer>
        </article>
      </section>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { getOpenAIIQ, type IQPublicResultStatus, type OpenAIIQView } from '@/api/openaiIQ'
import AppLayout from '@/components/layout/AppLayout.vue'
const { t } = useI18n()
const data = ref<OpenAIIQView | null>(null)
const loading = ref(true)
const loadError = ref('')
const summaryCards = computed(() => {
  if (!data.value) {
    return []
  }

  return [
    {
      key: 'total',
      label: t('modelIQ.summary.total'),
      value: data.value.summary.total,
      className: 'text-gray-900 dark:text-white',
    },
    {
      key: 'correct',
      label: t('modelIQ.summary.correct'),
      value: data.value.summary.correct,
      className: 'text-emerald-600 dark:text-emerald-400',
    },
    {
      key: 'incorrect',
      label: t('modelIQ.summary.incorrect'),
      value: data.value.summary.incorrect,
      className: 'text-amber-600 dark:text-amber-400',
    },
    {
      key: 'no_results',
      label: t('modelIQ.summary.noResults'),
      value: data.value.summary.no_results,
      className: 'text-gray-600 dark:text-gray-300',
    },
  ]
})

function formatDate(value: string): string {
  return new Intl.DateTimeFormat(undefined, {
    dateStyle: 'medium',
    timeStyle: 'medium',
  }).format(new Date(value))
}

function statusLabel(status: IQPublicResultStatus): string {
  return t(`modelIQ.status.${status}`)
}

function statusTextClass(status: IQPublicResultStatus): string {
  if (status === 'correct') {
    return 'text-emerald-600 dark:text-emerald-400'
  }

  if (status === 'incorrect') {
    return 'text-red-600 dark:text-red-400'
  }

  return 'text-gray-600 dark:text-gray-300'
}

async function load(): Promise<void> {
  loading.value = true
  loadError.value = ''

  try {
    data.value = await getOpenAIIQ()
  } catch {
    loadError.value = t('modelIQ.loadError')
  } finally {
    loading.value = false
  }
}

onMounted(() => {
  void load()
})
</script>
