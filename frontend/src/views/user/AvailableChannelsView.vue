<template>
  <AppLayout>
    <TablePageLayout>
      <template #filters>
        <div class="space-y-4">
          <CatalogSurfaceNav current="channels" />

          <div class="flex flex-col justify-between gap-3 xl:flex-row xl:items-end">
            <div class="flex min-w-0 flex-1 flex-wrap items-end gap-3">
              <label class="relative w-full sm:w-80">
                <span class="sr-only">{{ t('availableChannels.searchLabel') }}</span>
                <Icon
                  name="search"
                  size="md"
                  class="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-gray-400 dark:text-gray-500"
                />
                <input
                  v-model="searchQuery"
                  type="text"
                  :placeholder="t('availableChannels.searchPlaceholder')"
                  class="input pl-10 pr-9"
                />
                <button
                  v-if="searchQuery"
                  type="button"
                  class="absolute right-2.5 top-1/2 -translate-y-1/2 text-gray-400 hover:text-gray-700 dark:text-dark-400 dark:hover:text-white"
                  :aria-label="t('availableChannels.clearSearch')"
                  @click="searchQuery = ''"
                >
                  <Icon name="x" size="xs" />
                </button>
              </label>

              <label class="w-full sm:w-44">
                <span class="mb-1 block text-[11px] font-semibold uppercase tracking-wide text-gray-400 dark:text-dark-500">
                  {{ t('availableChannels.filters.platform') }}
                </span>
                <select v-model="selectedPlatform" class="input py-2">
                  <option value="all">{{ t('availableChannels.filters.allPlatforms') }}</option>
                  <option v-for="platform in platforms" :key="platform" :value="platform">
                    {{ platform }}
                  </option>
                </select>
              </label>

              <label class="w-full sm:w-44">
                <span class="mb-1 block text-[11px] font-semibold uppercase tracking-wide text-gray-400 dark:text-dark-500">
                  {{ t('availableChannels.filters.access') }}
                </span>
                <select v-model="selectedAccess" class="input py-2">
                  <option value="all">{{ t('availableChannels.filters.allAccess') }}</option>
                  <option value="public">{{ t('availableChannels.filters.public') }}</option>
                  <option value="exclusive">{{ t('availableChannels.filters.exclusive') }}</option>
                </select>
              </label>
            </div>

            <div class="flex w-full flex-shrink-0 items-center gap-2 xl:w-auto xl:justify-end">
              <button
                v-if="filtersActive"
                type="button"
                class="btn btn-ghost gap-1.5 text-gray-500 dark:text-dark-300"
                @click="clearFilters"
              >
                <Icon name="x" size="sm" />
                {{ t('availableChannels.clearFilters') }}
              </button>
              <button
                type="button"
                @click="loadChannels"
                :disabled="loading"
                class="btn btn-secondary"
                :title="t('common.refresh', 'Refresh')"
              >
                <Icon name="refresh" size="md" :class="loading ? 'animate-spin' : ''" />
              </button>
            </div>
          </div>

          <dl
            data-testid="available-summary"
            class="grid grid-cols-2 divide-x divide-gray-200 border-y border-gray-200 py-2.5 dark:divide-dark-700 dark:border-dark-700 sm:grid-cols-4"
          >
            <div class="px-3 first:pl-0 sm:px-4">
              <dt class="text-[11px] font-medium uppercase tracking-wide text-gray-400 dark:text-dark-500">
                {{ t('availableChannels.summary.channels') }}
              </dt>
              <dd class="mt-0.5 text-lg font-semibold text-gray-900 dark:text-white">{{ summary.channels }}</dd>
            </div>
            <div class="px-3 sm:px-4">
              <dt class="text-[11px] font-medium uppercase tracking-wide text-gray-400 dark:text-dark-500">
                {{ t('availableChannels.summary.platforms') }}
              </dt>
              <dd class="mt-0.5 text-lg font-semibold text-gray-900 dark:text-white">{{ summary.platforms }}</dd>
            </div>
            <div class="px-3 sm:px-4">
              <dt class="text-[11px] font-medium uppercase tracking-wide text-gray-400 dark:text-dark-500">
                {{ t('availableChannels.summary.groups') }}
              </dt>
              <dd class="mt-0.5 text-lg font-semibold text-gray-900 dark:text-white">{{ summary.groups }}</dd>
            </div>
            <div class="px-3 last:pr-0 sm:px-4">
              <dt class="text-[11px] font-medium uppercase tracking-wide text-gray-400 dark:text-dark-500">
                {{ t('availableChannels.summary.models') }}
              </dt>
              <dd class="mt-0.5 text-lg font-semibold text-gray-900 dark:text-white">{{ summary.models }}</dd>
            </div>
          </dl>
        </div>
      </template>

      <template #table>
        <AvailableChannelsTable
          :columns="columnLabels"
          :rows="filteredChannels"
          :loading="loading"
          :user-group-rates="userGroupRates"
          pricing-key-prefix="availableChannels.pricing"
          :no-pricing-label="t('availableChannels.noPricing')"
          :no-models-label="t('availableChannels.noModels')"
          :empty-label="t('availableChannels.empty')"
        />
      </template>
    </TablePageLayout>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import TablePageLayout from '@/components/layout/TablePageLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import CatalogSurfaceNav from '@/components/catalog/CatalogSurfaceNav.vue'
import AvailableChannelsTable from '@/components/channels/AvailableChannelsTable.vue'
import userChannelsAPI, { type UserAvailableChannel } from '@/api/channels'
import userGroupsAPI from '@/api/groups'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'
import { filterAvailableChannels, summarizeAvailableChannels } from '@/utils/availableChannelsCatalog'

const { t } = useI18n()
const appStore = useAppStore()

const channels = ref<UserAvailableChannel[]>([])
const userGroupRates = ref<Record<number, number>>({})
const loading = ref(false)
const searchQuery = ref('')
const selectedPlatform = ref('all')
const selectedAccess = ref<'all' | 'public' | 'exclusive'>('all')

const columnLabels = computed(() => ({
  name: t('availableChannels.columns.name'),
  description: t('availableChannels.columns.description'),
  platform: t('availableChannels.columns.platform'),
  groups: t('availableChannels.columns.groups'),
  supportedModels: t('availableChannels.columns.supportedModels'),
}))

const platforms = computed(() =>
  [...new Set(channels.value.flatMap((channel) => channel.platforms.map((section) => section.platform)))].sort()
)

const filtersActive = computed(
  () => searchQuery.value.trim() !== '' || selectedPlatform.value !== 'all' || selectedAccess.value !== 'all'
)

/**
 * 渠道筛选保留原有聚合结构：命中渠道名/描述时保留其全部子项，
 * 命中平台/分组/模型时只保留相关 section，方便用户看到完整的上下文。
 */
const filteredChannels = computed(() => {
  return filterAvailableChannels(channels.value, {
    query: searchQuery.value,
    platform: selectedPlatform.value,
    access: selectedAccess.value
  })
})

const summary = computed(() => summarizeAvailableChannels(filteredChannels.value))

function clearFilters() {
  searchQuery.value = ''
  selectedPlatform.value = 'all'
  selectedAccess.value = 'all'
}

async function loadChannels() {
  loading.value = true
  try {
    // 渠道列表和用户专属倍率并发拉取。专属倍率失败不阻塞渠道展示——
    // 失败时只是无法渲染专属倍率角标，降级为仅显示默认倍率。
    const [list, rates] = await Promise.all([
      userChannelsAPI.getAvailable(),
      userGroupsAPI.getUserGroupRates().catch((err: unknown) => {
        console.error('Failed to load user group rates:', err)
        return {} as Record<number, number>
      }),
    ])
    channels.value = list
    userGroupRates.value = rates
  } catch (err: unknown) {
    appStore.showError(extractApiErrorMessage(err, t('common.error')))
  } finally {
    loading.value = false
  }
}

onMounted(loadChannels)
</script>
