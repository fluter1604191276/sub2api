import { describe, expect, it } from 'vitest'
import {
  buildAvailableChannelDisplayRows,
  getAvailableChannelCategory,
} from '@/utils/availableChannels'
import type { UserSupportedModel } from '@/api/channels'

function model(name: string, platform = 'openai'): UserSupportedModel {
  return { name, platform, pricing: null }
}

describe('available channel catalogue categorization', () => {
  it('classifies by model identity before protocol platform', () => {
    expect(getAvailableChannelCategory(model('deepseek-v4-pro', 'openai'))).toBe('domestic')
    expect(getAvailableChannelCategory(model('glm-5.3', 'anthropic'))).toBe('domestic')
    expect(getAvailableChannelCategory(model('kimi-k3', 'openai'))).toBe('domestic')
    expect(getAvailableChannelCategory(model('claude-sonnet-4-6', 'openai'))).toBe('claude')
  })

  it('keeps image and video models in the image section', () => {
    expect(getAvailableChannelCategory(model('gpt-image-2'))).toBe('image')
    expect(getAvailableChannelCategory(model('seedance-2.0'))).toBe('image')
    expect(getAvailableChannelCategory(model('video-ds-2.0'))).toBe('image')
  })

  it('splits mixed platform sections into stable category rows', () => {
    const rows = buildAvailableChannelDisplayRows([{
      name: 'mixed channel',
      description: 'test',
      platforms: [{
        platform: 'openai',
        groups: [],
        supported_models: [model('gpt-5.6-sol'), model('deepseek-v4-flash')],
      }],
    }])

    expect(rows.map((row) => [row.category, row.models.map((item) => item.name)])).toEqual([
      ['codex', ['gpt-5.6-sol']],
      ['domestic', ['deepseek-v4-flash']],
    ])
  })
})
