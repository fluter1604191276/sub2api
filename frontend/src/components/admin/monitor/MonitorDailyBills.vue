<template>
  <div class="space-y-2">
    <div class="flex flex-wrap items-center gap-3">
      <span>{{ t('admin.channelMonitor.bills.today') }}:
        <strong>{{ today?.account_cost_usd != null ? money(today.account_cost_usd) : '--' }}</strong>
      </span>
      <span v-if="today">{{ t('admin.channelMonitor.bills.unknown') }}: {{ Math.max(0, today.checks - today.costed_checks) }}</span>
      <button type="button" class="btn btn-secondary btn-sm" @click="show = true">
        <Icon name="clock" size="sm" />{{ t('admin.channelMonitor.bills.title') }}
      </button>
    </div>
    <span v-if="bill">{{ t('admin.channelMonitor.bills.until', { time: clockTime }) }}</span>
    <p v-if="error" role="alert" class="text-red-600">{{ t('admin.channelMonitor.bills.error') }}</p>
    <BaseDialog :show="show" :title="t('admin.channelMonitor.bills.title')" width="extra-wide" @close="show = false">
      <div class="mb-4 flex flex-wrap items-center gap-3">
        <select v-model.number="days" class="input w-40" :aria-label="t('admin.channelMonitor.bills.range')" @change="load">
          <option v-for="n in [7, 30, 90, 365]" :key="n" :value="n">{{ t('admin.channelMonitor.bills.days', { n }) }}</option>
        </select>
        <button type="button" class="btn btn-secondary" :disabled="loading" :title="t('common.refresh')" :aria-label="t('common.refresh')" @click="load">
          <Icon name="refresh" size="sm" />
        </button>
        <span v-if="bill" class="text-xs">{{ t('admin.channelMonitor.bills.until', { time: clockTime }) }}</span>
      </div>
      <p v-if="error" role="alert" class="text-red-600">{{ t('admin.channelMonitor.bills.error') }}</p>
      <div class="overflow-x-auto" :aria-busy="loading">
        <table class="w-full text-left text-sm">
          <thead><tr class="border-b">
            <th v-for="key in ['date', 'cost', 'actual', 'checks', 'unknown', 'status']" :key="key" class="whitespace-nowrap p-2">{{ t(`admin.channelMonitor.bills.${key}`) }}</th>
          </tr></thead>
          <tbody><tr v-for="row in bill?.days ?? []" :key="row.date" class="border-b">
            <td class="whitespace-nowrap p-2">{{ row.date }}</td>
            <td class="p-2 tabular-nums">{{ money(row.base_cost_usd) }}</td>
            <td class="p-2">{{ row.account_cost_usd != null ? money(row.account_cost_usd) : t('admin.channelMonitor.bills.unverified') }}</td>
            <td class="p-2 tabular-nums">{{ row.checks }}</td>
            <td class="p-2 tabular-nums">{{ Math.max(0, row.checks - row.costed_checks) }}</td>
            <td class="p-2">{{ t(`admin.channelMonitor.bills.${row.historical_partial ? 'partial' : row.date === todayDate ? 'ongoing' : 'ended'}`) }}</td>
          </tr></tbody>
        </table>
        <p v-if="!loading && !error && !bill?.days.length" class="py-6 text-center">{{ t('admin.channelMonitor.bills.empty') }}</p>
      </div>
    </BaseDialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { getDailyBills, type ChannelMonitorBills, type ChannelMonitorBudgetStatus } from '@/api/admin/channelMonitor'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'

const props = defineProps<{ budget: ChannelMonitorBudgetStatus }>()
const { t } = useI18n()
const show = ref(false)
const days = ref(30)
const bill = ref<ChannelMonitorBills | null>(null)
const loading = ref(false)
const error = ref(false)
let generation = 0
const todayDate = computed(() => bill.value?.as_of.slice(0, 10))
const today = computed(() => bill.value?.days.find(row => row.date === todayDate.value))
const clockTime = computed(() => bill.value ? new Intl.DateTimeFormat('zh-CN', {
  timeZone: 'Asia/Shanghai', hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false,
}).format(new Date(bill.value.as_of)) : '--')
const money = (value: number) => '$' + value.toFixed(6)
async function load() {
  const id = ++generation
  loading.value = true
  error.value = false
  try {
    const result = await getDailyBills(days.value)
    if (id === generation) bill.value = result
  } catch {
    if (id === generation) { error.value = true; bill.value = null }
  } finally {
    if (id === generation) loading.value = false
  }
}
watch(() => props.budget, load)
watch(show, value => { if (value) void load() })
onMounted(load)
onUnmounted(() => { generation++ })
</script>
