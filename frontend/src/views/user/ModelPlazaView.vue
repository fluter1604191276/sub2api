<template>
  <AppLayout>
    <div class="model-plaza space-y-5">
      <header class="flex flex-col justify-between gap-4 md:flex-row md:items-start">
        <div class="flex min-w-0 items-start gap-3">
          <div class="flex h-11 w-11 flex-shrink-0 items-center justify-center rounded-xl bg-primary-50 text-primary-600 dark:bg-primary-900/30 dark:text-primary-400">
            <Icon name="grid" size="lg" />
          </div>
          <div class="min-w-0">
            <h1 class="truncate text-xl font-semibold text-gray-900 dark:text-white">{{ t('modelPlaza.title') }}</h1>
            <p class="mt-1 max-w-3xl text-sm text-gray-500 dark:text-gray-400">{{ t('modelPlaza.description') }}</p>
          </div>
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
      </header>

      <section class="grid grid-cols-1 gap-3 sm:grid-cols-3" aria-label="model-plaza-stats">
        <div class="card flex items-center gap-3 p-4">
          <div class="flex h-9 w-9 items-center justify-center rounded-lg bg-primary-50 text-primary-600 dark:bg-primary-900/30 dark:text-primary-400">
            <Icon name="grid" size="md" />
          </div>
          <div class="min-w-0">
            <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('modelPlaza.stats.models') }}</p>
            <p class="mt-0.5 text-lg font-semibold text-gray-900 dark:text-white">{{ models.length }}</p>
          </div>
        </div>
        <div class="card flex items-center gap-3 p-4">
          <div class="flex h-9 w-9 items-center justify-center rounded-lg bg-emerald-50 text-emerald-600 dark:bg-emerald-900/30 dark:text-emerald-400">
            <Icon name="globe" size="md" />
          </div>
          <div class="min-w-0">
            <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('modelPlaza.stats.channels') }}</p>
            <p class="mt-0.5 text-lg font-semibold text-gray-900 dark:text-white">{{ channelCount }}</p>
          </div>
        </div>
        <div class="card flex items-center gap-3 p-4">
          <div class="flex h-9 w-9 items-center justify-center rounded-lg bg-amber-50 text-amber-600 dark:bg-amber-900/30 dark:text-amber-400">
            <Icon name="filter" size="md" />
          </div>
          <div class="min-w-0">
            <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('modelPlaza.stats.visible') }}</p>
            <p class="mt-0.5 text-lg font-semibold text-gray-900 dark:text-white">{{ filteredModels.length }}</p>
          </div>
        </div>
      </section>

      <section class="card p-4">
        <div class="flex flex-col gap-4 lg:flex-row lg:items-center lg:justify-between">
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

          <div class="flex min-w-0 flex-wrap gap-1.5" role="tablist" :aria-label="t('modelPlaza.categoryFilter')">
            <button
              v-for="category in categories"
              :key="category"
              type="button"
              role="tab"
              class="rounded-lg border px-3 py-1.5 text-xs font-medium transition-colors"
              :class="selectedCategory === category
                ? 'border-primary-500 bg-primary-500 text-white'
                : 'border-gray-200 bg-white text-gray-600 hover:border-primary-300 hover:text-primary-600 dark:border-dark-600 dark:bg-dark-800 dark:text-gray-300 dark:hover:border-primary-500 dark:hover:text-primary-400'"
              :aria-selected="selectedCategory === category"
              @click="selectedCategory = category"
            >
              {{ categoryLabel(category) }}
            </button>
          </div>
        </div>
      </section>

      <div v-if="loading" class="card flex min-h-64 items-center justify-center p-8">
        <Icon name="refresh" size="xl" class="animate-spin text-gray-400" />
      </div>

      <section v-else-if="filteredModels.length > 0" class="grid grid-cols-1 gap-4 xl:grid-cols-2">
        <article
          v-for="model in filteredModels"
          :key="model.key"
          class="card overflow-hidden transition-shadow hover:shadow-card-hover"
        >
          <div class="p-4">
            <div class="flex items-start gap-3">
              <div class="flex h-11 w-11 flex-shrink-0 items-center justify-center rounded-xl border bg-gray-50 dark:bg-dark-900/40" :class="platformBorderClass(model.platform)">
                <ModelIcon :model="model.name" size="26px" />
              </div>

              <div class="min-w-0 flex-1">
                <div class="flex min-w-0 items-start justify-between gap-2">
                  <div class="min-w-0">
                    <h2 class="break-all text-base font-semibold text-gray-900 dark:text-white">{{ model.name }}</h2>
                    <div class="mt-1 flex flex-wrap items-center gap-1.5">
                      <span :class="['inline-flex items-center rounded-md border px-2 py-0.5 text-[11px] font-medium', platformBadgeClass(model.platform)]">
                        <PlatformIcon :platform="model.platform as GroupPlatform" size="xs" />
                        {{ platformLabel(model.platform) }}
                      </span>
                      <span class="inline-flex items-center rounded-md bg-gray-100 px-2 py-0.5 text-[11px] font-medium text-gray-600 dark:bg-dark-700 dark:text-gray-300">
                        {{ categoryLabel(model.category) }}
                      </span>
                    </div>
                  </div>

                  <div class="flex flex-shrink-0 items-center gap-1">
                    <button
                      type="button"
                      class="flex h-8 w-8 items-center justify-center rounded-lg text-gray-500 transition-colors hover:bg-gray-100 hover:text-gray-900 focus:outline-none focus-visible:ring-2 focus-visible:ring-primary-500/40 dark:text-gray-400 dark:hover:bg-dark-700 dark:hover:text-white"
                      :title="t('modelPlaza.copyModel')"
                      :aria-label="t('modelPlaza.copyModel')"
                      @click="copyModel(model.name)"
                    >
                      <Icon name="copy" size="sm" />
                    </button>
                    <button
                      type="button"
                      class="flex h-8 w-8 items-center justify-center rounded-lg text-gray-500 transition-colors hover:bg-gray-100 hover:text-gray-900 focus:outline-none focus-visible:ring-2 focus-visible:ring-primary-500/40 dark:text-gray-400 dark:hover:bg-dark-700 dark:hover:text-white"
                      :title="isExpanded(model.key) ? t('modelPlaza.collapse') : t('modelPlaza.expand')"
                      :aria-label="isExpanded(model.key) ? t('modelPlaza.collapse') : t('modelPlaza.expand')"
                      :aria-expanded="isExpanded(model.key)"
                      @click="toggleExpanded(model.key)"
                    >
                      <Icon :name="isExpanded(model.key) ? 'chevronUp' : 'chevronDown'" size="sm" />
                    </button>
                  </div>
                </div>

                <div class="mt-4 grid grid-cols-2 gap-3 border-t border-gray-100 pt-3 dark:border-dark-700">
                  <div class="min-w-0">
                    <p class="text-[11px] text-gray-500 dark:text-gray-400">{{ t('modelPlaza.channels') }}</p>
                    <p class="mt-1 truncate text-sm font-medium text-gray-800 dark:text-gray-200">
                      {{ countModelPlazaChannels(model) }}
                    </p>
                  </div>
                  <div class="min-w-0">
                    <p class="text-[11px] text-gray-500 dark:text-gray-400">{{ t('modelPlaza.groups') }}</p>
                    <p class="mt-1 truncate text-sm font-medium text-gray-800 dark:text-gray-200">
                      {{ countModelPlazaGroups(model) }}
                    </p>
                  </div>
                </div>

                <div class="mt-3 rounded-lg bg-gray-50 px-3 py-2.5 dark:bg-dark-900/40">
                  <div class="flex items-center justify-between gap-3">
                    <span class="text-[11px] text-gray-500 dark:text-gray-400">{{ t('modelPlaza.pricing') }}</span>
                    <span v-if="hasMultipleModelPlazaPrices(model)" class="text-[10px] font-medium text-amber-700 dark:text-amber-300">
                      {{ t('modelPlaza.multiplePrices') }}
                    </span>
                  </div>
                  <p class="mt-1 break-words text-xs font-medium text-gray-800 dark:text-gray-200">
                    {{ modelPricingSummary(model) }}
                  </p>
                </div>
              </div>
            </div>
          </div>

          <div v-if="isExpanded(model.key)" class="border-t border-gray-100 bg-gray-50/70 px-4 py-3 dark:border-dark-700 dark:bg-dark-900/20">
            <div class="mb-2 flex items-center justify-between gap-3">
              <h3 class="text-xs font-semibold uppercase tracking-wide text-gray-600 dark:text-gray-300">{{ t('modelPlaza.details') }}</h3>
              <span class="text-[11px] text-gray-400 dark:text-gray-500">{{ model.sources.length }} {{ t('modelPlaza.sources') }}</span>
            </div>

            <div class="space-y-3">
              <div v-for="source in model.sources" :key="`${source.channelName}-${source.platform}`" class="rounded-lg border border-gray-200 bg-white p-3 dark:border-dark-700 dark:bg-dark-800/70">
                <div class="flex flex-wrap items-start justify-between gap-2">
                  <div class="min-w-0">
                    <p class="truncate text-sm font-medium text-gray-800 dark:text-gray-200" :title="source.channelName">{{ source.channelName }}</p>
                    <p class="mt-0.5 text-[11px] text-gray-500 dark:text-gray-400">{{ platformLabel(source.platform) }}</p>
                  </div>
                  <span class="max-w-full break-words text-right text-[11px] font-medium text-gray-600 dark:text-gray-300">
                    {{ pricingSummary(source.pricing) }}
                  </span>
                </div>

                <div v-if="source.groups.length > 0" class="mt-2 flex flex-wrap gap-1.5">
                  <GroupBadge
                    v-for="group in source.groups"
                    :key="group.id"
                    :name="group.name"
                    :platform="group.platform as GroupPlatform"
                    :subscription-type="group.subscription_type as SubscriptionType"
                    :rate-multiplier="group.rate_multiplier"
                    :peak-rate-enabled="group.peak_rate_enabled"
                    :peak-start="group.peak_start"
                    :peak-end="group.peak_end"
                    :peak-rate-multiplier="group.peak_rate_multiplier"
                    always-show-rate
                  />
                </div>
                <p v-else class="mt-2 text-xs text-gray-400 dark:text-gray-500">{{ t('modelPlaza.noGroups') }}</p>
              </div>
            </div>
          </div>
        </article>
      </section>

      <section v-else class="card flex min-h-64 flex-col items-center justify-center p-8 text-center">
        <Icon name="inbox" size="xl" class="mb-3 h-12 w-12 text-gray-400" />
        <p class="text-sm text-gray-500 dark:text-gray-400">{{ query.trim() || selectedCategory !== 'all' ? t('modelPlaza.noMatches') : t('modelPlaza.empty') }}</p>
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
import {
  buildModelPlazaModels,
  countModelPlazaChannels,
  countModelPlazaGroups,
  filterModelPlazaModels,
  hasMultipleModelPlazaPrices,
  type ModelPlazaCategory,
  type ModelPlazaModel,
} from '@/utils/modelPlaza'

const { t } = useI18n()
const appStore = useAppStore()
const { copyToClipboard } = useClipboard()

const models = ref<ModelPlazaModel[]>([])
const loading = ref(false)
const query = ref('')
const selectedCategory = ref<ModelPlazaCategory>('all')
const expandedKeys = ref<Set<string>>(new Set())
let abortController: AbortController | null = null

const categories: ModelPlazaCategory[] = ['all', 'claude', 'codex', 'gemini', 'grok', 'domestic', 'image', 'other']

const filteredModels = computed(() => filterModelPlazaModels(models.value, {
  query: query.value,
  category: selectedCategory.value,
}))

const channelCount = computed(() => {
  return new Set(models.value.flatMap((model) => model.sources.map((source) => source.channelName))).size
})

function categoryLabel(category: ModelPlazaCategory): string {
  return category === 'all' ? t('modelPlaza.categories.all') : t(`availableChannels.categories.${category}`)
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

function formatPrice(value: number | null, scale: number): string {
  return formatScaled(value, scale)
}

function formatTokenPrice(pricing: UserSupportedModelPricing): string {
  const input = formatPrice(pricing.input_price, 1_000_000)
  const output = formatPrice(pricing.output_price, 1_000_000)
  return `${t('modelPlaza.input')}: ${input} · ${t('modelPlaza.output')}: ${output}`
}

function formatIntervals(intervals: UserPricingInterval[]): string {
  return intervals.length > 0 ? ` · ${t('modelPlaza.tiered')}` : ''
}

function pricingSummary(pricing: UserSupportedModelPricing | null): string {
  if (!pricing) return t('availableChannels.noPricing')
  switch (pricing.billing_mode) {
    case 'token':
      return `${t('modelPlaza.perToken')} · ${formatTokenPrice(pricing)}${formatIntervals(pricing.intervals)}`
    case 'per_request':
      return `${t('modelPlaza.perRequest')} · ${formatPrice(pricing.per_request_price, 1)}`
    case 'image':
      return `${t('modelPlaza.perImage')} · ${formatPrice(pricing.image_output_price, 1)}`
    default:
      return t('availableChannels.noPricing')
  }
}

function modelPricingSummary(model: ModelPlazaModel): string {
  const pricing = model.sources.find((source) => source.pricing)?.pricing || null
  if (hasMultipleModelPlazaPrices(model)) {
    return `${t('modelPlaza.multiplePrices')} · ${pricingSummary(pricing)}`
  }
  return pricingSummary(pricing)
}

async function loadModels() {
  if (abortController) abortController.abort()
  const controller = new AbortController()
  abortController = controller
  loading.value = true
  try {
    const channels = await userChannelsAPI.getAvailable({ signal: controller.signal })
    if (controller.signal.aborted || abortController !== controller) return
    models.value = buildModelPlazaModels(channels)
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
