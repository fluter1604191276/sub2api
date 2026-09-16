/**
 * formatScaled formats a per-token (or per-request) USD price scaled by `scale`.
 *
 *   formatScaled(0.000003, 1_000_000)    → "$3"      // per 1M tokens
 *   formatScaled(0.000003, 1_000_000, 0, 'CNY') → "¥3"
 *   formatScaled(0.5,        1)          → "$0.5"    // per request
 *   formatScaled(null,       1_000_000)  → "-"
 *   formatScaled(0.000003, 1_000_000, 2) → "$3.00"   // pad to ≥2 decimals
 *   formatScaled(1.25e-8,  1_000_000, 2) → "$0.0125" // longer decimals kept as-is
 *
 * Uses toPrecision(10) then strips trailing zeros to avoid IEEE 754 display noise.
 * `minFractionDigits` pads the result back up to a minimum number of decimals.
 */
export type PricingCurrency = 'USD' | 'CNY'

export function formatScaled(
  value: number | null,
  scale: number,
  minFractionDigits = 0,
  currency: PricingCurrency = 'USD',
): string {
  if (value == null) return '-'
  let s = (value * scale).toPrecision(10).replace(/\.?0+$/, '')
  if (minFractionDigits > 0 && !s.includes('e')) {
    const dot = s.indexOf('.')
    const digits = dot === -1 ? 0 : s.length - dot - 1
    if (digits < minFractionDigits) {
      s = (dot === -1 ? `${s}.` : s) + '0'.repeat(minFractionDigits - digits)
    }
  }
  return `${currency === 'CNY' ? '¥' : '$'}${s}`
}

import type { UserPricingInterval, UserSupportedModelPricing } from '@/api/channels'

type TokenPrices = Pick<UserPricingInterval, 'input_price' | 'output_price' | 'cache_write_price' | 'cache_write_1h_price' | 'cache_read_price'>

export function resolveIntervalPrices(iv: UserPricingInterval, base: TokenPrices): UserPricingInterval {
  const price = (absolute: number | null | undefined, multiplier: number | null | undefined, fallback: number | null | undefined) =>
    absolute ?? (fallback == null ? null : fallback * (multiplier ?? 1))
  return {
    ...iv,
    input_price: price(iv.input_price, iv.input_multiplier, base.input_price),
    output_price: price(iv.output_price, iv.output_multiplier, base.output_price),
    cache_write_price: price(iv.cache_write_price, iv.cache_write_multiplier, base.cache_write_price),
    // Resolver uses an explicit cache-write price for both durations unless 1h is overridden.
    cache_write_1h_price: iv.cache_write_1h_price ?? iv.cache_write_price ?? price(null, iv.cache_write_multiplier, base.cache_write_1h_price),
    cache_read_price: price(iv.cache_read_price, iv.cache_read_multiplier, base.cache_read_price)
  }
}

export interface EffectivePricingOptions {
  /** Image billing can use a group-level rate independent from the normal group/user rate. */
  imageRateMultiplier?: number | null
}

/**
 * Resolves the user-facing effective price without mutating configured base prices.
 * Currency is display metadata only: values stay in the site's unified balance unit.
 */
export function resolveEffectivePricing(
  pricing: UserSupportedModelPricing,
  rateMultiplier: number,
  options: EffectivePricingOptions = {},
): UserSupportedModelPricing {
  const requestRate = pricing.billing_mode === 'image' && options.imageRateMultiplier != null
    ? options.imageRateMultiplier
    : rateMultiplier
  const scaled = (value: number | null | undefined, rate = rateMultiplier): number | null =>
    value == null ? null : value * rate

  return {
    ...pricing,
    input_price: scaled(pricing.input_price),
    output_price: scaled(pricing.output_price),
    cache_write_price: scaled(pricing.cache_write_price),
    cache_write_1h_price: scaled(pricing.cache_write_1h_price),
    cache_read_price: scaled(pricing.cache_read_price),
    image_input_price: scaled(pricing.image_input_price),
    image_output_price: scaled(pricing.image_output_price),
    per_request_price: scaled(pricing.per_request_price, requestRate),
    intervals: pricing.intervals.map((interval) => {
      if (pricing.billing_mode !== 'token') {
        return {
          ...interval,
          per_request_price: scaled(interval.per_request_price, requestRate),
        }
      }
      const resolved = resolveIntervalPrices(interval, pricing)
      return {
        ...resolved,
        input_price: scaled(resolved.input_price),
        output_price: scaled(resolved.output_price),
        cache_write_price: scaled(resolved.cache_write_price),
        cache_write_1h_price: scaled(resolved.cache_write_1h_price),
        cache_read_price: scaled(resolved.cache_read_price),
      }
    }),
  }
}
