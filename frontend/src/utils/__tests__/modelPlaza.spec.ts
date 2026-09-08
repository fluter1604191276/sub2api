import { describe, expect, it } from 'vitest'
import type { UserAvailableGroup } from '@/api/channels'
import { buildModelPlazaGroups, buildModelPlazaModels, filterModelPlazaGroups } from '../modelPlaza'

const group = (overrides: Partial<UserAvailableGroup> = {}): UserAvailableGroup => ({
  id: 1, name: 'Standard', platform: 'openai', subscription_type: 'standard', rate_multiplier: 1,
  peak_rate_enabled: true, peak_start: '09:00', peak_end: '18:00', peak_rate_multiplier: 1.5,
  is_exclusive: false, ...overrides,
})
const pricing = (input_price: number) => ({ billing_mode: 'token' as const, input_price, output_price: 2, cache_write_price: null, cache_read_price: null, image_input_price: null, image_output_price: null, per_request_price: null, intervals: [] })

describe('model plaza helpers', () => {
  it('aggregates models, channels and source pricing', () => {
    const channels = [
      { name: 'A', description: 'a', platforms: [{ platform: 'openai', groups: [group()], supported_models: [{ name: 'gpt-4', platform: 'openai', pricing: pricing(1) }] }] },
      { name: 'B', description: 'b', platforms: [{ platform: 'openai', groups: [group({ id: 2, name: 'Premium', rate_multiplier: 0.8 })], supported_models: [{ name: 'gpt-4', platform: 'openai', pricing: pricing(3) }] }] },
    ]
    const models = buildModelPlazaModels(channels)
    expect(models[0].channels).toEqual(['A', 'B'])
    expect(models[0].sources).toHaveLength(2)
    expect(models[0].pricing?.input_price).toBe(1)
    const groups = buildModelPlazaGroups(models, { 2: 0.6 })
    expect(groups.map((g) => g.name)).toEqual(['Premium', 'Standard'])
    expect(groups[0].rateMultiplier).toBe(0.6)
    expect(groups[0].models[0].channels).toEqual(['B'])
    expect(groups[0].models[0].pricing?.input_price).toBe(3)
    expect(groups[1].models[0].sources.map((s) => s.channelName)).toEqual(['A'])
    expect(groups[1].peakRateEnabled).toBe(true)
  })

  it('keeps ungrouped models isolated and supports all filters', () => {
    const channels = [{ name: 'Local', description: '', platforms: [{ platform: 'gemini', groups: [], supported_models: [{ name: 'flash', platform: 'gemini', pricing: null }] }] }]
    const groups = buildModelPlazaGroups(buildModelPlazaModels(channels), {})
    expect(groups).toHaveLength(1)
    expect(groups[0].id).toBeNull()
    expect(groups[0].key).toContain('__ungrouped')
    expect(filterModelPlazaGroups(groups, { query: 'flash', platform: 'gemini', category: 'gemini', groupKey: groups[0].key })).toHaveLength(1)
    expect(filterModelPlazaGroups(groups, { rateMultiplier: '1' })).toHaveLength(0)
  })
})
