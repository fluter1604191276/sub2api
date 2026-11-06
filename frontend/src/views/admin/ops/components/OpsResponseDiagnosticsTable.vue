<template>
  <section class="rounded-2xl border border-gray-200 bg-white p-5 shadow-sm dark:border-dark-700 dark:bg-dark-800">
    <div class="mb-4 flex flex-wrap items-start justify-between gap-3">
      <div>
        <h2 class="text-base font-semibold text-gray-900 dark:text-gray-100">{{ t('admin.ops.responseDiagnostics.title') }}</h2>
        <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('admin.ops.responseDiagnostics.description') }}</p>
      </div>
      <button class="btn-secondary" :disabled="loading" @click="fetchRows">{{ t('common.refresh') }}</button>
    </div>

    <form class="mb-4 grid grid-cols-1 gap-3 md:grid-cols-3 xl:grid-cols-6" @submit.prevent="applyFilters">
      <input v-model="filters.start_time" class="input" type="datetime-local" :aria-label="t('admin.ops.responseDiagnostics.startTime')">
      <input v-model="filters.end_time" class="input" type="datetime-local" :aria-label="t('admin.ops.responseDiagnostics.endTime')">
      <select v-model="filters.status" class="input" :aria-label="t('admin.ops.responseDiagnostics.status')">
        <option value="">{{ t('admin.ops.responseDiagnostics.allStatuses') }}</option>
        <option v-for="option in statuses" :key="option" :value="option">{{ statusLabel(option) }}</option>
      </select>
      <input v-model="filters.reason" class="input" :aria-label="t('admin.ops.responseDiagnostics.reason')" :placeholder="t('admin.ops.responseDiagnostics.reason')">
      <input v-model="filters.account_id" class="input" inputmode="numeric" :aria-label="t('admin.ops.responseDiagnostics.accountId')" :placeholder="t('admin.ops.responseDiagnostics.accountId')">
      <input v-model="filters.user_id" class="input" inputmode="numeric" :aria-label="t('admin.ops.responseDiagnostics.userId')" :placeholder="t('admin.ops.responseDiagnostics.userId')">
      <input v-model="filters.request_id" class="input md:col-span-2" :aria-label="t('admin.ops.responseDiagnostics.requestId')" :placeholder="t('admin.ops.responseDiagnostics.requestId')">
      <button class="btn-primary" type="submit" :disabled="loading">{{ t('admin.ops.responseDiagnostics.search') }}</button>
    </form>

    <p v-if="validationMessage" class="mb-3 text-sm text-red-600 dark:text-red-400" role="alert">{{ validationMessage }}</p>

    <p class="mb-3 text-xs text-amber-700 dark:text-amber-300">{{ t('admin.ops.responseDiagnostics.incompleteHint') }}</p>
    <div class="overflow-x-auto">
      <table class="min-w-full text-left text-sm">
        <thead class="border-b border-gray-200 text-xs text-gray-500 dark:border-dark-700 dark:text-gray-400">
          <tr><th class="px-2 py-2">{{ t('admin.ops.responseDiagnostics.time') }}</th><th class="px-2 py-2">{{ t('admin.ops.responseDiagnostics.upstreamStatus') }}</th><th class="px-2 py-2">{{ t('admin.ops.responseDiagnostics.requestId') }}</th><th class="px-2 py-2">{{ t('admin.ops.responseDiagnostics.route') }}</th><th class="px-2 py-2">{{ t('admin.ops.responseDiagnostics.limits') }}</th><th class="px-2 py-2">{{ t('admin.ops.responseDiagnostics.tokens') }}</th></tr>
        </thead>
        <tbody>
          <tr v-if="loading"><td class="px-2 py-4 text-gray-500" colspan="6">{{ t('common.loading') }}</td></tr>
          <tr v-else-if="rows.length === 0"><td class="px-2 py-4 text-gray-500" colspan="6">{{ t('admin.ops.responseDiagnostics.empty') }}</td></tr>
          <tr v-for="row in rows" :key="row.id" class="border-b border-gray-100 align-top dark:border-dark-700">
            <td class="px-2 py-3 whitespace-nowrap">{{ formatTime(row.created_at) }}</td>
            <td class="px-2 py-3"><div class="font-medium">{{ statusLabel(row.status) }}</div><div v-if="row.client_disconnected" class="mt-1 text-xs font-medium text-amber-700 dark:text-amber-300">{{ t('admin.ops.responseDiagnostics.clientDisconnected') }}</div><div class="text-xs text-gray-500">{{ value(row.reason) }}</div><div class="text-xs text-gray-500">HTTP {{ value(row.http_status) }}</div></td>
            <td class="max-w-60 break-all px-2 py-3 text-xs"><div>{{ t('admin.ops.responseDiagnostics.serverRequestId') }}: {{ value(row.request_id) }}</div><div>{{ t('admin.ops.responseDiagnostics.clientRequestId') }}: {{ value(row.client_request_id) }}</div><div>{{ t('admin.ops.responseDiagnostics.upstreamRequestId') }}: {{ value(row.upstream_request_id) }}</div><div>{{ t('admin.ops.responseDiagnostics.responseId') }}: {{ value(row.response_id) }}</div></td>
            <td class="px-2 py-3 text-xs"><div>{{ value(row.model) }}</div><div>{{ t('admin.ops.responseDiagnostics.accountId') }}: {{ value(row.account_id) }}</div><div>{{ t('admin.ops.responseDiagnostics.userId') }}: {{ value(row.user_id) }}</div><div>{{ t('admin.ops.responseDiagnostics.groupId') }}: {{ value(row.group_id) }}</div></td>
            <td class="px-2 py-3 text-xs"><div>{{ t('admin.ops.responseDiagnostics.requestedLimit') }}: {{ value(row.requested_max_output_tokens) }}</div><div>{{ t('admin.ops.responseDiagnostics.forwardedLimit') }}: {{ value(row.forwarded_max_output_tokens) }}</div><div>{{ t('admin.ops.responseDiagnostics.upstreamLimit') }}: {{ value(row.upstream_max_output_tokens) }}</div></td>
            <td class="px-2 py-3 text-xs"><div>{{ t('admin.ops.responseDiagnostics.inputTokens') }}: {{ value(row.input_tokens) }}</div><div>{{ t('admin.ops.responseDiagnostics.cacheReadTokens') }}: {{ value(row.cache_read_tokens) }}</div><div>{{ t('admin.ops.responseDiagnostics.cacheCreationTokens') }}: {{ value(row.cache_creation_tokens) }}</div><div>{{ t('admin.ops.responseDiagnostics.outputTokens') }}: {{ value(row.output_tokens) }}</div><div>{{ t('admin.ops.responseDiagnostics.reasoningTokens') }}: {{ value(row.reasoning_tokens) }}</div></td>
          </tr>
        </tbody>
      </table>
    </div>
    <div class="mt-4 flex items-center justify-between text-sm text-gray-600 dark:text-gray-300">
      <span>{{ t('admin.ops.responseDiagnostics.total', { count: total }) }}</span>
      <div class="flex gap-2"><button class="btn-secondary" :disabled="loading || page <= 1" @click="changePage(page - 1)">{{ t('common.previous') }}</button><span class="py-2">{{ page }}</span><button class="btn-secondary" :disabled="loading || page * pageSize >= total" @click="changePage(page + 1)">{{ t('common.next') }}</button></div>
    </div>
  </section>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { opsAPI, type OpsResponseDiagnostic, type OpsResponseDiagnosticStatus } from '@/api/admin/ops'
import { useAppStore } from '@/stores'

const { t } = useI18n()
const appStore = useAppStore()
const loading = ref(false)
const rows = ref<OpsResponseDiagnostic[]>([])
const total = ref(0)
const validationMessage = ref('')
const page = ref(1)
const pageSize = 20
const statuses: OpsResponseDiagnosticStatus[] = ['completed', 'incomplete', 'failed', 'client_disconnected', 'missing_terminal']
const filters = reactive({ start_time: '', end_time: '', status: '', reason: '', account_id: '', user_id: '', request_id: '' })

const value = (v: unknown) => v == null || v === '' ? t('admin.ops.responseDiagnostics.unknown') : String(v)
const formatTime = (v: string) => Number.isNaN(new Date(v).getTime()) ? v : new Date(v).toLocaleString()
const asPositiveInt = (v: string) => /^\d+$/.test(v.trim()) && Number(v) > 0 ? Number(v) : undefined
const asISO = (v: string) => v ? new Date(v).toISOString() : undefined
const statusLabel = (status: OpsResponseDiagnosticStatus) => t(`admin.ops.responseDiagnostics.statuses.${status}`)

function validateFilters() {
  validationMessage.value = ''
  if ((filters.account_id.trim() && !asPositiveInt(filters.account_id)) || (filters.user_id.trim() && !asPositiveInt(filters.user_id))) {
    validationMessage.value = t('admin.ops.responseDiagnostics.invalidId'); return false
  }
  const start = filters.start_time ? new Date(filters.start_time) : null
  const end = filters.end_time ? new Date(filters.end_time) : null
  if ((start && Number.isNaN(start.getTime())) || (end && Number.isNaN(end.getTime())) || (start && end && start > end)) {
    validationMessage.value = t('admin.ops.responseDiagnostics.invalidTimeRange'); return false
  }
  return true
}

async function fetchRows() {
  loading.value = true
  try {
    const result = await opsAPI.listResponseDiagnostics({
      page: page.value, page_size: pageSize,
      start_time: asISO(filters.start_time), end_time: asISO(filters.end_time),
      status: (filters.status || undefined) as OpsResponseDiagnosticStatus | undefined,
      reason: filters.reason.trim() || undefined, account_id: asPositiveInt(filters.account_id),
      user_id: asPositiveInt(filters.user_id), request_id: filters.request_id.trim() || undefined
    })
    rows.value = result.items || []; total.value = result.total || 0
  } catch {
    appStore.showError(t('admin.ops.responseDiagnostics.loadFailed'))
  } finally { loading.value = false }
}
function applyFilters() { if (!validateFilters()) return; page.value = 1; fetchRows() }
function changePage(next: number) { page.value = next; fetchRows() }
onMounted(fetchRows)
</script>
