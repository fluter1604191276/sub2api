<template>
  <AppLayout>
    <div class="mx-auto w-full max-w-5xl space-y-6 pb-8">
      <header
        class="rounded-2xl bg-white p-5 shadow-sm ring-1 ring-gray-900/5 dark:bg-dark-800 dark:ring-dark-700 sm:p-6"
      >
        <div class="flex flex-wrap items-start justify-between gap-4">
          <div>
            <h1 class="text-xl font-black text-gray-900 dark:text-white">
              {{ t('admin.openaiIQ.title') }}
            </h1>
            <p class="mt-2 max-w-3xl text-sm text-gray-500 dark:text-gray-400">
              {{ t('admin.openaiIQ.description') }}
            </p>
          </div>
          <div class="flex gap-2">
            <RouterLink
              to="/model-iq"
              class="btn btn-secondary btn-sm"
            >
              {{ t('admin.openaiIQ.openResults') }}
            </RouterLink>
            <button
              type="submit"
              form="openai-iq-settings"
              class="btn btn-primary btn-sm"
              data-testid="save-openai-iq"
              :disabled="loading || saving || !loaded"
            >
              {{ saving ? t('common.saving') : t('common.save') }}
            </button>
          </div>
        </div>
      </header>

      <div
        v-if="loading"
        class="rounded-2xl bg-white px-6 py-16 text-center text-gray-500 shadow-sm dark:bg-dark-800"
      >
        {{ t('common.loading') }}
      </div>
      <div
        v-else-if="loadError && !loaded"
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
      <form
        v-else
        id="openai-iq-settings"
        @submit.prevent="save"
      >
        <fieldset
          :disabled="saving"
          class="min-w-0 space-y-6"
        >
          <section
            class="rounded-2xl bg-white p-5 shadow-sm ring-1 ring-gray-900/5 dark:bg-dark-800 dark:ring-dark-700 sm:p-6"
          >
            <div class="flex items-center justify-between gap-4">
              <div class="min-w-0 flex-1">
                <h2 class="text-base font-bold text-gray-900 dark:text-white">
                  {{ t('admin.openaiIQ.controlTitle') }}
                </h2>
                <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
                  {{ t('admin.openaiIQ.controlHint') }}
                </p>
              </div>
              <label class="inline-flex shrink-0 cursor-pointer items-center gap-3 whitespace-nowrap">
                <input
                v-model="settings.enabled"
                type="checkbox"
                class="h-5 w-5 rounded border-gray-300 text-primary-600 focus:ring-primary-500"
                data-testid="openai-iq-enabled"
                >
                <span
                class="text-sm font-medium text-gray-700 dark:text-gray-200"
                >
                  {{ settings.enabled ? t('common.enabled') : t('common.disabled') }}
                </span>
              </label>
            </div>
            <div class="mt-5 grid gap-4 md:grid-cols-2">
              <label>
                <span class="input-label">{{ t('admin.openaiIQ.model') }}</span>
                <input
                v-model.trim="settings.model"
                class="input"
                data-testid="openai-iq-model"
                required
                >
              </label>
              <label>
                <span class="input-label">{{ t('admin.openaiIQ.interval') }}</span>
                <input
                v-model.number="settings.interval_seconds"
                class="input"
                type="number"
                min="3600"
                max="86400"
                required
                >
              </label>
              <label>
                <span class="input-label">{{ t('admin.openaiIQ.dailyBudget') }}</span>
                <input
                v-model.number="settings.daily_budget_usd"
                class="input"
                type="number"
                min="0.01"
                max="1000"
                step="0.01"
                required
                >
              </label>
              <label>
                <span class="input-label">{{ t('admin.openaiIQ.requestLimit') }}</span>
                <input
                v-model.number="settings.per_request_limit_usd"
                class="input"
                type="number"
                min="0.01"
                :max="settings.daily_budget_usd"
                step="0.01"
                required
                >
              </label>
              <label class="md:col-span-2">
                <span class="input-label">{{ t('admin.openaiIQ.maxOutputTokens') }}</span>
                <input
                v-model.number="settings.max_output_tokens"
                class="input"
                type="number"
                min="32"
                max="4096"
                required
                >
              </label>
            </div>
          </section>

          <section
            class="rounded-2xl bg-white p-5 shadow-sm ring-1 ring-gray-900/5 dark:bg-dark-800 dark:ring-dark-700 sm:p-6"
          >
            <h2 class="text-base font-bold text-gray-900 dark:text-white">
              {{ t('admin.openaiIQ.questionTitle') }}
            </h2>
            <div class="mt-4 space-y-4">
              <label class="block">
                <span class="input-label">{{ t('admin.openaiIQ.question') }}</span>
                <textarea
                  v-model="settings.question"
                  class="input min-h-40"
                  data-testid="openai-iq-question"
                  required
                ></textarea>
              </label>
              <label class="block">
                <span class="input-label">{{ t('admin.openaiIQ.expectedAnswer') }}</span>
                <textarea
                  v-model="settings.expected_answer"
                  class="input min-h-24"
                  data-testid="openai-iq-expected-answer"
                  required
                ></textarea>
              </label>
            </div>
          </section>

          <section
            class="rounded-2xl bg-white p-5 shadow-sm ring-1 ring-gray-900/5 dark:bg-dark-800 dark:ring-dark-700 sm:p-6"
          >
            <h2 class="text-base font-bold text-gray-900 dark:text-white">
              {{ t('admin.openaiIQ.groupsTitle') }}
            </h2>
            <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
              {{ t('admin.openaiIQ.groupsHint') }}
            </p>
            <div
              v-if="groups.length"
              class="mt-4 grid grid-cols-1 gap-3 sm:grid-cols-2"
            >
              <label
                v-for="group in groups"
                :key="group.id"
                class="flex min-w-0 cursor-pointer items-center gap-3 rounded-xl border border-gray-200 p-3 dark:border-dark-700"
              >
                <input
                  v-model="settings.group_ids"
                  type="checkbox"
                  :value="group.id"
                  class="h-4 w-4 rounded border-gray-300 text-primary-600 focus:ring-primary-500"
                >
                <span
                  class="min-w-0 flex-1 truncate text-sm font-medium text-gray-800 dark:text-gray-100"
                >
                  {{ group.name }}
                </span>
                <span class="text-xs text-gray-500">×{{ group.rate_multiplier }}</span>
              </label>
            </div>
            <p
              v-else
              class="mt-4 text-sm text-gray-500"
            >
              {{ t('common.noData') }}
            </p>
            <div
              v-if="unavailableGroupIDs.length"
              class="mt-4 rounded-xl bg-amber-50 p-3 dark:bg-amber-950/30"
            >
              <p class="text-sm text-amber-800 dark:text-amber-200">
                {{ t('admin.openaiIQ.unavailableGroupsHint') }}
              </p>
              <label
                v-for="id in unavailableGroupIDs"
                :key="id"
                class="mt-3 flex items-center gap-3 text-sm"
              >
                <input
                  v-model="settings.group_ids"
                  type="checkbox"
                  :value="id"
                  :data-testid="`openai-iq-unavailable-${id}`"
                >
                <span>{{ t('admin.openaiIQ.unavailableGroup', { id }) }}</span>
              </label>
            </div>
          </section>

          <section
            class="rounded-2xl border border-amber-200 bg-amber-50 p-5 text-sm text-amber-900 dark:border-amber-900/50 dark:bg-amber-950/30 dark:text-amber-200"
          >
            <h2 class="font-bold">
              {{ t('admin.openaiIQ.testUserTitle') }}
            </h2>
            <p class="mt-2">
              {{ t('admin.openaiIQ.testUserNotice') }}
            </p>
            <p class="mt-2">
              {{ t('admin.openaiIQ.budgetNotice', {
                date: budget.date,
                spent: budget.spent_usd,
                reserved: budget.reserved_usd,
              }) }}
            </p>
            <dl class="mt-4 grid gap-3 sm:grid-cols-4">
              <div>
                <dt class="text-xs opacity-75">
                  {{ t('admin.openaiIQ.testUserId') }}
                </dt>
                <dd class="mt-1 font-semibold">
                  {{ testUserID ?? t('common.notAvailable') }}
                </dd>
              </div>
              <div>
                <dt class="text-xs opacity-75">
                  {{ t('admin.openaiIQ.testUserBalance') }}
                </dt>
                <dd class="mt-1 font-semibold">
                  {{ testUserBalance }}
                </dd>
              </div>
              <div>
                <dt class="text-xs opacity-75">
                  {{ t('admin.openaiIQ.budgetSpent') }}
                </dt>
                <dd class="mt-1 font-semibold">
                  {{ budget.spent_usd }}
                </dd>
              </div>
              <div>
                <dt class="text-xs opacity-75">
                  {{ t('admin.openaiIQ.budgetReserved') }}
                </dt>
                <dd class="mt-1 font-semibold">
                  {{ budget.reserved_usd }}
                </dd>
              </div>
              <div>
                <dt class="text-xs opacity-75">
                  {{ t('admin.openaiIQ.budgetRemaining') }}
                </dt>
                <dd class="mt-1 font-semibold">
                  {{ budget.remaining_usd }}
                </dd>
              </div>
            </dl>
          </section>
        </fieldset>
      </form>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { useI18n } from 'vue-i18n'
import {
  getOpenAIIQSettings,
  updateOpenAIIQSettings,
  type IQBudget,
  type IQGroupOption,
  type IQSettings,
} from '@/api/openaiIQ'
import AppLayout from '@/components/layout/AppLayout.vue'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'

const { t } = useI18n()
const appStore = useAppStore()
const loading = ref(true)
const saving = ref(false)
const loaded = ref(false)
const loadError = ref('')
const groups = ref<IQGroupOption[]>([])
const testUserID = ref<number | null>(null)
const testUserBalance = ref(0)
const budget = ref<IQBudget>({
  date: '',
  spent_usd: 0,
  reserved_usd: 0,
  remaining_usd: 0,
})
const settings = ref<IQSettings>({
  enabled: false,
  question: '',
  expected_answer: '',
  group_ids: [],
  model: 'gpt-6-astra',
  interval_seconds: 3600,
  daily_budget_usd: 1,
  per_request_limit_usd: 0.1,
  max_output_tokens: 2048,
})

const unavailableGroupIDs = computed(() => {
  const activeIDs = new Set(groups.value.map(group => group.id))
  return settings.value.group_ids.filter(id => !activeIDs.has(id))
})

function applyView(view: Awaited<ReturnType<typeof getOpenAIIQSettings>>): void {
  settings.value = {
    ...view.settings,
    group_ids: [...view.settings.group_ids],
  }
  groups.value = view.groups
  testUserID.value = view.test_user_id
  testUserBalance.value = view.test_user_balance
  budget.value = view.budget
}

function isValid(): boolean {
  const value = settings.value
  const hasValidInterval = Number.isFinite(value.interval_seconds)
    && value.interval_seconds >= 3600
    && value.interval_seconds <= 86400
  const hasValidDailyBudget = Number.isFinite(value.daily_budget_usd)
    && value.daily_budget_usd > 0
    && value.daily_budget_usd <= 1000
  const hasValidRequestLimit = Number.isFinite(value.per_request_limit_usd)
    && value.per_request_limit_usd > 0
    && value.per_request_limit_usd <= value.daily_budget_usd
  const hasValidOutputLimit = Number.isFinite(value.max_output_tokens)
    && value.max_output_tokens >= 32
    && value.max_output_tokens <= 4096
  const hasValidLimits = hasValidInterval
    && hasValidDailyBudget
    && hasValidRequestLimit
    && hasValidOutputLimit

  if (!value.enabled) {
    return hasValidLimits
  }

  return hasValidLimits
    && value.model.trim().length > 0
    && value.question.trim().length > 0
    && value.expected_answer.trim().length > 0
    && value.group_ids.length > 0
}

async function load(): Promise<void> {
  loading.value = true
  loadError.value = ''
  try {
    applyView(await getOpenAIIQSettings())
    loaded.value = true
  } catch (error: unknown) {
    loadError.value = extractApiErrorMessage(error, t('admin.openaiIQ.loadError'))
    appStore.showError(loadError.value)
  } finally {
    loading.value = false
  }
}

async function save(): Promise<void> {
  if (!loaded.value || !isValid()) {
    appStore.showError(t('admin.openaiIQ.invalidSettings'))
    return
  }
  saving.value = true

  try {
    applyView(await updateOpenAIIQSettings({
      ...settings.value,
      group_ids: [...settings.value.group_ids],
    }))
    appStore.showSuccess(t('admin.openaiIQ.saveSuccess'))
  } catch (error: unknown) {
    appStore.showError(extractApiErrorMessage(error, t('admin.openaiIQ.saveError')))
  } finally {
    saving.value = false
  }
}

onMounted(() => {
  void load()
})
</script>
