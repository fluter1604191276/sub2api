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
                data-testid="openai-iq-request-limit"
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
              <div
                v-for="group in groups"
                :key="group.id"
                class="flex min-w-0 items-center gap-3 rounded-xl border border-gray-200 p-3 dark:border-dark-700"
              >
                <label class="flex min-w-0 flex-1 cursor-pointer items-center gap-3">
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
                </label>
                <span class="text-xs text-gray-500">×{{ group.rate_multiplier }}</span>
                <button
                  type="button"
                  class="btn btn-secondary btn-sm shrink-0"
                  :data-testid="`openai-iq-recheck-${group.id}`"
                  :disabled="recheckBusy || !isSavedRecheckGroup(group.id)"
                  :title="isSavedRecheckGroup(group.id) ? undefined : t('admin.openaiIQ.recheckRequiresSavedGroup')"
                  @click="openRecheckConfirmation(group)"
                >
                  {{ t('admin.openaiIQ.recheck') }}
                </button>
                <span
                  v-if="!isSavedRecheckGroup(group.id)"
                  :data-testid="`openai-iq-recheck-requires-save-${group.id}`"
                  class="basis-full text-xs text-amber-700 dark:text-amber-300"
                >
                  {{ t('admin.openaiIQ.recheckRequiresSavedGroup') }}
                </span>
              </div>
            </div>
            <p
              v-else
              class="mt-4 text-sm text-gray-500"
            >
              {{ t('common.noData') }}
            </p>
            <p
              v-if="recheckStatus"
              class="mt-4 rounded-lg bg-blue-50 p-3 text-sm text-blue-800 dark:bg-blue-950/30 dark:text-blue-200"
            >
              {{ recheckStatus }}
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
            <dl class="mt-4 grid gap-3 sm:grid-cols-5">
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
              <div class="min-w-0">
                <dt class="text-xs opacity-75">
                  {{ t('admin.openaiIQ.budgetUnconfirmed') }}
                </dt>
                <dd class="mt-1 font-semibold">
                  {{ budget.unconfirmed_usd }}
                </dd>
                <p
                  v-if="budget.unconfirmed_since"
                  class="mt-1 break-words text-xs opacity-75"
                >
                  {{ t('admin.openaiIQ.budgetUnconfirmedSince', { time: formatDate(budget.unconfirmed_since) }) }}
                </p>
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
          <section class="rounded-2xl bg-white p-5 shadow-sm ring-1 ring-gray-900/5 dark:bg-dark-800 dark:ring-dark-700 sm:p-6">
            <div class="flex flex-wrap items-center justify-between gap-3">
              <div class="min-w-0">
                <h2 class="text-base font-bold text-gray-900 dark:text-white">
                  {{ t('admin.openaiIQ.diagnosticsTitle') }}
                </h2>
                <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
                  {{ t('admin.openaiIQ.diagnosticsHint') }}
                </p>
              </div>
              <button type="button" class="btn btn-secondary btn-sm" :disabled="diagnosticsLoading" @click="loadDiagnostics(1)">
                {{ t('common.refresh') }}
              </button>
            </div>
            <p v-if="diagnosticsError" class="mt-4 rounded-lg bg-red-50 p-3 text-sm text-red-700 dark:bg-red-900/20 dark:text-red-300">
              {{ t('admin.openaiIQ.diagnosticsLoadError') }}
            </p>
            <div v-else-if="diagnosticsLoading" class="mt-4 text-sm text-gray-500">
              {{ t('common.loading') }}
            </div>
            <div v-else-if="diagnostics.runs.length" class="mt-4 space-y-3">
              <details v-for="run in diagnostics.runs" :key="run.id" class="rounded-xl border border-gray-200 p-4 dark:border-dark-700">
                <summary class="cursor-pointer break-words font-medium text-gray-900 dark:text-white">
                  {{ run.model }} · {{ formatDate(run.started_at) }} · {{ triggerLabel(run.trigger) }} · {{ run.status }}
                </summary>
                <p class="mt-3 whitespace-pre-wrap text-sm text-gray-600 dark:text-gray-300">
                  {{ run.question }}
                </p>
                <div
                  v-for="result in run.results"
                  :key="result.id"
                  class="mt-3 min-w-0 rounded-lg bg-gray-50 p-3 text-sm dark:bg-dark-900/50"
                >
                  <div class="flex flex-wrap justify-between gap-2">
                    <span class="min-w-0 break-words font-medium">{{ result.group_name }} · {{ result.status }}</span>
                    <span>{{ result.checked_at ? formatDate(result.checked_at) : t('common.notAvailable') }}</span>
                  </div>
                  <p
                    v-if="result.response_model && result.response_model !== result.model"
                    class="mt-2 break-words text-gray-500 dark:text-gray-400"
                  >
                    {{ t('admin.openaiIQ.diagnosticsResponseModel', { model: result.response_model }) }}
                  </p>
                  <dl class="mt-3 grid gap-2 sm:grid-cols-2">
                    <div>
                      <dt class="text-xs text-gray-500">{{ t('admin.openaiIQ.expectedAnswer') }}</dt>
                      <dd class="break-words">{{ result.expected_answer }}</dd>
                    </div>
                    <div>
                      <dt class="text-xs text-gray-500">{{ t('admin.openaiIQ.diagnosticsModelAnswer') }}</dt>
                      <dd class="break-words">{{ result.model_answer || t('common.notAvailable') }}</dd>
                    </div>
                  </dl>
                  <p v-if="result.error" class="mt-3 whitespace-pre-wrap break-words text-red-700 dark:text-red-300">
                    {{ result.error }}
                  </p>
                  <details v-if="result.output" class="mt-3">
                    <summary class="cursor-pointer font-medium text-primary-600 dark:text-primary-400">
                      {{ t('admin.openaiIQ.diagnosticsOutput') }}
                    </summary>
                    <pre class="mt-2 max-w-full overflow-x-auto whitespace-pre-wrap break-words font-sans text-xs text-gray-700 dark:text-gray-200">{{ result.output }}</pre>
                  </details>
                  <dl
                    v-if="result.reservation"
                    class="mt-3 grid gap-2 rounded-lg border border-gray-200 p-3 text-xs dark:border-dark-700 sm:grid-cols-2"
                  >
                    <div>
                      <dt class="text-gray-500">{{ t('admin.openaiIQ.diagnosticsReservation') }}</dt>
                      <dd>{{ result.reservation.reserved_usd }}</dd>
                    </div>
                    <div>
                      <dt class="text-gray-500">{{ t('admin.openaiIQ.diagnosticsActualCost') }}</dt>
                      <dd>{{ result.reservation.actual_cost_usd ?? t('common.notAvailable') }}</dd>
                    </div>
                    <div>
                      <dt class="text-gray-500">{{ t('admin.openaiIQ.diagnosticsRequestID') }}</dt>
                      <dd class="break-all">{{ result.reservation.request_id ?? t('common.notAvailable') }}</dd>
                    </div>
                    <div>
                      <dt class="text-gray-500">{{ t('admin.openaiIQ.diagnosticsUsageLogID') }}</dt>
                      <dd class="break-all">{{ result.reservation.usage_log_id ?? t('common.notAvailable') }}</dd>
                    </div>
                    <div>
                      <dt class="text-gray-500">{{ t('admin.openaiIQ.diagnosticsAccountID') }}</dt>
                      <dd class="break-all">{{ result.reservation.account_id ?? t('common.notAvailable') }}</dd>
                    </div>
                    <div>
                      <dt class="text-gray-500">{{ t('admin.openaiIQ.diagnosticsSettledAt') }}</dt>
                      <dd class="break-words">{{ result.reservation.settled_at ? formatDate(result.reservation.settled_at) : t('common.notAvailable') }}</dd>
                    </div>
                  </dl>
                </div>
              </details>
              <div class="flex justify-end gap-2">
                <button type="button" class="btn btn-secondary btn-sm" :disabled="diagnostics.page <= 1" @click="loadDiagnostics(diagnostics.page - 1)">
                  {{ t('admin.openaiIQ.previousPage') }}
                </button>
                <button type="button" class="btn btn-secondary btn-sm" :disabled="diagnostics.page * diagnostics.page_size >= diagnostics.total" @click="loadDiagnostics(diagnostics.page + 1)">
                  {{ t('admin.openaiIQ.nextPage') }}
                </button>
              </div>
            </div>
            <p v-else class="mt-4 text-sm text-gray-500">{{ t('common.noData') }}</p>
          </section>
        </fieldset>
      </form>
    </div>
  </AppLayout>
  <ConfirmDialog
    :show="recheckGroup !== null"
    :title="t('admin.openaiIQ.recheckConfirmTitle')"
    :message="recheckConfirmationMessage"
    :confirm-text="t('admin.openaiIQ.recheck')"
    @confirm="confirmRecheck"
    @cancel="closeRecheckConfirmation"
  />
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { useI18n } from 'vue-i18n'
import {
  getOpenAIIQSettings,
  getOpenAIIQDiagnostics,
  recheckOpenAIIQGroup,
  updateOpenAIIQSettings,
  type IQBudget,
  type IQGroupOption,
  type IQSettings,
  type OpenAIIQDiagnostics,
} from '@/api/openaiIQ'
import AppLayout from '@/components/layout/AppLayout.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage, extractI18nErrorMessage } from '@/utils/apiError'

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
  unconfirmed_usd: 0,
  unconfirmed_since: null,
  remaining_usd: 0,
})
const diagnosticsLoading = ref(false)
const diagnosticsError = ref(false)
const diagnostics = ref<OpenAIIQDiagnostics>({ runs: [], total: 0, page: 1, page_size: 20 })
const savedSettings = ref<IQSettings | null>(null)
const recheckGroup = ref<IQGroupOption | null>(null)
const recheckSubmitting = ref(false)
const manualRunID = ref<number | null>(null)
const manualRunGroupID = ref<number | null>(null)
const recheckStatus = ref('')
let diagnosticsPollTimer: ReturnType<typeof setTimeout> | null = null
let diagnosticsRequestVersion = 0
let diagnosticsPollingRequested = false
let unmounted = false
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
const recheckBusy = computed(() => recheckSubmitting.value || manualRunID.value !== null || diagnostics.value.runs.some(isActiveRun))
const recheckConfirmationMessage = computed(() => {
  if (!recheckGroup.value || !savedSettings.value) return ''
  return t('admin.openaiIQ.recheckConfirm', {
    group: recheckGroup.value.name,
    amount: savedSettings.value.per_request_limit_usd,
  })
})
function isSavedRecheckGroup(groupID: number): boolean {
  return savedSettings.value?.group_ids.includes(groupID) ?? false
}

function applyView(view: Awaited<ReturnType<typeof getOpenAIIQSettings>>): void {
  settings.value = {
    ...view.settings,
    group_ids: [...view.settings.group_ids],
  }
  savedSettings.value = {
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
    void loadDiagnostics(1)
  } catch (error: unknown) {
    loadError.value = extractApiErrorMessage(error, t('admin.openaiIQ.loadError'))
    appStore.showError(loadError.value)
  } finally {
    loading.value = false
  }
}

function formatDate(value: string): string {
  return new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'medium' }).format(new Date(value))
}
function triggerLabel(trigger: 'scheduled' | 'manual' | undefined): string {
  return t(trigger === 'manual' ? 'admin.openaiIQ.triggerManual' : 'admin.openaiIQ.triggerScheduled')
}
function isActiveRun(run: OpenAIIQDiagnostics['runs'][number]): boolean {
  if (run.status === 'pending') return true
  return run.status === 'running' && run.lease_active !== false
}
function stopDiagnosticsPolling(): void {
  diagnosticsPollingRequested = false
  if (diagnosticsPollTimer !== null) {
    clearTimeout(diagnosticsPollTimer)
    diagnosticsPollTimer = null
  }
}
function startDiagnosticsPolling(): void {
  diagnosticsPollingRequested = true
  if (unmounted || diagnosticsPollTimer !== null) return
  diagnosticsPollTimer = setTimeout(() => {
    diagnosticsPollTimer = null
    void loadDiagnostics(1)
  }, 2000)
}
async function loadDiagnostics(page: number): Promise<void> {
  const requestVersion = ++diagnosticsRequestVersion
  diagnosticsLoading.value = true
  diagnosticsError.value = false
  try {
    const nextDiagnostics = await getOpenAIIQDiagnostics(page, diagnostics.value.page_size)
    if (unmounted || requestVersion !== diagnosticsRequestVersion) return
    diagnostics.value = nextDiagnostics
    if (manualRunID.value !== null) {
      const run = diagnostics.value.runs.find(item => item.id === manualRunID.value)
      if (run && !isActiveRun(run)) {
        manualRunID.value = null
        stopDiagnosticsPolling()
        const result = run.results.find(item => item.group_id === manualRunGroupID.value)
        manualRunGroupID.value = null
        if (run.status === 'running') {
          recheckStatus.value = t('admin.openaiIQ.recheckExpired')
          appStore.showError(recheckStatus.value)
        } else if (result && (result.status === 'correct' || result.status === 'incorrect')) {
          recheckStatus.value = t('admin.openaiIQ.recheckComplete')
          appStore.showSuccess(recheckStatus.value)
        } else {
          recheckStatus.value = result?.error || t('admin.openaiIQ.recheckResultUnavailable')
          appStore.showError(recheckStatus.value)
        }
      }
    }
    const hasActiveRun = diagnostics.value.runs.some(isActiveRun)
    if (hasActiveRun || manualRunID.value !== null) startDiagnosticsPolling()
    else stopDiagnosticsPolling()
  } catch {
    if (unmounted || requestVersion !== diagnosticsRequestVersion) return
    diagnosticsError.value = true
  } finally {
    if (!unmounted && requestVersion === diagnosticsRequestVersion) {
      diagnosticsLoading.value = false
      if (diagnosticsPollingRequested) startDiagnosticsPolling()
    }
  }
}

function openRecheckConfirmation(group: IQGroupOption): void {
  if (recheckBusy.value || !isSavedRecheckGroup(group.id)) return
  recheckGroup.value = group
}
function closeRecheckConfirmation(): void {
  if (!recheckSubmitting.value) recheckGroup.value = null
}
async function confirmRecheck(): Promise<void> {
  const group = recheckGroup.value
  const confirmedLimitUSD = savedSettings.value?.per_request_limit_usd
  if (!group || confirmedLimitUSD === undefined || recheckSubmitting.value) return
  recheckSubmitting.value = true
  try {
    const run = await recheckOpenAIIQGroup(group.id, confirmedLimitUSD)
    if (unmounted) return
    manualRunID.value = run.run_id
    manualRunGroupID.value = group.id
    recheckGroup.value = null
    recheckStatus.value = t('admin.openaiIQ.recheckRunning')
    appStore.showSuccess(t('admin.openaiIQ.recheckStarted'))
    await loadDiagnostics(1)
    if (manualRunID.value !== null) startDiagnosticsPolling()
  } catch (error: unknown) {
    if (unmounted) return
    recheckStatus.value = extractI18nErrorMessage(
      error,
      t,
      'admin.openaiIQ.recheckErrors',
      extractApiErrorMessage(error, t('admin.openaiIQ.recheckFailed')),
    )
    appStore.showError(recheckStatus.value)
  } finally {
    if (!unmounted) recheckSubmitting.value = false
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
onBeforeUnmount(stopDiagnosticsPolling)
onBeforeUnmount(() => {
  unmounted = true
  diagnosticsRequestVersion += 1
})
</script>
