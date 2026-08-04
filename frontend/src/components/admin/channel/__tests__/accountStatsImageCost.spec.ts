import { describe, expect, it } from 'vitest'

import { findAccountStatsPricingConflict, isAccountStatsImageTierLabel } from '../accountStatsImageCost'
import type { PricingFormEntry } from '../types'

function entry(overrides: Partial<PricingFormEntry>): PricingFormEntry {
  return {
    models: ['claude-sonnet'],
    billing_mode: 'token',
    input_price: null,
    output_price: null,
    cache_write_price: null,
    cache_read_price: null,
    image_output_price: null,
    per_request_price: null,
    intervals: [],
    image_operation: null,
    ...overrides,
  }
}

describe('findAccountStatsPricingConflict', () => {
  it('allows the same model in token and image scopes', () => {
    expect(findAccountStatsPricingConflict([
      entry({ models: ['gpt-image-1'], billing_mode: 'token' }),
      entry({ models: ['gpt-image-1'], billing_mode: 'image' }),
    ])).toBeNull()
  })

  it('allows the same image model for generation and edit operations', () => {
    expect(findAccountStatsPricingConflict([
      entry({ models: ['gpt-image-1'], billing_mode: 'image', image_operation: 'generation' }),
      entry({ models: ['gpt-image-1'], billing_mode: 'image', image_operation: 'edit' }),
    ])).toBeNull()
  })

  it('conflicts the same model in token and ordinary per-request scopes', () => {
    expect(findAccountStatsPricingConflict([
      entry({ models: ['claude-sonnet'], billing_mode: 'token' }),
      entry({ models: ['claude-sonnet'], billing_mode: 'per_request' }),
    ])).toEqual(['claude-sonnet', 'claude-sonnet'])
  })

  it('returns a pair for duplicate same image model and same operation', () => {
    expect(findAccountStatsPricingConflict([
      entry({ models: ['gpt-image-1'], billing_mode: 'image', image_operation: 'generation' }),
      entry({ models: ['gpt-image-1'], billing_mode: 'image', image_operation: 'generation' }),
    ])).toEqual(['gpt-image-1', 'gpt-image-1'])
  })

  it('detects wildcard conflicts within the same account-stats image operation scope', () => {
    expect(findAccountStatsPricingConflict([
      entry({ models: ['gpt-image-*'], billing_mode: 'image', image_operation: 'responses' }),
      entry({ models: ['gpt-image-1'], billing_mode: 'image', image_operation: 'responses' }),
    ])).toEqual(['gpt-image-*', 'gpt-image-1'])
  })
})

describe('isAccountStatsImageTierLabel', () => {
  it('accepts canonical image tiers case-insensitively', () => {
    expect(isAccountStatsImageTierLabel('1K')).toBe(true)
    expect(isAccountStatsImageTierLabel(' 2k ')).toBe(true)
    expect(isAccountStatsImageTierLabel('4K')).toBe(true)
  })

  it('rejects tiers the account-cost resolver cannot use', () => {
    expect(isAccountStatsImageTierLabel('HD')).toBe(false)
    expect(isAccountStatsImageTierLabel('')).toBe(false)
  })
})
