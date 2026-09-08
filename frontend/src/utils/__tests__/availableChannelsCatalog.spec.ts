import { describe, expect, it } from 'vitest'
import {
  availableCatalogPlatforms,
  filterAvailableChannels,
  summarizeAvailableChannels
} from '../availableChannelsCatalog'
import type { UserAvailableChannel } from '@/api/channels'

const channels: UserAvailableChannel[] = [
  {
    name: 'Primary',
    description: 'Fast access',
    platforms: [
      {
        platform: 'openai',
        groups: [
          {
            id: 1,
            name: 'Public GPT',
            platform: 'openai',
            subscription_type: 'standard',
            rate_multiplier: 1,
            peak_rate_enabled: false,
            peak_start: '',
            peak_end: '',
            peak_rate_multiplier: 1,
            is_exclusive: false
          },
          {
            id: 2,
            name: 'Private GPT',
            platform: 'openai',
            subscription_type: 'standard',
            rate_multiplier: 0.8,
            peak_rate_enabled: false,
            peak_start: '',
            peak_end: '',
            peak_rate_multiplier: 1,
            is_exclusive: true
          }
        ],
        supported_models: [
          { name: 'gpt-5.6-sol', platform: 'openai', pricing: null },
          { name: 'gpt-5.6-luna', platform: 'openai', pricing: null }
        ]
      },
      {
        platform: 'anthropic',
        groups: [],
        supported_models: [{ name: 'claude-opus-5', platform: 'anthropic', pricing: null }]
      }
    ]
  }
]

describe('available channel catalog filters', () => {
  it('filters by platform and access while preserving the aggregate shape', () => {
    const result = filterAvailableChannels(channels, {
      platform: 'openai',
      access: 'exclusive'
    })

    expect(result).toHaveLength(1)
    expect(result[0].platforms).toHaveLength(1)
    expect(result[0].platforms[0].groups.map((group) => group.name)).toEqual(['Private GPT'])
  })

  it('matches model names without losing the channel context', () => {
    const result = filterAvailableChannels(channels, { query: 'opus-5' })

    expect(result[0].name).toBe('Primary')
    expect(result[0].platforms.map((section) => section.platform)).toEqual(['anthropic'])
  })

  it('summarizes the filtered result with unique group and model counts', () => {
    expect(summarizeAvailableChannels(channels)).toEqual({
      channels: 1,
      platforms: 2,
      groups: 2,
      models: 3
    })
  })

  it('keeps compatible models under their request protocol platform', () => {
    const mixed: UserAvailableChannel[] = [{
      name: 'CN models',
      description: '',
      platforms: [{
        platform: 'openai',
        groups: [],
        supported_models: [
          { name: 'kimi-k3', platform: 'openai', pricing: null },
          { name: 'deepseek-v4-pro', platform: 'openai', pricing: null },
          { name: 'gpt-5.6-sol', platform: 'openai', pricing: null }
        ]
      }]
    }]

    expect(availableCatalogPlatforms(mixed)).toEqual(['openai'])
    const openai = filterAvailableChannels(mixed, { platform: 'openai' })
    expect(openai[0].platforms).toHaveLength(1)
    expect(openai[0].platforms[0].platform).toBe('openai')
    expect(openai[0].platforms[0].supported_models.map((model) => model.name)).toEqual([
      'kimi-k3',
      'deepseek-v4-pro',
      'gpt-5.6-sol'
    ])
    expect(filterAvailableChannels(mixed, { platform: 'kimi' })).toEqual([])
  })
})
