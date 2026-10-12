<template>
  <BaseDialog :show="show" :title="t('admin.accounts.priorityConversion.title')" width="wide"
    :show-close-button="!saving" :close-on-escape="!saving" @close="close">
    <p class="mb-4 text-sm text-gray-600 dark:text-dark-300">{{ t('admin.accounts.priorityConversion.description') }}</p>
    <p v-if="loading" role="status">{{ t('common.loading') }}</p>
    <p v-if="error" role="alert" class="mb-3 text-sm text-red-600">{{ error }}</p>
    <div v-if="rows.length" class="max-h-96 overflow-auto">
      <table class="w-full text-left text-sm">
        <thead><tr>
          <th class="p-2">{{ t('admin.accounts.columns.name') }}</th>
          <th class="p-2">{{ t('admin.accounts.priorityConversion.multiplier') }}</th>
          <th class="p-2">{{ t('admin.accounts.priorityConversion.oldPriority') }}</th>
          <th class="p-2">{{ t('admin.accounts.priorityConversion.newPriority') }}</th>
          <th class="p-2">{{ t('admin.accounts.priorityConversion.result') }}</th>
        </tr></thead>
        <tbody><tr v-for="row in rows" :key="row.account_id" class="border-t border-gray-100 dark:border-dark-700">
          <td class="p-2">{{ row.name || `#${row.account_id}` }}</td>
          <td class="p-2">{{ row.rate_multiplier ?? '—' }}</td>
          <td class="p-2">{{ row.old_priority }}</td>
          <td class="p-2">{{ row.new_priority ?? '—' }}</td>
          <td class="p-2" :class="row.reason ? 'text-red-600' : ''">
            {{ row.reason ? t(`admin.accounts.priorityConversion.reasons.${row.reason}`) : row.applied ? t('admin.accounts.priorityConversion.applied') : '—' }}
          </td>
        </tr></tbody>
      </table>
    </div>
    <p v-if="completed" role="status" class="mt-3 text-sm">{{ t('admin.accounts.priorityConversion.summary', { count: updated }) }}</p>
    <template #footer>
      <button class="btn btn-secondary" :disabled="saving" @click="close">{{ completed ? t('common.close') : t('common.cancel') }}</button>
      <button v-if="!completed" class="btn btn-primary" :disabled="loading || saving || !eligible.length" @click="apply">
        {{ saving ? t('common.saving') : t('admin.accounts.priorityConversion.confirm') }}
      </button>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import { previewPriorityConversion, applyPriorityConversion, type PriorityConversionRow } from '@/api/admin/accounts'

const props = defineProps<{ show: boolean; accountIds: number[] }>()
const emit = defineEmits<{ close: []; updated: [] }>()
const { t } = useI18n()
const rows = ref<PriorityConversionRow[]>([])
const loading = ref(false)
const saving = ref(false)
const error = ref('')
const completed = ref(false)
const updated = ref(0)
const eligible = computed(() => rows.value.filter(row => !row.reason && row.new_priority !== null))
let generation = 0

watch(() => props.show, async show => {
  const current = ++generation
  if (!show) return
  rows.value = []; error.value = ''; completed.value = false; updated.value = 0
  loading.value = true
  try {
    const result = await previewPriorityConversion([...props.accountIds])
    if (current === generation && props.show) rows.value = result.rows
  } catch {
    if (current === generation && props.show) error.value = t('admin.accounts.priorityConversion.previewFailed')
  } finally {
    if (current === generation) loading.value = false
  }
}, { immediate: true })

function close() {
  if (!saving.value) emit('close')
}

async function apply() {
  if (saving.value || loading.value || !eligible.value.length || completed.value) return
  saving.value = true; error.value = ''
  try {
    const result = await applyPriorityConversion(eligible.value)
    const results = new Map(result.rows.map(row => [row.account_id, row]))
    rows.value = rows.value.map(row => results.get(row.account_id) ?? row)
    updated.value = result.updated; completed.value = true
    if (result.updated > 0) emit('updated')
  } catch {
    // An interrupted response may follow a committed write. Require a fresh
    // preview; do not automatically repeat an ambiguous confirmation.
    error.value = t('admin.accounts.priorityConversion.applyFailed')
    completed.value = true
  } finally {
    saving.value = false
  }
}
</script>
