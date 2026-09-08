<template>
  <BaseDialog :show="show" :title="t('admin.accounts.modelSync.title')" width="extra-wide" @close="$emit('close')">
    <div class="space-y-3">
      <div class="flex items-center justify-between text-sm text-gray-600 dark:text-gray-300">
        <span>{{ t('admin.accounts.modelSync.summary', { changed: changedCount, total: entries.length }) }}</span>
        <button class="btn btn-primary btn-sm" :disabled="applying || selected.size === 0" @click="apply">
          {{ applying ? t('admin.accounts.modelSync.applying') : t('admin.accounts.modelSync.apply', { count: selected.size }) }}
        </button>
      </div>
      <div class="max-h-[60vh] overflow-auto divide-y divide-gray-200 dark:divide-dark-700">
        <div v-for="entry in entries" :key="entry.account_id" class="py-3">
          <label class="flex items-start gap-3">
            <input v-if="entry.status === 'upstream' && (entry.added?.length || entry.removed?.length)" type="checkbox" class="mt-1" :checked="selected.has(entry.account_id)" @change="toggle(entry.account_id)" />
            <span class="min-w-0 flex-1">
              <span class="font-medium">{{ entry.account_name }} (#{{ entry.account_id }})</span>
              <span v-if="entry.status !== 'upstream'" class="ml-2 text-red-600">{{ entry.error || entry.status }}</span>
              <span v-else class="mt-1 block text-xs text-gray-500">+ {{ entry.added?.join(', ') || '-' }} · − {{ entry.removed?.join(', ') || '-' }}</span>
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
const { t } = useI18n(); const appStore = useAppStore(); const selected = ref(new Set<number>()); const applying = ref(false)
const changedCount = computed(() => props.entries.filter(e => e.status === 'upstream' && ((e.added?.length ?? 0) > 0 || (e.removed?.length ?? 0) > 0)).length)
watch(() => props.show, open => { if (open) selected.value = new Set(props.entries.filter(e => e.status === 'upstream' && ((e.added?.length ?? 0) > 0 || (e.removed?.length ?? 0) > 0)).map(e => e.account_id)) })
const toggle = (id: number) => { const next = new Set(selected.value); next.has(id) ? next.delete(id) : next.add(id); selected.value = next }
const apply = async () => { applying.value = true; try { const items = props.entries.filter(e => selected.value.has(e.account_id)).map(e => ({ account_id: e.account_id, version: e.version })); const result = await adminAPI.accounts.applyModelMappings(items); const conflicts = result.results.filter(r => r.status !== 'applied').length; appStore.showSuccess(t('admin.accounts.modelSync.applied', { count: items.length - conflicts, conflicts })); emit('applied'); emit('close') } catch { appStore.showError(t('admin.accounts.modelSync.applyFailed')) } finally { applying.value = false } }
</script>
