<template>
  <BaseDialog :show="show" :title="t('admin.accounts.modelSync.title')" width="extra-wide" @close="handleClose">
    <div class="space-y-3">
      <div class="space-y-2 text-sm text-gray-600 dark:text-gray-300">
        <span>{{ t('admin.accounts.modelSync.summary', { changed: changedCount, total: entries.length }) }}</span>
        <div class="flex flex-wrap items-center gap-3">
          <span class="font-medium">{{ t('admin.accounts.modelSync.modeLabel') }}</span>
          <label class="inline-flex items-center gap-1"><input v-model="mode" :disabled="applying" type="radio" value="sync" /> {{ t('admin.accounts.modelSync.modeSync') }}</label>
          <label class="inline-flex items-center gap-1"><input v-model="mode" :disabled="applying" type="radio" value="add" /> {{ t('admin.accounts.modelSync.modeAdd') }}</label>
        </div>
        <div class="flex justify-end">
        <button class="btn btn-primary btn-sm" :disabled="applying || selectedCount === 0" @click="apply">
          {{ applying ? t('admin.accounts.modelSync.applying') : t('admin.accounts.modelSync.apply', { count: selectedCount }) }}
        </button>
        </div>
      </div>
      <div class="max-h-[60vh] overflow-auto divide-y divide-gray-200 dark:divide-dark-700">
        <div v-for="entry in entries" :key="entry.account_id" class="py-3">
          <label class="flex items-start gap-3">
            <input v-if="entry.status === 'upstream' && entryState(entry.account_id) !== 'applied' && hasChanges(entry)" :disabled="applying" type="checkbox" class="mt-1" :checked="selected.has(entry.account_id)" @change="toggle(entry.account_id)" />
            <span class="min-w-0 flex-1">
              <span class="font-medium">{{ entry.account_name }} (#{{ entry.account_id }})</span>
              <span v-if="entryState(entry.account_id) === 'applied'" class="ml-2 text-emerald-600">{{ t('admin.accounts.modelSync.statusApplied') }}</span>
              <span v-else-if="entryState(entry.account_id) === 'conflict'" class="ml-2 text-amber-600">{{ t('admin.accounts.modelSync.statusConflict') }}</span>
              <span v-else-if="entryState(entry.account_id) === 'failed' || entry.status !== 'upstream'" class="ml-2 text-red-600">{{ entry.error || t('admin.accounts.modelSync.statusFailed') }}</span>
              <span v-else class="mt-1 block text-xs text-gray-500">+ {{ entry.added?.join(', ') || '-' }} · − {{ mode === 'sync' ? (entry.removed?.join(', ') || '-') : '-' }}</span>
            </span>
          </label>
        </div>
      </div>
    </div>
  </BaseDialog>
</template>
<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import { adminAPI } from '@/api/admin'
import type { AccountModelSyncPreviewEntry } from '@/api/admin/accounts'
import { useAppStore } from '@/stores/app'
const props = defineProps<{ show: boolean; entries: AccountModelSyncPreviewEntry[] }>()
const emit = defineEmits<{ (e: 'close'): void; (e: 'applied'): void }>()
const { t } = useI18n(); const appStore = useAppStore(); const selected = ref(new Set<number>()); const applying = ref(false); const mode = ref<'sync' | 'add'>('sync')
const resultStates = ref(new Map<number, string>())
const hasChanges = (e: AccountModelSyncPreviewEntry) => (e.added?.length ?? 0) > 0 || (mode.value === 'sync' && (e.removed?.length ?? 0) > 0)
const changedCount = computed(() => props.entries.filter(e => e.status === 'upstream' && hasChanges(e)).length)
const selectedCount = computed(() => props.entries.filter(e => selected.value.has(e.account_id) && e.status === 'upstream' && resultStates.value.get(e.account_id) !== 'applied' && hasChanges(e)).length)
watch(() => props.show, open => { if (open) { resultStates.value = new Map(); selected.value = new Set(props.entries.filter(e => e.status === 'upstream' && ((e.added?.length ?? 0) > 0 || (e.removed?.length ?? 0) > 0)).map(e => e.account_id)) } }, { immediate: true })
const entryState = (id: number) => resultStates.value.get(id) || ''
const handleClose = () => { if (!applying.value) emit('close') }
const toggle = (id: number) => { const next = new Set(selected.value); next.has(id) ? next.delete(id) : next.add(id); selected.value = next }
const apply = async () => {
  if (applying.value) return
  applying.value = true
  const applyMode = mode.value
  let applied = 0; let conflicts = 0; let failed = 0
  try {
    const pending = props.entries.filter(e => selected.value.has(e.account_id) && entryState(e.account_id) !== 'applied' && hasChanges(e))
    for (let i = 0; i < pending.length; i += 200) {
      const batch = pending.slice(i, i + 200).map(e => ({ account_id: e.account_id, version: e.version, models: e.upstream_models || [], mode: applyMode }))
      const result = await adminAPI.accounts.applyModelMappings(batch)
      for (const row of result.results) { resultStates.value.set(row.account_id, row.status); if (row.status === 'applied') applied++; else if (row.status === 'conflict') conflicts++; else failed++ }
    }
    selected.value = new Set([...selected.value].filter(id => entryState(id) !== 'applied'))
    appStore.showSuccess(t('admin.accounts.modelSync.applied', { count: applied, conflicts: conflicts + failed }))
    emit('applied')
  } catch { appStore.showError(t('admin.accounts.modelSync.applyFailed')) } finally { applying.value = false }
}
</script>
