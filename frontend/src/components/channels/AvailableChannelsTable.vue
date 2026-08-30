<template>
  <div class="available-channels-table-wrapper">
    <table class="available-channels-table border-collapse text-sm">
      <thead class="sticky top-0 z-10">
        <tr class="border-b border-gray-100 bg-gray-50/50 text-xs font-medium uppercase tracking-wide text-gray-500 dark:border-dark-700 dark:bg-dark-800/50 dark:text-gray-400">
          <th class="w-[150px] px-4 py-3 text-left">{{ columns.category }}</th>
          <th class="w-[170px] px-4 py-3 text-left">{{ columns.platform }}</th>
          <th class="w-[390px] px-4 py-3 text-left">{{ columns.groups }}</th>
          <th class="px-4 py-3 text-left">{{ columns.supportedModels }}</th>
        </tr>
      </thead>
      <tbody v-if="loading">
        <tr>
          <td colspan="4" class="py-10 text-center">
            <Icon name="refresh" size="lg" class="inline-block animate-spin text-gray-400" />
          </td>
        </tr>
      </tbody>
      <tbody v-else-if="rows.length === 0">
        <tr>
          <td colspan="4" class="py-12 text-center">
            <Icon name="inbox" size="xl" class="mx-auto mb-3 h-12 w-12 text-gray-400" />
            <p class="text-sm text-gray-500 dark:text-gray-400">{{ emptyLabel }}</p>
          </td>
        </tr>
      </tbody>
      <!-- Each category is a visually grouped block, matching the reference
           catalogue while keeping channel context in the group column. -->
      <tbody
        v-else
        v-for="category in categoryBlocks"
        :key="category.category"
        class="border-b-2 border-gray-200 last:border-b-0 dark:border-dark-600"
      >
        <tr
          v-for="(row, rowIdx) in category.rows"
          :key="`${row.channelName}-${row.section.platform}-${row.category}`"
          class="transition-colors hover:bg-gray-50/40 dark:hover:bg-dark-800/40"
          :class="{ 'border-t border-gray-100/70 dark:border-dark-700/50': rowIdx > 0 }"
        >
          <!-- Category is the primary grouping, like the reference page. -->
          <td
            v-if="rowIdx === 0"
            :rowspan="category.rows.length"
            class="px-4 py-3 align-middle font-semibold text-gray-700 dark:text-gray-200"
          >
            <span class="inline-flex items-center gap-2">
              <Icon :name="categoryIcon(category.category)" size="sm" :class="categoryAccentClass(category.category)" />
              {{ categoryLabel(category.category) }}
            </span>
          </td>

          <!-- 平台徽章 -->
          <td class="align-top px-4 py-3">
            <span
              :class="[
                'inline-flex items-center gap-1 rounded-md border px-2 py-0.5 text-[11px] font-medium uppercase',
                platformBadgeClass(row.section.platform),
              ]"
            >
              <PlatformIcon :platform="row.section.platform as GroupPlatform" size="xs" />
              {{ platformDisplayName(row.section.platform) }}
            </span>
          </td>

          <!-- Group badges retain the existing public/exclusive and rate semantics. -->
          <td class="align-top px-4 py-3">
            <div class="flex flex-col gap-2">
              <div class="min-w-0">
                <div class="truncate text-xs font-medium text-gray-800 dark:text-gray-200" :title="row.channelName">
                  {{ row.channelName }}
                </div>
                <div v-if="row.description" class="truncate text-[11px] text-gray-400 dark:text-gray-500" :title="row.description">
                  {{ row.description }}
                </div>
              </div>
              <div class="flex flex-col gap-1.5">
              <div
                v-if="exclusiveGroups(row.section).length > 0"
                class="flex flex-wrap items-center gap-1.5"
              >
                <span
                  class="inline-flex items-center gap-0.5 text-[10px] font-medium uppercase text-purple-600 dark:text-purple-400"
                  :title="t('availableChannels.exclusiveTooltip')"
                >
                  <Icon name="shield" size="xs" class="h-3 w-3" />
                  {{ t('availableChannels.exclusive') }}
                </span>
                <div
                  v-for="g in exclusiveGroups(row.section)"
                  :key="`ex-${g.id}`"
                  class="inline-flex flex-wrap items-center gap-1"
                >
                  <GroupBadge
                    :name="g.name"
                    :platform="g.platform as GroupPlatform"
                    :subscription-type="(g.subscription_type || 'standard') as SubscriptionType"
                    :rate-multiplier="g.rate_multiplier"
                    :user-rate-multiplier="userGroupRates[g.id] ?? null"
                    always-show-rate
                  />
                  <span
                    v-if="hasPeakRate(g)"
                    class="inline-flex items-center gap-1 rounded-md bg-amber-50 px-1.5 py-0.5 text-[10px] font-medium text-amber-700 dark:bg-amber-900/20 dark:text-amber-300"
                    :title="peakRateTitle(g)"
                  >
                    <Icon name="clock" size="xs" class="h-3 w-3" />
                    {{ peakRateLabel(g) }}
                  </span>
                </div>
              </div>
              <div
                v-if="publicGroups(row.section).length > 0"
                class="flex flex-wrap items-center gap-1.5"
              >
                <span
                  class="inline-flex items-center gap-0.5 text-[10px] font-medium uppercase text-gray-500 dark:text-gray-400"
                  :title="t('availableChannels.publicTooltip')"
                >
                  <Icon name="globe" size="xs" class="h-3 w-3" />
                  {{ t('availableChannels.public') }}
                </span>
                <div
                  v-for="g in publicGroups(row.section)"
                  :key="`pub-${g.id}`"
                  class="inline-flex flex-wrap items-center gap-1"
                >
                  <GroupBadge
                    :name="g.name"
                    :platform="g.platform as GroupPlatform"
                    :subscription-type="(g.subscription_type || 'standard') as SubscriptionType"
                    :rate-multiplier="g.rate_multiplier"
                    :user-rate-multiplier="userGroupRates[g.id] ?? null"
                    always-show-rate
                  />
                  <span
                    v-if="hasPeakRate(g)"
                    class="inline-flex items-center gap-1 rounded-md bg-amber-50 px-1.5 py-0.5 text-[10px] font-medium text-amber-700 dark:bg-amber-900/20 dark:text-amber-300"
                    :title="peakRateTitle(g)"
                  >
                    <Icon name="clock" size="xs" class="h-3 w-3" />
                    {{ peakRateLabel(g) }}
                  </span>
                </div>
              </div>
              <span v-if="row.section.groups.length === 0" class="text-xs text-gray-400">-</span>
              </div>
            </div>
          </td>

          <!-- 支持模型 -->
          <td class="align-top px-4 py-3">
            <div class="flex flex-wrap gap-1">
              <SupportedModelChip
                v-for="m in row.models"
                :key="`${row.section.platform}-${m.name}`"
                :model="m"
                :pricing-key-prefix="pricingKeyPrefix"
                :no-pricing-label="noPricingLabel"
                :show-platform="false"
                :platform-hint="row.section.platform"
              />
              <span v-if="row.models.length === 0" class="text-xs text-gray-400">
                {{ noModelsLabel }}
              </span>
            </div>
          </td>
        </tr>
      </tbody>
    </table>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import PlatformIcon from '@/components/common/PlatformIcon.vue'
import GroupBadge from '@/components/common/GroupBadge.vue'
import SupportedModelChip from './SupportedModelChip.vue'
import type { UserAvailableChannel, UserAvailableGroup, UserChannelPlatformSection } from '@/api/channels'
import type { GroupPlatform, SubscriptionType } from '@/types'
import { platformBadgeClass, platformLabel } from '@/utils/platformColors'
import { useAppStore } from '@/stores/app'
import { hasPeakRate as groupHasPeakRate, formatPeakRateWindow, serverTimezoneLabel } from '@/utils/peak-rate'
import { buildAvailableChannelDisplayRows, type AvailableChannelCategory, type AvailableChannelDisplayRow } from '@/utils/availableChannels'

const props = defineProps<{
  columns: {
    category: string
    platform: string
    groups: string
    supportedModels: string
  }
  rows: UserAvailableChannel[]
  loading: boolean
  pricingKeyPrefix: string
  noPricingLabel: string
  noModelsLabel: string
  emptyLabel: string
  /** 用户专属倍率（group_id → multiplier）；无专属时由 GroupBadge 仅显示默认倍率。 */
  userGroupRates: Record<number, number>
}>()

// Suppress unused warning — props is accessed via template automatically but
// the explicit reference here keeps the linter from flagging userGroupRates.
void props.userGroupRates

const { t } = useI18n()

const displayRows = computed(() => buildAvailableChannelDisplayRows(props.rows))
const categoryBlocks = computed(() => {
  const blocks = new Map<AvailableChannelCategory, AvailableChannelDisplayRow[]>()
  for (const row of displayRows.value) {
    const rows = blocks.get(row.category) || []
    rows.push(row)
    blocks.set(row.category, rows)
  }
  return Array.from(blocks, ([category, rows]) => ({ category, rows }))
})

function categoryLabel(category: AvailableChannelCategory): string {
  return t(`availableChannels.categories.${category}`)
}

function categoryIcon(category: AvailableChannelCategory): 'chat' | 'terminal' | 'sparkles' | 'cloud' | 'cpu' | 'beaker' | 'grid' {
  switch (category) {
    case 'claude': return 'chat'
    case 'codex': return 'terminal'
    case 'gemini': return 'sparkles'
    case 'grok': return 'cloud'
    case 'domestic': return 'cpu'
    case 'image': return 'beaker'
    default: return 'grid'
  }
}

function categoryAccentClass(category: AvailableChannelCategory): string {
  switch (category) {
    case 'claude': return 'text-orange-500'
    case 'codex': return 'text-emerald-500'
    case 'gemini': return 'text-blue-500'
    case 'grok': return 'text-zinc-700 dark:text-zinc-300'
    case 'domestic': return 'text-teal-500'
    case 'image': return 'text-pink-500'
    default: return 'text-gray-400'
  }
}

function platformDisplayName(platform: string): string {
  return platformLabel(platform)
}

function exclusiveGroups(section: UserChannelPlatformSection): UserAvailableGroup[] {
  return section.groups.filter((g) => g.is_exclusive)
}

function publicGroups(section: UserChannelPlatformSection): UserAvailableGroup[] {
  return section.groups.filter((g) => !g.is_exclusive)
}

const appStore = useAppStore()

function hasPeakRate(group: UserAvailableGroup): boolean {
  return groupHasPeakRate(group)
}

function peakRateLabel(group: UserAvailableGroup): string {
  return formatPeakRateWindow(group, serverTimezoneLabel(appStore.cachedPublicSettings?.server_utc_offset))
}

function peakRateTitle(group: UserAvailableGroup): string {
  return t('common.peakRateTooltip', { window: peakRateLabel(group) }) + t('common.peakRateImageNote')
}
</script>

<style scoped>
.available-channels-table-wrapper {
  @apply min-h-0 flex-1 overflow-auto;
  scrollbar-gutter: stable;
}

.available-channels-table {
  @apply w-full;
  min-width: 1120px !important;
}

@media (max-width: 1023px) {
  .available-channels-table-wrapper {
    @apply overflow-x-auto overflow-y-auto;
  }

  .available-channels-table {
    min-width: 960px !important;
  }
}
</style>
