<template>
  <AppLayout>
    <div class="w-full min-w-0 space-y-6 pb-8">
      <header class="rounded-2xl bg-white p-5 shadow-sm ring-1 ring-gray-900/5 dark:bg-dark-800 dark:ring-dark-700 sm:p-6">
        <div class="flex flex-wrap items-start justify-between gap-4">
          <div class="min-w-0">
            <h1 class="flex items-center gap-2 text-xl font-black text-gray-900 dark:text-white">
              <span class="inline-flex h-8 w-8 flex-shrink-0 items-center justify-center rounded-xl bg-primary-50 text-primary-600 dark:bg-primary-900/30 dark:text-primary-400">
                <Icon name="grid" size="sm" />
              </span>
              {{ t('admin.publicCatalog.title') }}
            </h1>
            <p class="mt-2 max-w-3xl text-sm text-gray-500 dark:text-gray-400">
              {{ t('admin.publicCatalog.presentationOnlyNotice') }}
            </p>
          </div>

          <div class="flex flex-wrap items-center gap-2">
            <button
              type="button"
              class="btn btn-secondary btn-sm"
              :disabled="loading || saving"
              :title="t('common.refresh')"
              @click="load"
            >
              <Icon name="refresh" size="sm" :class="loading ? 'animate-spin' : ''" />
              <span class="ml-1.5">{{ t('common.refresh') }}</span>
            </button>
            <button type="button" class="btn btn-secondary btn-sm" @click="openCatalog('/available-channels')">
              <Icon name="externalLink" size="sm" class="mr-1.5" />
              {{ t('admin.publicCatalog.openAvailableChannels') }}
            </button>
            <button type="button" class="btn btn-secondary btn-sm" @click="openCatalog('/model-plaza')">
              <Icon name="externalLink" size="sm" class="mr-1.5" />
              {{ t('admin.publicCatalog.openModelPlaza') }}
            </button>
            <button
              type="button"
              class="btn btn-secondary btn-sm"
              data-testid="bulk-show-catalog"
              :disabled="loading || saving || filteredCandidates.length === 0"
              @click="setFilteredVisibility(true)"
            >
              <Icon name="eye" size="sm" class="mr-1.5" />
              {{ t('admin.publicCatalog.bulkShow') }}
            </button>
            <button
              type="button"
              class="btn btn-secondary btn-sm"
              data-testid="bulk-hide-catalog"
              :disabled="loading || saving || filteredCandidates.length === 0"
              @click="setFilteredVisibility(false)"
            >
              <Icon name="eyeOff" size="sm" class="mr-1.5" />
              {{ t('admin.publicCatalog.bulkHide') }}
            </button>
            <button
              type="button"
              class="btn btn-primary btn-sm"
              data-testid="save-public-catalog"
              :disabled="loading || saving"
              @click="save"
            >
              <Icon name="check" size="sm" class="mr-1.5" />
              {{ saving ? t('admin.publicCatalog.saving') : t('common.save') }}
            </button>
          </div>
        </div>

        <div class="mt-5 border-t border-gray-100 pt-5 dark:border-dark-700">
          <div class="flex flex-wrap items-end gap-4">
            <div class="min-w-[220px] flex-1">
              <label class="input-label">{{ t('admin.publicCatalog.searchLabel') }}</label>
              <div class="relative">
                <Icon name="search" size="sm" class="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-gray-400" />
                <input
                  v-model.trim="searchQuery"
                  type="search"
                  class="input pl-9"
                  data-testid="catalog-search"
                  :placeholder="t('admin.publicCatalog.searchPlaceholder')"
                />
              </div>
            </div>

            <div class="w-full sm:w-44">
              <label class="input-label">{{ t('admin.publicCatalog.platformLabel') }}</label>
              <select v-model="platformFilter" class="input">
                <option value="">{{ t('admin.publicCatalog.allPlatforms') }}</option>
                <option v-for="platform in platforms" :key="platform" :value="platform">{{ platform }}</option>
              </select>
            </div>

            <div class="w-full sm:w-36">
              <label class="input-label">{{ t('admin.publicCatalog.typeLabel') }}</label>
              <select v-model="typeFilter" class="input">
                <option value="all">{{ t('admin.publicCatalog.allTypes') }}</option>
                <option value="text">{{ t('admin.publicCatalog.textType') }}</option>
                <option value="media">{{ t('admin.publicCatalog.mediaType') }}</option>
              </select>
            </div>

            <div class="w-full sm:w-36">
              <label class="input-label">{{ t('admin.publicCatalog.visibilityLabel') }}</label>
              <select v-model="visibilityFilter" class="input">
                <option value="all">{{ t('admin.publicCatalog.allVisibility') }}</option>
                <option value="visible">{{ t('admin.publicCatalog.visible') }}</option>
                <option value="hidden">{{ t('admin.publicCatalog.hidden') }}</option>
              </select>
            </div>

            <div class="w-full sm:w-52">
              <label class="input-label">{{ t('admin.publicCatalog.defaultMediaLabel') }}</label>
              <select v-model="defaultMediaVisibility" class="input">
                <option value="hidden">{{ t('admin.publicCatalog.defaultMediaHidden') }}</option>
                <option value="visible">{{ t('admin.publicCatalog.defaultMediaVisible') }}</option>
              </select>
            </div>
          </div>
        </div>
      </header>

      <section class="overflow-hidden rounded-2xl bg-white shadow-sm ring-1 ring-gray-900/5 dark:bg-dark-800 dark:ring-dark-700">
        <div class="flex flex-wrap items-center justify-between gap-2 border-b border-gray-100 px-5 py-4 dark:border-dark-700 sm:px-6">
          <div>
            <h2 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('admin.publicCatalog.modelListTitle') }}</h2>
            <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
              {{ t('admin.publicCatalog.resultCount', { count: filteredCandidates.length, total: candidates.length }) }}
            </p>
          </div>
          <span class="text-xs text-gray-400 dark:text-gray-500">{{ t('admin.publicCatalog.defaultMediaHint') }}</span>
        </div>

        <div v-if="loading" class="flex items-center justify-center px-6 py-16">
          <div class="h-8 w-8 animate-spin rounded-full border-b-2 border-primary-600"></div>
        </div>

        <div v-else-if="filteredCandidates.length === 0" class="px-6 py-16 text-center text-sm text-gray-500 dark:text-gray-400">
          {{ candidates.length === 0 ? t('admin.publicCatalog.noCandidates') : t('admin.publicCatalog.noFilteredCandidates') }}
        </div>

        <div v-else class="overflow-x-auto">
          <table class="min-w-full divide-y divide-gray-100 dark:divide-dark-700">
            <thead class="bg-gray-50/80 dark:bg-dark-900/40">
              <tr>
                <th class="px-5 py-3 text-left text-xs font-semibold uppercase tracking-wide text-gray-500 dark:text-gray-400">{{ t('admin.publicCatalog.columns.model') }}</th>
                <th class="px-5 py-3 text-left text-xs font-semibold uppercase tracking-wide text-gray-500 dark:text-gray-400">{{ t('admin.publicCatalog.columns.platform') }}</th>
                <th class="px-5 py-3 text-left text-xs font-semibold uppercase tracking-wide text-gray-500 dark:text-gray-400">{{ t('admin.publicCatalog.columns.type') }}</th>
                <th class="px-5 py-3 text-left text-xs font-semibold uppercase tracking-wide text-gray-500 dark:text-gray-400">{{ t('admin.publicCatalog.columns.defaultStatus') }}</th>
                <th class="px-5 py-3 text-right text-xs font-semibold uppercase tracking-wide text-gray-500 dark:text-gray-400">{{ t('admin.publicCatalog.columns.userStatus') }}</th>
              </tr>
            </thead>
            <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
              <tr v-for="candidate in filteredCandidates" :key="candidate.key" class="hover:bg-gray-50/70 dark:hover:bg-dark-700/30">
                <td class="max-w-[32rem] px-5 py-3.5">
                  <div class="truncate font-medium text-gray-900 dark:text-white" :title="candidate.model">{{ candidate.model }}</div>
                  <div class="mt-0.5 truncate font-mono text-xs text-gray-400 dark:text-gray-500" :title="candidate.key">{{ candidate.key }}</div>
                </td>
                <td class="px-5 py-3.5 text-sm text-gray-600 dark:text-gray-300">{{ candidate.platform }}</td>
                <td class="px-5 py-3.5 text-sm text-gray-600 dark:text-gray-300">{{ candidate.is_media ? t('admin.publicCatalog.mediaType') : t('admin.publicCatalog.textType') }}</td>
                <td class="px-5 py-3.5 text-sm text-gray-500 dark:text-gray-400">{{ candidate.default_visible ? t('admin.publicCatalog.visible') : t('admin.publicCatalog.hidden') }}</td>
                <td class="px-5 py-3.5 text-right">
                  <button
                    type="button"
                    class="inline-flex items-center gap-2 rounded-lg px-2 py-1 text-sm font-medium transition-colors hover:bg-gray-100 dark:hover:bg-dark-700"
                    :class="effectiveVisibility(candidate) ? 'text-emerald-600 dark:text-emerald-400' : 'text-gray-400 dark:text-gray-500'"
                    :data-testid="'visibility-' + candidate.key"
                    :aria-label="candidate.model"
                    @click="toggleVisibility(candidate)"
                  >
                    <Icon :name="effectiveVisibility(candidate) ? 'eye' : 'eyeOff'" size="sm" />
                    <span>{{ effectiveVisibility(candidate) ? t('admin.publicCatalog.visible') : t('admin.publicCatalog.hidden') }}</span>
                  </button>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { getVisibility, updateVisibility } from '@/api/admin/publicCatalog'
import type {
  PublicCatalogMediaVisibility,
  PublicCatalogModelCandidate,
  PublicCatalogVisibilityView,
} from '@/api/admin/publicCatalog'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'

type CatalogTypeFilter = 'all' | 'text' | 'media'
type CatalogVisibilityFilter = 'all' | 'visible' | 'hidden'

const { t } = useI18n()
const appStore = useAppStore()
const loading = ref(true)
const saving = ref(false)
const searchQuery = ref('')
const platformFilter = ref('')
const typeFilter = ref<CatalogTypeFilter>('all')
const visibilityFilter = ref<CatalogVisibilityFilter>('all')
const defaultMediaVisibility = ref<PublicCatalogMediaVisibility>('hidden')
const candidates = ref<PublicCatalogModelCandidate[]>([])
const models = reactive<Record<string, boolean>>({})

const platforms = computed(() => [...new Set(candidates.value.map((candidate) => candidate.platform))].sort())

function effectiveVisibility(candidate: PublicCatalogModelCandidate): boolean {
  if (Object.prototype.hasOwnProperty.call(models, candidate.key)) {
    return models[candidate.key]
  }
  if (!candidate.is_media || candidate.default_visible) {
    return true
  }
  return defaultMediaVisibility.value === 'visible'
}

const filteredCandidates = computed(() => {
  const query = searchQuery.value.toLowerCase()
  return candidates.value.filter((candidate) => {
    const matchesSearch = !query || [candidate.key, candidate.platform, candidate.model].some((value) => value.toLowerCase().includes(query))
    const matchesPlatform = !platformFilter.value || candidate.platform === platformFilter.value
    const matchesType = typeFilter.value === 'all' || (typeFilter.value === 'media' ? candidate.is_media : !candidate.is_media)
    const visible = effectiveVisibility(candidate)
    const matchesVisibility = visibilityFilter.value === 'all' || (visibilityFilter.value === 'visible' ? visible : !visible)
    return matchesSearch && matchesPlatform && matchesType && matchesVisibility
  })
})

function applyView(view: PublicCatalogVisibilityView): void {
  defaultMediaVisibility.value = view.default_media_visibility
  candidates.value = view.candidates || []
  Object.keys(models).forEach((key) => delete models[key])
  Object.assign(models, view.models || {})
}

async function load(): Promise<void> {
  loading.value = true
  try {
    applyView(await getVisibility())
  } catch (error: unknown) {
    appStore.showError(extractApiErrorMessage(error, t('admin.publicCatalog.loadError')))
  } finally {
    loading.value = false
  }
}

function toggleVisibility(candidate: PublicCatalogModelCandidate): void {
  models[candidate.key] = !effectiveVisibility(candidate)
}

function setFilteredVisibility(visible: boolean): void {
  filteredCandidates.value.forEach((candidate) => {
    models[candidate.key] = visible
  })
}

function openCatalog(path: string): void {
  window.open(path, '_blank', 'noopener,noreferrer')
}

async function save(): Promise<void> {
  saving.value = true
  try {
    const view = await updateVisibility({
      default_media_visibility: defaultMediaVisibility.value,
      models: { ...models },
    })
    applyView(view)
    appStore.showSuccess(t('admin.publicCatalog.saveSuccess'))
  } catch (error: unknown) {
    appStore.showError(extractApiErrorMessage(error, t('admin.publicCatalog.saveError')))
  } finally {
    saving.value = false
  }
}

onMounted(() => {
  void load()
})
</script>
