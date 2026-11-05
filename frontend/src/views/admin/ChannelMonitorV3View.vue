<template>
  <AppLayout>
    <div class="w-full min-w-0 space-y-6 pb-8">
      <header class="page-header rounded-3xl bg-white p-5 shadow-sm ring-1 ring-gray-900/5 dark:bg-dark-800 dark:ring-dark-700 sm:p-6">
        <h1 class="page-title flex items-center gap-2 text-xl font-black text-gray-900 dark:text-white">
          <span class="inline-flex h-8 w-8 items-center justify-center rounded-xl bg-blue-50 text-blue-500 dark:bg-blue-900/30 dark:text-blue-400">
            <Icon name="chart" size="sm" />
          </span>
          {{ t('admin.channelMonitorV3.title') }}
        </h1>
        <p class="page-description mt-1.5 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.channelMonitorV3.description') }}</p>
      </header>

      <p v-if="loading" class="text-sm text-gray-500" data-testid="loading">{{ t('admin.channelMonitorV3.loading') }}</p>
      <section v-else-if="loadError" class="rounded-2xl border border-red-200 bg-red-50 p-4 text-sm text-red-700 dark:border-red-900 dark:bg-red-950/30 dark:text-red-300" data-testid="load-error">
        {{ t('admin.channelMonitorV3.loadError') }}
        <button type="button" class="ml-3 underline" @click="load">{{ t('common.tryAgain') }}</button>
      </section>

      <template v-else>
        <section class="rounded-3xl bg-white p-5 shadow-sm ring-1 ring-gray-900/5 dark:bg-dark-800 dark:ring-dark-700 sm:p-6">
          <h2 class="text-base font-bold text-gray-900 dark:text-white">{{ t('admin.channelMonitorV3.global') }}</h2>
          <form class="mt-4 grid gap-4 sm:grid-cols-[auto_minmax(14rem,1fr)_auto] sm:items-end" @submit.prevent="saveSettings">
            <label class="flex items-center gap-2 text-sm font-medium text-gray-700 dark:text-gray-200">
              <input v-model="settings.enabled" type="checkbox" class="h-4 w-4 rounded border-gray-300 text-primary-600 focus:ring-primary-500" data-testid="global-enabled" />
              {{ t('admin.channelMonitorV3.enabled') }}
            </label>
            <label class="grid gap-1 text-sm font-medium text-gray-700 dark:text-gray-200">
              {{ t('admin.channelMonitorV3.dailyBudget') }}
              <input v-model.number="settings.daily_budget_usd" type="number" min="0.001" step="0.001" class="input" data-testid="daily-budget" />
            </label>
            <button type="submit" class="btn btn-primary" :disabled="savingSettings" data-testid="save-settings">
              {{ t('admin.channelMonitorV3.saveGlobal') }}
            </button>
          </form>
          <p class="mt-3 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.channelMonitorV3.budgetHint') }} {{ t('admin.channelMonitorV3.userCost') }}</p>
        </section>

        <section class="rounded-3xl bg-white p-5 shadow-sm ring-1 ring-gray-900/5 dark:bg-dark-800 dark:ring-dark-700 sm:p-6">
          <h2 class="text-base font-bold text-gray-900 dark:text-white">{{ t('admin.channelMonitorV3.groups') }}</h2>
          <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.channelMonitorV3.groupHint') }}</p>
          <div v-if="groups.length === 0" class="py-8 text-center text-sm text-gray-500">{{ t('admin.channelMonitorV3.noGroups') }}</div>
          <div v-else class="mt-4 overflow-x-auto">
            <table class="min-w-full divide-y divide-gray-200 text-left text-sm dark:divide-dark-700">
              <thead class="text-xs uppercase tracking-wide text-gray-500 dark:text-gray-400">
                <tr>
                  <th class="px-3 py-2">{{ t('admin.channelMonitorV3.name') }}</th>
                  <th class="px-3 py-2">{{ t('admin.channelMonitorV3.platform') }}</th>
                  <th class="px-3 py-2">{{ t('admin.channelMonitorV3.status') }}</th>
                  <th class="px-3 py-2">{{ t('admin.channelMonitorV3.visible') }}</th>
                  <th class="px-3 py-2">{{ t('admin.channelMonitorV3.interval') }}</th>
                  <th class="px-3 py-2">{{ t('admin.channelMonitorV3.model') }}</th>
                  <th class="px-3 py-2"><span class="sr-only">{{ t('common.actions') }}</span></th>
                </tr>
              </thead>
              <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
                <tr v-for="group in groups" :key="group.group_id">
                  <td class="px-3 py-3 font-medium text-gray-900 dark:text-white">{{ group.name }}</td>
                  <td class="px-3 py-3 text-gray-600 dark:text-gray-300">{{ group.platform }}</td>
                  <td class="px-3 py-3 text-gray-600 dark:text-gray-300">{{ group.status }}</td>
                  <td class="px-3 py-3"><input v-model="group.visible" type="checkbox" class="h-4 w-4 rounded border-gray-300 text-primary-600 focus:ring-primary-500" :data-testid="`group-visible-${group.group_id}`" /></td>
                  <td class="px-3 py-3">
                    <label class="grid gap-1"><input v-model.number="group.probe_interval_seconds" type="number" min="0" max="86400" step="1" class="input min-w-36" :data-testid="`group-interval-${group.group_id}`" /><span class="text-xs text-gray-500">{{ t('admin.channelMonitorV3.disabledInterval') }}</span></label>
                  </td>
                  <td class="px-3 py-3">
                    <label class="grid gap-1"><input v-model.trim="group.test_model" type="text" class="input min-w-40" :data-testid="`group-model-${group.group_id}`" /><span class="text-xs text-gray-500">{{ t('admin.channelMonitorV3.modelHint') }}</span></label>
                  </td>
                  <td class="px-3 py-3"><button type="button" class="btn btn-secondary btn-sm" :disabled="savingGroups.has(group.group_id)" :data-testid="`save-group-${group.group_id}`" @click="saveGroup(group)">{{ t('admin.channelMonitorV3.saveGroup') }}</button></td>
                </tr>
              </tbody>
            </table>
          </div>
        </section>
      </template>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'
import { channelMonitorV3API, type ChannelMonitorV3Group } from '@/api/admin/channelMonitorV3'

const { t } = useI18n()
const appStore = useAppStore()
const loading = ref(true)
const loadError = ref(false)
const savingSettings = ref(false)
const savingGroups = reactive(new Set<number>())
const settings = reactive({ enabled: false, daily_budget_usd: 2 })
const groups = ref<ChannelMonitorV3Group[]>([])

function validBudget(value: number): boolean {
  return Number.isFinite(value) && value > 0
}

function validInterval(value: number): boolean {
  return Number.isInteger(value) && (value === 0 || (value >= 60 && value <= 86400))
}

function validModel(value: string): boolean {
  return value === '' || (value.length <= 256 && !/\s/.test(value))
}

async function load() {
  loading.value = true
  loadError.value = false
  try {
    const config = await channelMonitorV3API.getConfig()
    settings.enabled = config.settings.enabled
    settings.daily_budget_usd = config.settings.daily_budget_usd
    groups.value = config.groups.map((group) => ({ ...group }))
  } catch (error) {
    loadError.value = true
    appStore.showError(extractApiErrorMessage(error, t('admin.channelMonitorV3.loadError')))
  } finally {
    loading.value = false
  }
}

async function saveSettings() {
  if (!validBudget(settings.daily_budget_usd)) {
    appStore.showError(t('admin.channelMonitorV3.invalidBudget'))
    return
  }
  savingSettings.value = true
  try {
    const saved = await channelMonitorV3API.updateSettings({ ...settings })
    settings.enabled = saved.enabled
    settings.daily_budget_usd = saved.daily_budget_usd
    appStore.showSuccess(t('admin.channelMonitorV3.settingsSaved'))
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('admin.channelMonitorV3.saveError')))
  } finally {
    savingSettings.value = false
  }
}

async function saveGroup(group: ChannelMonitorV3Group) {
  if (!validInterval(group.probe_interval_seconds)) {
    appStore.showError(t('admin.channelMonitorV3.invalidInterval'))
    return
  }
  if (!validModel(group.test_model)) {
    appStore.showError(t('admin.channelMonitorV3.invalidModel'))
    return
  }
  savingGroups.add(group.group_id)
  try {
    const saved = await channelMonitorV3API.updateGroup(group.group_id, {
      visible: group.visible,
      probe_interval_seconds: group.probe_interval_seconds,
      test_model: group.test_model,
    })
    const index = groups.value.findIndex((item) => item.group_id === group.group_id)
    if (index >= 0) groups.value[index] = { ...saved }
    appStore.showSuccess(t('admin.channelMonitorV3.groupSaved'))
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('admin.channelMonitorV3.saveError')))
  } finally {
    savingGroups.delete(group.group_id)
  }
}

onMounted(load)
</script>
