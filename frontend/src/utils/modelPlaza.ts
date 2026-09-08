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
}

export type ModelPlazaCategory = AvailableChannelCategory | 'all'

export interface ModelPlazaFilters {
  query?: string
  category?: ModelPlazaCategory
}

function modelKey(model: UserSupportedModel, platformHint: string): string {
  const platform = (model.platform || platformHint).trim().toLowerCase()
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
        const platform = (model.platform || section.platform).trim()
        const category = getAvailableChannelCategory(model, section.platform)
        const plazaModel = models.get(key) || {
          key,
          name: model.name.trim(),
          platform,
          category,
          sources: [],
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
