import type {
  UserAvailableChannel,
  UserAvailableGroup,
  UserPricingInterval,
  UserSupportedModel,
  UserSupportedModelPricing,
} from '@/api/channels'
import {
  AVAILABLE_CHANNEL_CATEGORY_ORDER,
  getAvailableChannelCategory,
  type AvailableChannelCategory,
} from '@/utils/availableChannels'

export interface ModelPlazaSource {
  channelName: string
  description: string
  platform: string
  groups: UserAvailableGroup[]
  pricing: UserSupportedModelPricing | null
}

export interface ModelPlazaModel {
  key: string
  name: string
  platform: string
  category: AvailableChannelCategory
  sources: ModelPlazaSource[]
  /** Channel names represented by this model (kept for compact card rendering). */
  channels: string[]
  /** Primary display price; source prices remain available in `sources`. */
  pricing: UserSupportedModelPricing | null
}

export type ModelPlazaCategory = AvailableChannelCategory | 'all'

export interface ModelPlazaFilters {
  query?: string
  category?: ModelPlazaCategory
}

function modelKey(model: UserSupportedModel, platformHint: string): string {
  const platform = (platformHint || model.platform).trim().toLowerCase()
  return `${platform}::${model.name.trim().toLowerCase()}`
}

function pricingKey(pricing: UserSupportedModelPricing | null): string {
  if (!pricing) return 'none'
  return JSON.stringify({
    billing_mode: pricing.billing_mode,
    input_price: pricing.input_price,
    output_price: pricing.output_price,
    cache_write_price: pricing.cache_write_price,
    cache_read_price: pricing.cache_read_price,
    image_output_price: pricing.image_output_price,
    per_request_price: pricing.per_request_price,
    intervals: pricing.intervals.map((interval: UserPricingInterval) => ({
      min_tokens: interval.min_tokens,
      max_tokens: interval.max_tokens,
      tier_label: interval.tier_label,
      input_price: interval.input_price,
      output_price: interval.output_price,
      cache_write_price: interval.cache_write_price,
      cache_read_price: interval.cache_read_price,
      per_request_price: interval.per_request_price,
    })),
  })
}

function mergeGroups(existing: UserAvailableGroup[], incoming: UserAvailableGroup[]): UserAvailableGroup[] {
  const byId = new Map(existing.map((group) => [group.id, group]))
  for (const group of incoming) byId.set(group.id, group)
  return Array.from(byId.values()).sort((a, b) => a.name.localeCompare(b.name))
}

/**
 * Aggregates the permission-filtered available-channel response into model cards.
 * The platform remains part of the identity because equal model names can use
 * different protocol adapters and pricing contracts.
 */
export function buildModelPlazaModels(channels: UserAvailableChannel[]): ModelPlazaModel[] {
  const models = new Map<string, ModelPlazaModel>()

  for (const channel of channels) {
    for (const section of channel.platforms) {
      for (const model of section.supported_models) {
        const key = modelKey(model, section.platform)
        const platform = (section.platform || model.platform).trim()
        const category = getAvailableChannelCategory(model, section.platform)
        const plazaModel = models.get(key) || {
          key,
          name: model.name.trim(),
          platform,
          category,
          sources: [],
          channels: [],
          pricing: null,
        }
        models.set(key, plazaModel)

        let source = plazaModel.sources.find(
          (candidate) => candidate.channelName === channel.name && candidate.platform === section.platform,
        )
        if (!source) {
          source = {
            channelName: channel.name,
            description: channel.description,
            platform: section.platform,
            groups: [],
            pricing: model.pricing,
          }
          plazaModel.sources.push(source)
        }

        source.groups = mergeGroups(source.groups, section.groups)
        if (!source.pricing && model.pricing) source.pricing = model.pricing
        if (!plazaModel.channels.includes(channel.name)) plazaModel.channels.push(channel.name)
        if (!plazaModel.pricing && model.pricing) plazaModel.pricing = model.pricing
      }
    }
  }

  return Array.from(models.values()).sort((a, b) => {
    const categoryDiff = AVAILABLE_CHANNEL_CATEGORY_ORDER.indexOf(a.category) - AVAILABLE_CHANNEL_CATEGORY_ORDER.indexOf(b.category)
    if (categoryDiff !== 0) return categoryDiff
    return a.name.localeCompare(b.name)
  })
}

export function filterModelPlazaModels(
  models: ModelPlazaModel[],
  filters: ModelPlazaFilters,
): ModelPlazaModel[] {
  const query = filters.query?.trim().toLowerCase() || ''
  const category = filters.category || 'all'

  return models.filter((model) => {
    if (category !== 'all' && model.category !== category) return false
    if (!query) return true

    const sourceText = model.sources
      .flatMap((source) => [
        source.channelName,
        ...source.groups.map((group) => group.name),
      ])
      .join(' ')
      .toLowerCase()

    return `${model.name} ${model.platform} ${sourceText}`.toLowerCase().includes(query)
  })
}

export function countModelPlazaChannels(model: ModelPlazaModel): number {
  return new Set(model.sources.map((source) => source.channelName)).size
}

export function countModelPlazaGroups(model: ModelPlazaModel): number {
  return new Set(model.sources.flatMap((source) => source.groups.map((group) => group.id))).size
}

export function hasMultipleModelPlazaPrices(model: ModelPlazaModel): boolean {
  return new Set(model.sources.map((source) => pricingKey(source.pricing))).size > 1
}

export interface ModelPlazaGroupModel extends ModelPlazaModel {}
export interface ModelPlazaGroup {
  key: string; id: number | null; name: string; platform: string; models: ModelPlazaGroupModel[]; channelNames: string[]; rateMultiplier: number | null; subscriptionType: string; peakRateEnabled: boolean; peakStart?: string; peakEnd?: string; peakRateMultiplier?: number
  isExclusive: boolean
}
export function buildModelPlazaGroups(models: ModelPlazaModel[], rates: Record<number, number> = {}): ModelPlazaGroup[] {
  const map = new Map<string, ModelPlazaGroup>()
  const ungrouped = new Map<string, ModelPlazaModel[]>()
  for (const model of models) {
    for (const source of model.sources) for (const group of source.groups) {
      const groupPlatform = (group.platform || model.platform).trim()
      const key = `${groupPlatform}::${group.id}`
      const existing = map.get(key) || {
        key, id: group.id, name: group.name, platform: groupPlatform, models: [], channelNames: [],
        rateMultiplier: rates[group.id] ?? group.rate_multiplier ?? null,
        subscriptionType: group.subscription_type || '', peakRateEnabled: Boolean(group.peak_rate_enabled),
        peakStart: group.peak_start, peakEnd: group.peak_end, peakRateMultiplier: group.peak_rate_multiplier,
        isExclusive: Boolean(group.is_exclusive),
      }
      const relevantSources = model.sources.filter((candidate) => candidate.groups.some((candidateGroup) => candidateGroup.id === group.id && (candidateGroup.platform || model.platform).trim() === groupPlatform))
      const groupModel: ModelPlazaGroupModel = {
        ...model,
        sources: relevantSources,
        channels: Array.from(new Set(relevantSources.map((candidate) => candidate.channelName))),
        pricing: relevantSources.find((candidate) => candidate.pricing)?.pricing ?? null,
      }
      const existingModel = existing.models.findIndex((candidate) => candidate.key === model.key)
      if (existingModel >= 0) existing.models[existingModel] = groupModel
      else existing.models.push(groupModel)
      if (!existing.channelNames.includes(source.channelName)) existing.channelNames.push(source.channelName)
      map.set(key, existing)
    }
    const ungroupedSources = model.sources.filter((source) => source.groups.length === 0)
    if (ungroupedSources.length) {
      const list = ungrouped.get(model.platform) || []
      list.push({ ...model, sources: ungroupedSources, channels: ungroupedSources.map((source) => source.channelName), pricing: ungroupedSources.find((source) => source.pricing)?.pricing ?? null })
      ungrouped.set(model.platform, list)
    }
  }
  for (const [platform, platformModels] of ungrouped) {
    const key = `${platform}::__ungrouped`
    map.set(key, { key, id: null, name: 'Ungrouped', platform, models: platformModels, channelNames: Array.from(new Set(platformModels.flatMap((model) => model.channels))), rateMultiplier: null, subscriptionType: '', peakRateEnabled: false, isExclusive: false })
  }
  return Array.from(map.values()).sort((a, b) => a.platform.localeCompare(b.platform) || a.name.localeCompare(b.name))
}
export function filterModelPlazaGroups(groups: ModelPlazaGroup[], filters: { query?: string; platform?: string; category?: string; groupKey?: string; rateMultiplier?: string }): ModelPlazaGroup[] {
  const q = filters.query?.trim().toLowerCase() || ''
  const requestedRate = filters.rateMultiplier && filters.rateMultiplier !== 'all' ? Number(filters.rateMultiplier) : null
  return groups.filter((g) => {
    if (filters.platform && filters.platform !== 'all' && g.platform !== filters.platform) return false
    if (filters.groupKey && filters.groupKey !== 'all' && g.key !== filters.groupKey) return false
    if (requestedRate !== null && (g.rateMultiplier === null || g.rateMultiplier !== requestedRate)) return false
    if (filters.category && filters.category !== 'all' && !g.models.some((m) => m.category === filters.category)) return false
    if (!q) return true
    return `${g.name} ${g.platform} ${g.channelNames.join(' ')} ${g.models.map((m) => `${m.name} ${m.category} ${m.channels.join(' ')}`).join(' ')}`.toLowerCase().includes(q)
  })
}
