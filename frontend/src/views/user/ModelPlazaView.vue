<template>
  <AppLayout>
    <div class="model-plaza space-y-5">
      <header class="flex flex-col justify-between gap-4 lg:flex-row lg:items-start">
        <div class="flex min-w-0 items-start gap-3">
          <div class="flex h-11 w-11 flex-shrink-0 items-center justify-center rounded-xl bg-primary-50 text-primary-600 dark:bg-primary-900/30 dark:text-primary-400">
            <Icon name="grid" size="lg" />
          </div>
          <div class="min-w-0">
            <h1 class="text-xl font-semibold text-gray-900 dark:text-white">{{ t('modelPlaza.title') }}</h1>
            <p class="mt-1 max-w-3xl text-sm text-gray-500 dark:text-gray-400">{{ t('modelPlaza.description') }}</p>
          </div>
        </div>

        <div class="flex flex-wrap items-center gap-3">
          <div class="model-plaza-summary" :aria-label="t('modelPlaza.summaryLabel')">
            <span><strong>{{ models.length }}</strong> {{ t('modelPlaza.stats.models') }}</span>
            <span><strong>{{ groups.length }}</strong> {{ t('modelPlaza.stats.groups') }}</span>
            <span><strong>{{ channelCount }}</strong> {{ t('modelPlaza.stats.channels') }}</span>
          </div>
          <button
            type="button"
            class="btn btn-secondary flex-shrink-0"
            :disabled="loading"
            :title="t('common.refresh')"
            @click="loadModels"
          >
            <Icon name="refresh" size="md" :class="loading ? 'animate-spin' : ''" />
            <span class="hidden sm:inline">{{ t('common.refresh') }}</span>
          </button>
        </div>
      </header>

      <section class="catalogue-filters" :aria-label="t('modelPlaza.filterLabel')">
        <div class="relative w-full lg:max-w-md">
          <Icon
            name="search"
            size="md"
            class="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-gray-400 dark:text-gray-500"
          />
          <input
            v-model="query"
            type="search"
            class="input w-full pl-10"
            :placeholder="t('modelPlaza.searchPlaceholder')"
            :aria-label="t('modelPlaza.searchPlaceholder')"
          />
        </div>

        <div class="filter-row" role="tablist" :aria-label="t('modelPlaza.platformFilter')">
          <span class="filter-label">{{ t('modelPlaza.platform') }}</span>
          <button
            v-for="option in platformOptions"
            :key="option.value"
            type="button"
            role="tab"
            class="filter-pill"
            :class="{ 'filter-pill-active': selectedPlatform === option.value }"
            :aria-selected="selectedPlatform === option.value"
            @click="selectedPlatform = option.value"
          >
            {{ option.label }}
            <span class="filter-count">{{ option.count }}</span>
          </button>
        </div>

        <div class="filter-row" role="tablist" :aria-label="t('modelPlaza.groupFilter')">
          <span class="filter-label">{{ t('modelPlaza.group') }}</span>
          <button
            v-for="option in groupOptions"
            :key="option.value"
            type="button"
            role="tab"
            class="filter-pill"
            :class="{ 'filter-pill-active': selectedGroup === option.value }"
            :aria-selected="selectedGroup === option.value"
            @click="selectedGroup = option.value"
          >
            {{ option.label }}
            <span v-if="option.rate" class="filter-rate">{{ option.rate }}</span>
          </button>
        </div>

        <div class="filter-row" role="tablist" :aria-label="t('modelPlaza.rateFilter')">
          <span class="filter-label">{{ t('modelPlaza.rate') }}</span>
          <button
            v-for="option in rateOptions"
            :key="option.value"
            type="button"
            role="tab"
            class="filter-pill filter-pill-rate"
            :class="{ 'filter-pill-active': selectedRate === option.value }"
            :aria-selected="selectedRate === option.value"
            @click="selectedRate = option.value"
          >
            {{ option.label }}
          </button>
        </div>

        <div class="filter-row" role="tablist" :aria-label="t('modelPlaza.categoryFilter')">
          <span class="filter-label">{{ t('modelPlaza.category') }}</span>
          <button
            v-for="category in categories"
            :key="category"
            type="button"
            role="tab"
            class="filter-pill"
            :class="{ 'filter-pill-active': selectedCategory === category }"
            :aria-selected="selectedCategory === category"
            @click="selectedCategory = category"
          >
            {{ categoryLabel(category) }}
          </button>
        </div>
      </section>

      <div v-if="loading" class="catalogue-empty">
        <Icon name="refresh" size="xl" class="animate-spin text-gray-400" />
      </div>

      <section v-else-if="filteredGroups.length > 0" class="space-y-4">
        <article
          v-for="group in filteredGroups"
          :key="group.key"
          class="catalogue-group"
        >
          <header class="catalogue-group-header">
            <div class="min-w-0">
              <div class="flex flex-wrap items-center gap-2">
                <GroupBadge
                  v-if="group.id !== null"
                  :name="group.name"
                  :platform="group.platform as GroupPlatform"
                  :subscription-type="group.subscriptionType as SubscriptionType"
                  :rate-multiplier="group.rateMultiplier ?? undefined"
                  :user-rate-multiplier="null"
                  :peak-rate-enabled="group.peakRateEnabled"
                  :peak-start="group.peakStart"
                  :peak-end="group.peakEnd"
                  :peak-rate-multiplier="group.peakRateMultiplier"
                  always-show-rate
                />
                <span v-else class="ungrouped-label">{{ group.name }}</span>
                <span
                  :class="[
                    'platform-label',
                    platformBadgeClass(group.platform),
                  ]"
                >
                  <PlatformIcon :platform="group.platform as GroupPlatform" size="xs" />
                  {{ platformLabel(group.platform) }}
                </span>
                <span v-if="group.isExclusive" class="scope-label scope-label-exclusive">
                  <Icon name="shield" size="xs" />
                  {{ t('availableChannels.exclusive') }}
                </span>
              </div>
              <p class="mt-2 truncate text-xs text-gray-500 dark:text-gray-400" :title="group.channelNames.join(', ')">
                {{ group.channelNames.join(' · ') || t('modelPlaza.noChannel') }}
              </p>
            </div>

            <div class="flex flex-shrink-0 items-center gap-3 text-xs text-gray-500 dark:text-gray-400">
              <span><strong class="text-gray-800 dark:text-gray-200">{{ group.models.length }}</strong> {{ t('modelPlaza.modelsInGroup') }}</span>
              <span v-if="group.rateMultiplier !== null" class="rate-emphasis">{{ formatRate(group.rateMultiplier) }}</span>
            </div>
          </header>

          <div class="price-matrix-wrap">
            <div class="price-grid price-grid-header">
              <div>{{ t('modelPlaza.model') }}</div>
              <div>{{ t('modelPlaza.input') }}</div>
              <div>{{ t('modelPlaza.output') }}</div>
              <div>{{ t('modelPlaza.cacheRead') }}</div>
              <div>{{ t('modelPlaza.cacheWrite') }}</div>
              <div>{{ t('modelPlaza.billing') }}</div>
            </div>

            <div
              v-for="model in group.models"
              :key="`${group.key}-${model.key}`"
              class="model-price-row"
              :class="{ 'model-price-row-expanded': isExpanded(rowKey(group, model)) }"
            >
              <div class="price-grid model-price-grid">
                <div class="model-name-cell" data-label="Model">
                  <div class="flex min-w-0 items-start gap-2">
                    <div class="model-icon-small" :class="platformBorderClass(model.platform)">
                      <ModelIcon :model="model.name" size="18px" />
                    </div>
                    <div class="min-w-0 flex-1">
                      <div class="flex min-w-0 items-center gap-1.5">
                        <span class="model-name" :title="model.name">{{ model.name }}</span>
                        <span v-if="model.channels.length > 1" class="source-count">{{ model.channels.length }}x</span>
                      </div>
                      <div class="mt-1 flex flex-wrap items-center gap-1.5">
                        <span class="model-category">{{ categoryLabel(model.category) }}</span>
                        <span v-if="model.pricing?.intervals.length" class="tier-label">{{ t('modelPlaza.tiered') }}</span>
                      </div>
                    </div>
                    <div class="flex flex-shrink-0 items-center gap-0.5">
                      <button
                        type="button"
                        class="icon-button"
                        :title="t('modelPlaza.copyModel')"
                        :aria-label="t('modelPlaza.copyModel')"
                        @click="copyModel(model.name)"
                      >
                        <Icon name="copy" size="sm" />
                      </button>
                      <button
                        type="button"
                        class="icon-button"
                        :title="isExpanded(rowKey(group, model)) ? t('modelPlaza.collapse') : t('modelPlaza.expand')"
                        :aria-label="isExpanded(rowKey(group, model)) ? t('modelPlaza.collapse') : t('modelPlaza.expand')"
                        :aria-expanded="isExpanded(rowKey(group, model))"
                        @click="toggleExpanded(rowKey(group, model))"
                      >
                        <Icon :name="isExpanded(rowKey(group, model)) ? 'chevronUp' : 'chevronDown'" size="sm" />
                      </button>
                    </div>
                  </div>
                </div>
                <div
                  v-for="field in pricingFields"
                  :key="field.key"
                  class="price-cell"
                  :data-label="field.label"
                >
                  <div v-if="priceLines(model.pricing, field.key).length > 1" class="price-stack">
                    <div
                      v-for="line in priceLines(model.pricing, field.key)"
                      :key="`${line.label}-${line.value}`"
                      class="price-line"
                    >
                      <span class="price-tier-label">{{ line.label }}</span>
                      <span>{{ line.value }}</span>
                    </div>
                  </div>
                  <template v-else>{{ priceLines(model.pricing, field.key)[0]?.value || '-' }}</template>
                </div>
                <div class="price-cell billing-cell" data-label="Billing">{{ pricingSummary(model.pricing) }}</div>
              </div>

              <div v-if="isExpanded(rowKey(group, model))" class="model-price-details">
                <div class="flex flex-wrap items-center gap-x-5 gap-y-2">
                  <span><strong>{{ t('modelPlaza.channels') }}:</strong> {{ model.channels.join(' · ') }}</span>
                  <span><strong>{{ t('modelPlaza.pricing') }}:</strong> {{ pricingSummary(model.pricing) }}</span>
                  <span v-if="model.pricing?.intervals.length"><strong>{{ t('modelPlaza.tiers') }}:</strong> {{ formatIntervals(model.pricing.intervals) }}</span>
                </div>
              </div>
            </div>
          </div>
        </article>
      </section>

      <section v-else class="catalogue-empty">
        <Icon name="inbox" size="xl" class="mb-3 h-12 w-12 text-gray-400" />
        <p class="text-sm text-gray-500 dark:text-gray-400">
          {{ query.trim() || selectedCategory !== 'all' || selectedPlatform !== 'all' || selectedGroup !== 'all' || selectedRate !== 'all' ? t('modelPlaza.noMatches') : t('modelPlaza.empty') }}
        </p>
      </section>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import ModelIcon from '@/components/common/ModelIcon.vue'
import PlatformIcon from '@/components/common/PlatformIcon.vue'
import GroupBadge from '@/components/common/GroupBadge.vue'
import userChannelsAPI from '@/api/channels'
import type { UserPricingInterval, UserSupportedModelPricing } from '@/api/channels'
import type { GroupPlatform, SubscriptionType } from '@/types'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'
import { useClipboard } from '@/composables/useClipboard'
import { formatScaled } from '@/utils/pricing'
import { platformBadgeClass, platformBorderClass, platformLabel } from '@/utils/platformColors'
import { BILLING_MODE_IMAGE, BILLING_MODE_PER_REQUEST, BILLING_MODE_TOKEN } from '@/constants/channel'
import userGroupsAPI from '@/api/groups'
import {
  buildModelPlazaGroups,
  buildModelPlazaModels,
  filterModelPlazaGroups,
  type ModelPlazaCategory,
  type ModelPlazaGroup,
  type ModelPlazaGroupModel,
  type ModelPlazaModel,
} from '@/utils/modelPlaza'

const { t } = useI18n()
const appStore = useAppStore()
const { copyToClipboard } = useClipboard()

const models = ref<ModelPlazaModel[]>([])
const groups = ref<ModelPlazaGroup[]>([])
const loading = ref(false)
const query = ref('')
const selectedPlatform = ref('all')
const selectedGroup = ref('all')
const selectedRate = ref('all')
const selectedCategory = ref<ModelPlazaCategory>('all')
const expandedKeys = ref<Set<string>>(new Set())
let abortController: AbortController | null = null

const categories: ModelPlazaCategory[] = ['all', 'claude', 'codex', 'gemini', 'grok', 'domestic', 'image', 'other']
const pricingFields = computed<Array<{ key: PricingField; label: string }>>(() => [
  { key: 'input', label: t('modelPlaza.input') },
  { key: 'output', label: t('modelPlaza.output') },
  { key: 'cacheRead', label: t('modelPlaza.cacheRead') },
  { key: 'cacheWrite', label: t('modelPlaza.cacheWrite') },
])

const filteredGroups = computed(() => filterModelPlazaGroups(groups.value, {
  query: query.value,
  platform: selectedPlatform.value,
  category: selectedCategory.value,
  groupKey: selectedGroup.value,
  rateMultiplier: selectedRate.value,
}))

const channelCount = computed(() => new Set(groups.value.flatMap((group) => group.channelNames)).size)

const platformOptions = computed(() => {
  const counts = new Map<string, number>()
  for (const group of groups.value) counts.set(group.platform, (counts.get(group.platform) || 0) + 1)
  return [
    { value: 'all', label: t('modelPlaza.all'), count: groups.value.length },
    ...Array.from(counts.entries())
      .sort(([a], [b]) => a.localeCompare(b))
      .map(([value, count]) => ({ value, label: platformLabel(value), count })),
  ]
})

const groupOptions = computed(() => [
  { value: 'all', label: t('modelPlaza.all'), rate: '' },
  ...groups.value
    .filter((group) => group.id !== null)
    .map((group) => ({
      value: group.key,
      label: group.name,
      rate: group.rateMultiplier === null ? '' : formatRate(group.rateMultiplier),
    })),
])

const rateOptions = computed(() => {
  const rates = new Set(groups.value.map((group) => group.rateMultiplier).filter((rate): rate is number => rate !== null))
  return [
    { value: 'all', label: t('modelPlaza.all') },
    ...Array.from(rates)
      .sort((a, b) => a - b)
      .map((rate) => ({ value: String(rate), label: formatRate(rate) })),
  ]
})

function categoryLabel(category: ModelPlazaCategory): string {
  return category === 'all' ? t('modelPlaza.categories.all') : platformLabel(category)
}

function formatRate(rate: number): string {
  return `${rate.toPrecision(6).replace(/\.?(0+)$/, '')}x`
}

function rowKey(group: ModelPlazaGroup, model: ModelPlazaGroupModel): string {
  return `${group.key}::${model.key}`
}

function isExpanded(key: string): boolean {
  return expandedKeys.value.has(key)
}

function toggleExpanded(key: string) {
  const next = new Set(expandedKeys.value)
  if (next.has(key)) next.delete(key)
  else next.add(key)
  expandedKeys.value = next
}

async function copyModel(name: string) {
  await copyToClipboard(name, t('modelPlaza.copied'))
}

type PricingField = 'input' | 'output' | 'cacheRead' | 'cacheWrite'

function pricingValue(pricing: UserSupportedModelPricing, field: PricingField): number | null {
  switch (field) {
    case 'input':
      return pricing.input_price
    case 'output':
      return pricing.output_price
    case 'cacheRead':
      return pricing.cache_read_price
    case 'cacheWrite':
      return pricing.cache_write_price
  }
}

function intervalLabel(interval: UserPricingInterval): string {
  if (interval.tier_label) return interval.tier_label
  const max = interval.max_tokens == null ? '∞' : interval.max_tokens
  return `${interval.min_tokens}-${max}`
}

function priceLines(pricing: UserSupportedModelPricing | null, field: PricingField): Array<{ label: string; value: string }> {
  if (!pricing || pricing.billing_mode !== BILLING_MODE_TOKEN) return []

  const intervals = pricing.intervals
    .map((interval) => ({
      label: intervalLabel(interval),
      value: formatScaled(
        field === 'input'
          ? interval.input_price
          : field === 'output'
            ? interval.output_price
            : field === 'cacheRead'
              ? interval.cache_read_price
              : interval.cache_write_price,
        1_000_000,
        0,
        pricing.currency,
      ),
    }))
    .filter((line) => line.value !== '-')

  if (intervals.length > 0) return intervals
  return [{ label: '', value: formatScaled(pricingValue(pricing, field), 1_000_000, 0, pricing.currency) }]
}

function tokenPrice(pricing: UserSupportedModelPricing | null, field: PricingField): string {
  return priceLines(pricing, field)[0]?.value || '-'
}

function pricingSummary(pricing: UserSupportedModelPricing | null): string {
  if (!pricing) return t('availableChannels.noPricing')
  switch (pricing.billing_mode) {
    case BILLING_MODE_TOKEN:
      return `${t('modelPlaza.perToken')} · ${tokenPrice(pricing, 'input')} / ${tokenPrice(pricing, 'output')}`
    case BILLING_MODE_PER_REQUEST:
      return `${t('modelPlaza.perRequest')} · ${formatScaled(pricing.per_request_price, 1, 0, pricing.currency)}`
    case BILLING_MODE_IMAGE:
      return `${t('modelPlaza.perImage')} · ${formatScaled(pricing.image_output_price, 1, 0, pricing.currency)}`
    default:
      return t('availableChannels.noPricing')
  }
}

function formatIntervals(intervals: UserPricingInterval[]): string {
  return intervals
    .map((interval) => interval.tier_label || `(${interval.min_tokens}, ${interval.max_tokens ?? '∞'}]`)
    .join(' · ')
}

async function loadModels() {
  if (abortController) abortController.abort()
  const controller = new AbortController()
  abortController = controller
  loading.value = true
  try {
    const [channels, userGroupRates] = await Promise.all([
      userChannelsAPI.getAvailable({ signal: controller.signal }),
      userGroupsAPI.getUserGroupRates().catch((err: unknown) => {
        console.error('Failed to load user group rates:', err)
        return {} as Record<number, number>
      }),
    ])
    if (controller.signal.aborted || abortController !== controller) return
    const nextModels = buildModelPlazaModels(channels)
    models.value = nextModels
    groups.value = buildModelPlazaGroups(nextModels, userGroupRates)
  } catch (err: unknown) {
    const error = err as { name?: string; code?: string }
    if (error?.name === 'AbortError' || error?.code === 'ERR_CANCELED') return
    appStore.showError(extractApiErrorMessage(err, t('modelPlaza.loadError')))
  } finally {
    if (abortController === controller) {
      loading.value = false
      abortController = null
    }
  }
}

onMounted(() => {
  void loadModels()
})

onBeforeUnmount(() => {
  abortController?.abort()
})
</script>

<style scoped>
.model-plaza-summary {
  @apply flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-gray-500 dark:text-gray-400;
}

.model-plaza-summary span + span {
  @apply border-l border-gray-200 pl-3 dark:border-dark-600;
}

.model-plaza-summary strong {
  @apply text-sm font-semibold text-gray-900 dark:text-white;
}

.catalogue-filters {
  @apply space-y-3 rounded-2xl border border-gray-200 bg-white p-4 shadow-sm dark:border-dark-700 dark:bg-dark-800;
}

.filter-row {
  @apply flex min-w-0 flex-wrap items-center gap-2;
}

.filter-label {
  @apply mr-1 min-w-[3.5rem] text-xs font-semibold text-gray-500 dark:text-gray-400;
}

.filter-pill {
  @apply inline-flex items-center gap-1.5 rounded-lg border border-gray-200 bg-white px-3 py-1.5 text-xs font-medium text-gray-600 transition-colors hover:border-primary-300 hover:text-primary-600 dark:border-dark-600 dark:bg-dark-800 dark:text-gray-300 dark:hover:border-primary-500 dark:hover:text-primary-400;
}

.filter-pill-active {
  @apply border-primary-500 bg-primary-500 text-white hover:border-primary-500 hover:text-white dark:border-primary-500 dark:bg-primary-500 dark:text-white;
}

.filter-count,
.filter-rate {
  @apply rounded bg-gray-100 px-1.5 py-0.5 text-[10px] text-gray-500 dark:bg-dark-700 dark:text-gray-400;
}

.filter-pill-active .filter-count,
.filter-pill-active .filter-rate {
  @apply bg-white/20 text-white;
}

.filter-pill-rate {
  @apply font-mono;
}

.catalogue-group {
  @apply overflow-hidden rounded-xl border border-gray-200 bg-white shadow-sm dark:border-dark-700 dark:bg-dark-800;
}

.catalogue-group-header {
  @apply flex flex-col justify-between gap-3 border-b border-gray-100 px-4 py-3 sm:flex-row sm:items-center dark:border-dark-700;
}

.platform-label,
.scope-label,
.model-category,
.tier-label,
.source-count {
  @apply inline-flex items-center gap-1 rounded-md px-1.5 py-0.5 text-[10px] font-medium;
}

.platform-label {
  @apply border;
}

.scope-label-exclusive {
  @apply bg-purple-50 text-purple-700 dark:bg-purple-900/20 dark:text-purple-300;
}

.ungrouped-label {
  @apply text-sm font-semibold text-gray-800 dark:text-gray-100;
}

.rate-emphasis {
  @apply rounded-md bg-primary-50 px-2 py-1 font-mono text-xs font-semibold text-primary-700 dark:bg-primary-900/30 dark:text-primary-300;
}

.price-matrix-wrap {
  @apply overflow-x-auto;
}

.price-grid {
  display: grid;
  grid-template-columns: minmax(250px, 1.7fr) repeat(4, minmax(92px, 0.65fr)) minmax(150px, 1fr);
  min-width: 840px;
}

.price-grid-header {
  @apply border-b border-gray-100 bg-gray-50/80 px-4 py-2.5 text-[11px] font-semibold uppercase text-gray-500 dark:border-dark-700 dark:bg-dark-900/30 dark:text-gray-400;
}

.model-price-row {
  @apply border-b border-gray-100 last:border-b-0 dark:border-dark-700;
}

.model-price-row:hover,
.model-price-row-expanded {
  @apply bg-gray-50/60 dark:bg-dark-900/20;
}

.model-price-grid {
  @apply items-center px-4 py-3;
}

.model-icon-small {
  @apply flex h-8 w-8 flex-shrink-0 items-center justify-center rounded-lg border bg-gray-50 dark:bg-dark-900/40;
}

.model-name-cell {
  @apply min-w-0;
}

.model-name {
  @apply block truncate font-mono text-sm font-semibold text-gray-800 dark:text-gray-100;
}

.model-category {
  @apply bg-gray-100 text-gray-500 dark:bg-dark-700 dark:text-gray-400;
}

.tier-label {
  @apply bg-amber-50 text-amber-700 dark:bg-amber-900/20 dark:text-amber-300;
}

.source-count {
  @apply bg-blue-50 font-mono text-blue-700 dark:bg-blue-900/20 dark:text-blue-300;
}

.price-cell {
  @apply min-w-0 truncate font-mono text-xs font-medium text-gray-800 dark:text-gray-200;
}

.price-stack {
  @apply space-y-1;
}

.price-line {
  @apply flex items-center gap-1.5 whitespace-nowrap;
}

.price-tier-label {
  @apply text-[10px] font-sans font-normal text-gray-400 dark:text-gray-500;
}

.billing-cell {
  @apply whitespace-normal font-sans text-[11px] text-gray-500 dark:text-gray-400;
}

.icon-button {
  @apply flex h-7 w-7 items-center justify-center rounded-md text-gray-400 transition-colors hover:bg-gray-100 hover:text-gray-900 focus:outline-none focus-visible:ring-2 focus-visible:ring-primary-500/40 dark:text-gray-500 dark:hover:bg-dark-700 dark:hover:text-white;
}

.model-price-details {
  @apply border-t border-gray-100 bg-gray-50/70 px-4 py-2.5 pl-14 text-[11px] text-gray-500 dark:border-dark-700 dark:bg-dark-900/30 dark:text-gray-400;
}

.model-price-details strong {
  @apply font-medium text-gray-700 dark:text-gray-300;
}

.catalogue-empty {
  @apply flex min-h-64 flex-col items-center justify-center rounded-xl border border-gray-200 bg-white p-8 text-center shadow-sm dark:border-dark-700 dark:bg-dark-800;
}

@media (max-width: 767px) {
  .catalogue-filters {
    @apply rounded-xl p-3;
  }

  .filter-label {
    @apply basis-full;
  }

  .price-matrix-wrap {
    @apply overflow-visible;
  }

  .price-grid {
    display: block;
    min-width: 0;
  }

  .price-grid-header {
    @apply hidden;
  }

  .model-price-grid {
    @apply grid grid-cols-2 gap-x-4 gap-y-3 px-3 py-3;
  }

  .model-name-cell {
    grid-column: 1 / -1;
  }

  .price-cell {
    @apply flex items-center justify-between gap-2 whitespace-normal text-right;
  }

  .price-cell::before {
    content: attr(data-label);
    @apply text-[10px] font-sans font-medium text-gray-400 dark:text-gray-500;
  }

  .billing-cell {
    @apply col-span-2;
  }

  .model-price-details {
    @apply px-3 py-2.5;
  }
}
</style>
