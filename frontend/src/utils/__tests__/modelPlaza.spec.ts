import { describe, expect, it } from 'vitest'
import type { UserAvailableChannel, UserAvailableGroup, UserSupportedModel } from '@/api/channels'
import {
  buildModelPlazaModels,
  countModelPlazaChannels,
  countModelPlazaGroups,
  filterModelPlazaModels,
  hasMultipleModelPlazaPrices,
} from '@/utils/modelPlaza'

function group(id: number, name = `group-${id}`): UserAvailableGroup {
  return {
    id,
    name,
    platform: 'openai',
    subscription_type: 'standard',
    rate_multiplier: 1,
    peak_rate_enabled: false,
    peak_start: '',
    peak_end: '',
    peak_rate_multiplier: 1,
    is_exclusive: false,
  }
}

function model(name: string, platform = 'openai', inputPrice: number | null = null): UserSupportedModel {
  return {
    name,
    platform,
    pricing: inputPrice === null
      ? null
      : {
          billing_mode: 'token',
          input_price: inputPrice,
          output_price: inputPrice * 2,
          cache_write_price: null,
          cache_read_price: null,
          image_output_price: null,
          per_request_price: null,
          intervals: [],
        },
  }
}

function channel(name: string, platforms: UserAvailableChannel['platforms']): UserAvailableChannel {
  return { name, description: `${name} description`, platforms }
}

describe('model plaza aggregation', () => {
  it('merges the same model across channels and preserves visible sources', () => {
    const models = buildModelPlazaModels([
      channel('Alpha', [{ platform: 'openai', groups: [group(1)], supported_models: [model('gpt-5.6-sol', 'openai', 5)] }]),
      channel('Beta', [{ platform: 'openai', groups: [group(2)], supported_models: [model('gpt-5.6-sol', 'openai', 6)] }]),
    ])

    expect(models).toHaveLength(1)
    expect(countModelPlazaChannels(models[0])).toBe(2)
    expect(countModelPlazaGroups(models[0])).toBe(2)
    expect(models[0].sources.map((source) => source.channelName)).toEqual(['Alpha', 'Beta'])
    expect(hasMultipleModelPlazaPrices(models[0])).toBe(true)
  })

  it('does not merge equal names from different platforms', () => {
    const models = buildModelPlazaModels([
      channel('Mixed', [{
        platform: 'openai',
        groups: [],
        supported_models: [model('shared-model', 'openai'), model('shared-model', 'anthropic')],
      }]),
    ])

    expect(models).toHaveLength(2)
    expect(new Set(models.map((item) => item.platform))).toEqual(new Set(['openai', 'anthropic']))
  })

  it('deduplicates repeated group references', () => {
    const models = buildModelPlazaModels([
      channel('Alpha', [
        { platform: 'openai', groups: [group(1)], supported_models: [model('gpt-a')] },
        { platform: 'openai', groups: [group(1), group(2)], supported_models: [model('gpt-a')] },
      ]),
    ])

    expect(countModelPlazaGroups(models[0])).toBe(2)
    expect(models[0].sources).toHaveLength(1)
  })

  it('filters by model, channel, group and category', () => {
    const models = buildModelPlazaModels([
      channel('Claude Hub', [{ platform: 'anthropic', groups: [group(1, 'premium')], supported_models: [model('claude-sonnet-4-6', 'anthropic')] }]),
      channel('Open Hub', [{ platform: 'openai', groups: [group(2, 'fast')], supported_models: [model('gpt-5.6-sol')] }]),
    ])

    expect(filterModelPlazaModels(models, { query: 'premium' })).toHaveLength(1)
    expect(filterModelPlazaModels(models, { query: 'claude' })).toHaveLength(1)
    expect(filterModelPlazaModels(models, { category: 'codex' })).toHaveLength(1)
  })
})
