<template>
  <section class="rounded-2xl border border-gray-200 bg-white p-5 shadow-sm dark:border-dark-700 dark:bg-dark-800">
    <div class="mb-4 flex flex-wrap items-start justify-between gap-3">
      <div>
        <h2 class="text-base font-semibold text-gray-900 dark:text-gray-100">{{ t('admin.ops.upstreamAttempts.title') }}</h2>
        <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('admin.ops.upstreamAttempts.description') }}</p>
      </div>
      <button class="btn-secondary" :disabled="loading" @click="load">{{ t('admin.ops.upstreamAttempts.refresh') }}</button>
    </div>
    <form class="mb-4 grid grid-cols-1 gap-2 md:grid-cols-4" @submit.prevent="load">
      <input v-model="accountID" class="input" inputmode="numeric" :placeholder="t('admin.ops.upstreamAttempts.accountId')" :aria-label="t('admin.ops.upstreamAttempts.accountId')">
      <input v-model="startDate" class="input" type="date" :aria-label="t('admin.ops.upstreamAttempts.startDate')">
      <input v-model="endDate" class="input" type="date" :aria-label="t('admin.ops.upstreamAttempts.endDate')">
      <button class="btn-primary" :disabled="loading" type="submit">{{ t('admin.ops.upstreamAttempts.refresh') }}</button>
    </form>
    <p v-if="validationMessage" class="mb-3 text-sm text-red-600 dark:text-red-400" role="alert">{{ validationMessage }}</p>
    <div class="overflow-x-auto">
      <table class="min-w-full text-left text-sm">
        <thead class="border-b border-gray-200 text-xs text-gray-500 dark:border-dark-700 dark:text-gray-400">
          <tr><th class="px-2 py-2">{{ t('admin.ops.upstreamAttempts.day') }}</th><th class="px-2 py-2">{{ t('admin.ops.upstreamAttempts.accountId') }}</th><th class="px-2 py-2">{{ t('admin.ops.upstreamAttempts.attempts') }}</th><th class="px-2 py-2">{{ t('admin.ops.upstreamAttempts.knownCost') }}</th><th class="px-2 py-2">{{ t('admin.ops.upstreamAttempts.unknown') }}</th></tr>
        </thead>
        <tbody>
          <tr v-if="loading"><td colspan="5" class="px-2 py-4 text-gray-500">{{ t('common.loading') }}</td></tr>
          <tr v-else-if="rows.length === 0"><td colspan="5" class="px-2 py-4 text-gray-500">{{ t('admin.ops.upstreamAttempts.empty') }}</td></tr>
          <tr v-for="row in rows" :key="`${row.day}-${row.account_id}`" class="border-b border-gray-100 dark:border-dark-700"><td class="px-2 py-3">{{ day(row.day) }}</td><td class="px-2 py-3">{{ row.account_id }}</td><td class="px-2 py-3">{{ count(row.attempt_count) }}</td><td class="px-2 py-3">{{ usd(row.known_cost_usd) }}</td><td class="px-2 py-3">{{ count(row.unknown_count) }}</td></tr>
        </tbody>
      </table>
    </div>
  </section>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { opsAPI, type UpstreamAttemptDailyCost } from '@/api/admin/ops'
import { useAppStore } from '@/stores'

const { t } = useI18n()
const appStore = useAppStore()
const accountID = ref('')
const startDate = ref('')
const endDate = ref('')
const loading = ref(false)
const rows = ref<UpstreamAttemptDailyCost[]>([])
const validationMessage = ref('')
let latestRequest = 0

const day = (value: string) => /^\d{4}-\d{2}-\d{2}/.test(value) ? value.slice(0, 10) : '-'
const usd = (value: number) => Number.isFinite(value) ? value.toFixed(6) : '-'
const count = (value: number) => Number.isFinite(value) && value >= 0 ? String(value) : '-'

function nextBeijingDay(value: string): string {
  const date = new Date(`${value}T00:00:00Z`)
  date.setUTCDate(date.getUTCDate() + 1)
  return date.toISOString().slice(0, 10)
}

function validate(): number | undefined {
  validationMessage.value = ''
  const raw = accountID.value.trim()
  if (raw && (!/^\d+$/.test(raw) || !Number.isSafeInteger(Number(raw)) || Number(raw) <= 0)) {
    validationMessage.value = t('admin.ops.upstreamAttempts.invalidAccountId')
    return undefined
  }
  if ((startDate.value && !/^\d{4}-\d{2}-\d{2}$/.test(startDate.value)) || (endDate.value && !/^\d{4}-\d{2}-\d{2}$/.test(endDate.value))) {
    validationMessage.value = t('admin.ops.upstreamAttempts.invalidDateRange')
    return undefined
  }
  if (startDate.value && endDate.value) {
    if (endDate.value < startDate.value || (Date.parse(`${endDate.value}T00:00:00Z`) - Date.parse(`${startDate.value}T00:00:00Z`)) / 86_400_000 >= 30) {
      validationMessage.value = t('admin.ops.upstreamAttempts.invalidDateRange')
      return undefined
    }
  }
  return raw ? Number(raw) : undefined
}

async function load() {
  const parsedAccountID = validate()
  if (validationMessage.value) return
  const requestID = ++latestRequest
  loading.value = true
  try {
    const result = await opsAPI.listUpstreamAttemptDailyCosts({
      account_id: parsedAccountID,
      start_time: startDate.value ? `${startDate.value}T00:00:00+08:00` : undefined,
      end_time: endDate.value ? `${nextBeijingDay(endDate.value)}T00:00:00+08:00` : undefined,
      time_range: startDate.value || endDate.value ? undefined : '24h'
    })
    if (requestID === latestRequest) rows.value = result
  } catch {
    if (requestID === latestRequest) appStore.showError(t('admin.ops.upstreamAttempts.loadFailed'))
  } finally {
    if (requestID === latestRequest) loading.value = false
  }
}

onMounted(load)
</script>
