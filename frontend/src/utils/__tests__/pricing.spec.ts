import { describe, expect, it } from 'vitest'

import { formatScaled, resolveEffectivePricing } from '@/utils/pricing'
import type { UserSupportedModelPricing } from '@/api/channels'

describe('formatScaled currency display', () => {
  it('uses the CNY symbol without converting the site credit value', () => {
    expect(formatScaled(3e-6, 1_000_000, 0, 'CNY')).toBe('¥3')
  })

  it('keeps USD as the backward-compatible default', () => {
    expect(formatScaled(3e-6, 1_000_000)).toBe('$3')
  })
})

const tokenPricing = (): UserSupportedModelPricing => ({
  currency: 'CNY',
  billing_mode: 'token',
  input_price: 1.4e-6,
  output_price: 4.4e-6,
  cache_write_price: 0.26e-6,
  cache_write_1h_price: 0,
  cache_read_price: 0.13e-6,
  image_input_price: 0,
  image_output_price: 2e-6,
  per_request_price: null,
  intervals: [{
    min_tokens: 200_000,
    max_tokens: null,
    input_price: null,
    output_price: null,
    cache_write_price: null,
    cache_write_1h_price: 0,
    cache_read_price: null,
    input_multiplier: 2,
    output_multiplier: 1.5,
    cache_write_multiplier: 2,
    cache_read_multiplier: 0.5,
    per_request_price: null,
  }],
})

describe('resolveEffectivePricing', () => {
  it('resolves GLM group prices while preserving currency metadata and configured bases', () => {
    const pricing = tokenPricing()
    const before = structuredClone(pricing)
    const resolved = resolveEffectivePricing(pricing, 0.6)

    expect(resolved.currency).toBe('CNY')
    expect(resolved.input_price).toBeCloseTo(0.84e-6)
    expect(resolved.output_price).toBeCloseTo(2.64e-6)
    expect(resolved.cache_write_price).toBeCloseTo(0.156e-6)
    expect(resolved.cache_write_1h_price).toBe(0)
    expect(resolved.image_input_price).toBe(0)
    expect(pricing).toEqual(before)
  })

  it('preserves free prices and accepts a zero user rate', () => {
    const resolved = resolveEffectivePricing(tokenPricing(), 0)
    expect(resolved.input_price).toBe(0)
    expect(resolved.output_price).toBe(0)
    expect(resolved.cache_read_price).toBe(0)
    expect(resolved.intervals[0].input_price).toBe(0)
  })

  it('resolves tier overrides and cache multipliers before applying the effective rate', () => {
    const resolved = resolveEffectivePricing(tokenPricing(), 0.5)
    expect(resolved.intervals[0]).toMatchObject({
      input_price: 1.4e-6,
      output_price: 3.3e-6,
      cache_write_price: 0.26e-6,
      cache_write_1h_price: 0,
      cache_read_price: 0.0325e-6,
    })
  })

  it('uses an independent image rate for image per-request prices and group rate otherwise', () => {
    const image: UserSupportedModelPricing = {
      ...tokenPricing(),
      billing_mode: 'image',
      per_request_price: 0.2,
      intervals: [{ ...tokenPricing().intervals[0], tier_label: '2K', per_request_price: 0.3 }],
    }
    expect(resolveEffectivePricing(image, 0.6, { imageRateMultiplier: 0.8 }).per_request_price).toBeCloseTo(0.16)
    expect(resolveEffectivePricing(image, 0.6, { imageRateMultiplier: 0.8 }).intervals[0].per_request_price).toBeCloseTo(0.24)
    expect(resolveEffectivePricing(image, 0.6).per_request_price).toBeCloseTo(0.12)
  })
})
