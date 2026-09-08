<template>
  <BaseDialog
    :show="show"
    :title="t('admin.channelMonitor.bulkInterval.title')"
    width="normal"
    @close="emit('close')"
  >
    <form id="bulk-channel-monitor-interval-form" class="space-y-5" @submit.prevent="submit">
      <p class="text-sm text-gray-600 dark:text-gray-300">
        {{ t('admin.channelMonitor.bulkInterval.selectedCount', { count: selectedIds.length }) }}
      </p>

      <div class="space-y-2">
        <label for="bulk-channel-monitor-interval" class="input-label">
          {{ t('admin.channelMonitor.form.intervalSeconds') }}
        </label>
        <input
          id="bulk-channel-monitor-interval"
          v-model.number="intervalSeconds"
          type="number"
          min="15"
          max="3600"
          step="1"
          class="input"
          data-testid="bulk-channel-monitor-interval"
        />
        <div class="flex flex-wrap gap-2" :aria-label="t('admin.channelMonitor.bulkInterval.presetsLabel')">
          <button
            v-for="preset in presets"
            :key="preset"
            type="button"
            class="btn btn-secondary px-3 py-1.5 text-xs"
            :class="intervalSeconds === preset ? 'ring-2 ring-primary-500' : ''"
            @click="intervalSeconds = preset"
          >
            {{ preset }}s
          </button>
        </div>
        <p class="input-hint">
          {{ t('admin.channelMonitor.bulkInterval.hint') }}
        </p>
      </div>

      <p v-if="!validInterval" class="text-sm text-red-600 dark:text-red-400">
        {{ t('admin.channelMonitor.bulkInterval.invalid') }}
      </p>
    </form>

    <template #footer>
      <div class="flex justify-end gap-3">
        <button type="button" class="btn btn-secondary" @click="emit('close')">
          {{ t('common.cancel') }}
        </button>
        <button
          type="submit"
          form="bulk-channel-monitor-interval-form"
          class="btn btn-primary"
          :disabled="!canSubmit"
          data-testid="bulk-channel-monitor-interval-submit"
        >
          {{ submitting ? t('common.saving') : t('admin.channelMonitor.bulkInterval.apply') }}
        </button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import { adminAPI } from '@/api/admin'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'

const props = defineProps<{
  show: boolean
  selectedIds: number[]
}>()

const emit = defineEmits<{
  close: []
  success: [affected: number]
}>()

const { t } = useI18n()
const appStore = useAppStore()
const presets = [15, 30, 60, 300]
const intervalSeconds = ref<number>(15)
const submitting = ref(false)

const validInterval = computed(() => Number.isInteger(intervalSeconds.value)
  && intervalSeconds.value >= 15
  && intervalSeconds.value <= 3600)
const canSubmit = computed(() => props.selectedIds.length > 0 && validInterval.value && !submitting.value)

watch(() => props.show, (show) => {
  if (show) {
    intervalSeconds.value = 15
    submitting.value = false
  }
})

async function submit() {
  if (!canSubmit.value) return
  submitting.value = true
  try {
    const result = await adminAPI.channelMonitor.bulkUpdateInterval({
      monitor_ids: [...props.selectedIds],
      interval_seconds: intervalSeconds.value,
    })
    appStore.showSuccess(t('admin.channelMonitor.bulkInterval.success', { count: result.affected }))
    emit('success', result.affected)
    emit('close')
  } catch (error: unknown) {
    appStore.showError(extractApiErrorMessage(error, t('admin.channelMonitor.bulkInterval.failed')))
  } finally {
    submitting.value = false
  }
}
</script>
