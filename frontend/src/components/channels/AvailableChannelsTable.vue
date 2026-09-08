<template>
  <div class="table-wrapper available-channels-table-wrapper">
    <div v-if="loading" class="catalogue-empty">
      <Icon name="refresh" size="lg" class="animate-spin text-gray-400" />
    </div>

    <div v-else-if="displayRows.length === 0" class="catalogue-empty">
      <Icon name="inbox" size="xl" class="mb-3 h-12 w-12 text-gray-400" />
      <p class="text-sm text-gray-500 dark:text-gray-400">{{ emptyLabel }}</p>
    </div>

    <div v-else class="available-catalogue">
      <section
        v-for="category in categoryBlocks"
        :key="category.category"
        class="available-category"
      >
        <header class="available-category-header">
          <div class="flex items-center gap-2">
            <span class="category-icon" :class="categoryAccentClass(category.category)">
              <Icon :name="categoryIcon(category.category)" size="sm" />
            </span>
            <h2 class="text-sm font-semibold text-gray-800 dark:text-gray-100">{{ categoryLabel(category.category) }}</h2>
          </div>
          <span class="text-xs text-gray-400 dark:text-gray-500">
            {{ category.rows.length }} {{ t('availableChannels.summary.channels') }}
          </span>
        </header>

        <div class="available-category-columns" aria-hidden="true">
          <span>{{ columns.platform }}</span>
          <span>{{ columns.groups }}</span>
          <span>{{ columns.supportedModels }}</span>
        </div>

        <article
          v-for="row in category.rows"
          :key="`${row.channelName}-${row.section.platform}-${row.category}`"
          class="available-channel-row"
        >
          <div class="channel-identity">
            <span
              :class="[
                'platform-label',
                platformBadgeClass(row.section.platform),
              ]"
            >
              <PlatformIcon :platform="row.section.platform as GroupPlatform" size="xs" />
              {{ platformDisplayName(row.section.platform) }}
            </span>
            <div class="min-w-0">
              <h3 class="channel-name" :title="row.channelName">{{ row.channelName }}</h3>
              <p v-if="row.description" class="channel-description" :title="row.description">{{ row.description }}</p>
            </div>
          </div>

          <div class="channel-groups">
            <div v-if="exclusiveGroups(row.section).length > 0" class="group-line">
              <span
                class="group-kind group-kind-exclusive"
                :title="t('availableChannels.exclusiveTooltip')"
              >
                <Icon name="shield" size="xs" />
                {{ t('availableChannels.exclusive') }}
              </span>
              <div class="group-badges">
                <div v-for="group in exclusiveGroups(row.section)" :key="`ex-${group.id}`" class="group-badge-item">
                  <GroupBadge
                    :name="group.name"
                    :platform="group.platform as GroupPlatform"
                    :subscription-type="(group.subscription_type || 'standard') as SubscriptionType"
                    :rate-multiplier="group.rate_multiplier"
                    :user-rate-multiplier="userGroupRates[group.id] ?? null"
                    always-show-rate
                  />
                  <span
                    v-if="hasPeakRate(group)"
                    class="peak-label"
                    :title="peakRateTitle(group)"
                  >
                    <Icon name="clock" size="xs" />
                    {{ peakRateLabel(group) }}
                  </span>
                </div>
              </div>
            </div>
            <div v-if="publicGroups(row.section).length > 0" class="group-line">
              <span
                class="group-kind group-kind-public"
                :title="t('availableChannels.publicTooltip')"
              >
                <Icon name="globe" size="xs" />
                {{ t('availableChannels.public') }}
              </span>
              <div class="group-badges">
                <div v-for="group in publicGroups(row.section)" :key="`pub-${group.id}`" class="group-badge-item">
                  <GroupBadge
                    :name="group.name"
                    :platform="group.platform as GroupPlatform"
                    :subscription-type="(group.subscription_type || 'standard') as SubscriptionType"
                    :rate-multiplier="group.rate_multiplier"
                    :user-rate-multiplier="userGroupRates[group.id] ?? null"
                    always-show-rate
                  />
                  <span
                    v-if="hasPeakRate(group)"
                    class="peak-label"
                    :title="peakRateTitle(group)"
                  >
                    <Icon name="clock" size="xs" />
                    {{ peakRateLabel(group) }}
                  </span>
                </div>
              </div>
            </div>
            <span v-if="row.section.groups.length === 0" class="text-xs text-gray-400">-</span>
          </div>

          <div class="channel-models">
            <div class="model-chip-list">
              <SupportedModelChip
                v-for="model in row.models"
                :key="`${row.section.platform}-${model.name}`"
                :model="model"
                :pricing-key-prefix="pricingKeyPrefix"
                :no-pricing-label="noPricingLabel"
                :show-platform="false"
                :platform-hint="row.section.platform"
              />
              <span v-if="row.models.length === 0" class="text-xs text-gray-400">
                {{ noModelsLabel }}
              </span>
            </div>
          </div>
        </article>
      </section>
    </div>
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
    category?: string
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
  /** 用户专属倍率（group_id -> multiplier）；无专属时由 GroupBadge 仅显示默认倍率。 */
  userGroupRates: Record<number, number>
}>()

const { t } = useI18n()
const appStore = useAppStore()
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
  return platformLabel(category)
}

function categoryIcon(category: AvailableChannelCategory): 'chat' | 'terminal' | 'sparkles' | 'cloud' | 'cpu' | 'beaker' | 'grid' {
  switch (category) {
    case 'anthropic': return 'chat'
    case 'openai': return 'terminal'
    case 'gemini': return 'sparkles'
    case 'grok': return 'cloud'
    case 'kimi':
    case 'zhipu':
    case 'deepseek': return 'cpu'
    case 'antigravity': return 'beaker'
    default: return 'grid'
  }
}

function categoryAccentClass(category: AvailableChannelCategory): string {
  switch (category) {
    case 'anthropic': return 'text-orange-500'
    case 'openai': return 'text-emerald-500'
    case 'gemini': return 'text-blue-500'
    case 'grok': return 'text-zinc-700 dark:text-zinc-300'
    case 'kimi':
    case 'zhipu':
    case 'deepseek': return 'text-teal-500'
    case 'antigravity': return 'text-pink-500'
    default: return 'text-gray-400'
  }
}

function platformDisplayName(platform: string): string {
  return platformLabel(platform)
}

function exclusiveGroups(section: UserChannelPlatformSection): UserAvailableGroup[] {
  return section.groups.filter((group) => group.is_exclusive)
}

function publicGroups(section: UserChannelPlatformSection): UserAvailableGroup[] {
  return section.groups.filter((group) => !group.is_exclusive)
}

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

.available-catalogue {
  @apply divide-y divide-gray-200 dark:divide-dark-700;
}

.available-category {
  @apply bg-white dark:bg-dark-800;
}

.available-category-header {
  @apply flex items-center justify-between border-b border-gray-100 bg-gray-50/80 px-4 py-3 dark:border-dark-700 dark:bg-dark-900/30;
}

.category-icon {
  @apply flex h-7 w-7 items-center justify-center rounded-lg bg-white shadow-sm dark:bg-dark-800;
}

.available-category-columns {
  display: grid;
  grid-template-columns: minmax(220px, 0.95fr) minmax(250px, 1.15fr) minmax(300px, 1.7fr);
  @apply gap-4 border-b border-gray-100 px-4 py-2 text-[10px] font-semibold uppercase text-gray-400 dark:border-dark-700 dark:text-gray-500;
}

.available-channel-row {
  display: grid;
  grid-template-columns: minmax(220px, 0.95fr) minmax(250px, 1.15fr) minmax(300px, 1.7fr);
  @apply items-start gap-4 border-b border-gray-100 px-4 py-4 last:border-b-0 hover:bg-gray-50/50 dark:border-dark-700 dark:hover:bg-dark-900/20;
}

.channel-identity,
.channel-groups,
.channel-models {
  @apply min-w-0;
}

.channel-identity {
  @apply flex min-w-0 items-start gap-2.5;
}

.platform-label {
  @apply inline-flex flex-shrink-0 items-center gap-1 rounded-md border px-2 py-1 text-[10px] font-semibold uppercase;
}

.channel-name {
  @apply truncate text-sm font-semibold text-gray-800 dark:text-gray-100;
}

.channel-description {
  @apply mt-1 truncate text-[11px] text-gray-400 dark:text-gray-500;
}

.channel-groups {
  @apply space-y-2;
}

.group-line {
  @apply flex min-w-0 items-start gap-2;
}

.group-kind {
  @apply inline-flex flex-shrink-0 items-center gap-1 pt-1 text-[10px] font-medium;
}

.group-kind-exclusive {
  @apply text-purple-600 dark:text-purple-400;
}

.group-kind-public {
  @apply text-gray-500 dark:text-gray-400;
}

.group-badges,
.group-badge-item,
.model-chip-list {
  @apply flex min-w-0 flex-wrap items-center gap-1.5;
}

.group-badge-item {
  @apply items-center;
}

.peak-label {
  @apply inline-flex items-center gap-1 rounded-md bg-amber-50 px-1.5 py-0.5 text-[10px] font-medium text-amber-700 dark:bg-amber-900/20 dark:text-amber-300;
}

.model-chip-list :deep(.relative) {
  max-width: 100%;
}

.catalogue-empty {
  @apply flex min-h-64 flex-col items-center justify-center p-8 text-center;
}

@media (max-width: 1100px) {
  .available-category-columns,
  .available-channel-row {
    grid-template-columns: minmax(150px, 0.85fr) minmax(230px, 1.15fr);
  }

  .available-category-columns span:last-child,
  .channel-models {
    grid-column: 1 / -1;
  }
}

@media (max-width: 767px) {
  .available-category-columns {
    display: none;
  }

  .available-channel-row {
    display: block;
    @apply space-y-3 px-3 py-3;
  }

  .channel-groups,
  .channel-models {
    @apply border-t border-gray-100 pt-3 dark:border-dark-700;
  }

  .group-line {
    @apply flex-col gap-1;
  }

  .group-kind {
    @apply pt-0;
  }
}
</style>
