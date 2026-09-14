import { describe, expect, it } from 'vitest'
import {
  accountStatsPricingRulesToAPI,
  accountStatsPricingRuleToForm,
  accountStatsRuleSectionPlatform,
  apiIntervalsToForm,
  apiTimePricingToForm,
  createDefaultTimePricingForm,
  findAccountStatsTimePricingError,
  formIntervalsToAPI,
  formTimePricingToAPI,
  isValidPositiveMultiplier,
  validateIntervals,
  validateTimePricing,
  type IntervalFormEntry,
  type PricingFormEntry,
  type TimePricingFormEntry,
  type TimePricingPeriodFormEntry,
} from '../types'
import type { AccountStatsPricingRule } from '@/api/admin/channels'

describe('interval multiplier conversion', () => {
  it('preserves component multipliers without MTok conversion', () => {
    const form = apiIntervalsToForm([{
      min_tokens: 272000,
      max_tokens: null,
      tier_label: '',
      input_price: null,
      output_price: null,
      cache_write_price: null,
      cache_read_price: null,
      input_multiplier: 2,
      output_multiplier: 1.5,
      cache_write_multiplier: 2,
      cache_read_multiplier: 2,
      per_request_price: null,
      sort_order: 0,
    }])

    expect(form[0].input_multiplier).toBe(2)
    expect(form[0].output_multiplier).toBe(1.5)
    expect(formIntervalsToAPI(form)[0]).toMatchObject({
      input_multiplier: 2,
      output_multiplier: 1.5,
      cache_write_multiplier: 2,
      cache_read_multiplier: 2,
    })
  })
})

describe('positive multiplier validation', () => {
  it('accepts empty and positive values but rejects zero and negative values', () => {
    expect(isValidPositiveMultiplier(null)).toBe(true)
    expect(isValidPositiveMultiplier('')).toBe(true)
    expect(isValidPositiveMultiplier('0.5')).toBe(true)
    expect(isValidPositiveMultiplier(0)).toBe(false)
    expect(isValidPositiveMultiplier(-1)).toBe(false)
  })

  it('rejects a zero interval multiplier', () => {
    expect(validateIntervals([
      makeInterval({ min_tokens: 100, input_multiplier: 0 }),
    ], 'token', t)).toContain('multiplierPositive')
  })
})

function makeInterval(over: Partial<IntervalFormEntry>): IntervalFormEntry {
  return {
    min_tokens: 0,
    max_tokens: null,
    tier_label: '',
    input_price: null,
    output_price: null,
    cache_write_price: null,
    cache_read_price: null,
    input_multiplier: null,
    output_multiplier: null,
    cache_write_multiplier: null,
    cache_read_multiplier: null,
    per_request_price: null,
    sort_order: 0,
    ...over,
  }
}

function t(key: string, params?: Record<string, unknown>): string {
  return `${key}${params ? ` ${JSON.stringify(params)}` : ''}`
}

describe('validateIntervals', () => {
  describe('token mode', () => {
    it('rejects unbounded interval that is not last', () => {
      const intervals: IntervalFormEntry[] = [
        makeInterval({ min_tokens: 0, max_tokens: null, input_price: 1, output_price: 1 }),
        makeInterval({ min_tokens: 200000, max_tokens: 500000, input_price: 2, output_price: 2 }),
      ]
      expect(validateIntervals(intervals, 'token', t)).toContain('unboundedLast')
    })

    it('accepts unbounded interval at the end', () => {
      const intervals: IntervalFormEntry[] = [
        makeInterval({ min_tokens: 0, max_tokens: 200000, input_price: 1, output_price: 1 }),
        makeInterval({ min_tokens: 200000, max_tokens: null, input_price: 2, output_price: 2 }),
      ]
      expect(validateIntervals(intervals, 'token', t)).toBeNull()
    })

    it('rejects overlapping intervals', () => {
      const intervals: IntervalFormEntry[] = [
        makeInterval({ min_tokens: 0, max_tokens: 250000, input_price: 1, output_price: 1 }),
        makeInterval({ min_tokens: 200000, max_tokens: 500000, input_price: 2, output_price: 2 }),
      ]
      expect(validateIntervals(intervals, 'token', t)).toContain('overlap')
    })

    it('rejects unbounded interval in token mode', () => {
      const intervals: IntervalFormEntry[] = [
        makeInterval({ min_tokens: 0, max_tokens: null, input_price: 1, output_price: 1 }),
        makeInterval({ min_tokens: 100, max_tokens: 200, input_price: 2, output_price: 2 }),
      ]
      expect(validateIntervals(intervals, 'token', t)).toContain('unboundedLast')
    })
  })

  describe('image / per_request mode', () => {
    it('allows multiple unbounded tiers identified by label', () => {
      const intervals: IntervalFormEntry[] = [
        makeInterval({ tier_label: '1K', per_request_price: 0.04 }),
        makeInterval({ tier_label: '2K', per_request_price: 0.06 }),
        makeInterval({ tier_label: '4K', per_request_price: 0.08 }),
      ]
      expect(validateIntervals(intervals, 'image', t)).toBeNull()
      expect(validateIntervals(intervals, 'per_request', t)).toBeNull()
    })

    it('still rejects negative prices', () => {
      const intervals: IntervalFormEntry[] = [
        makeInterval({ tier_label: '1K', per_request_price: -1 }),
      ]
      expect(validateIntervals(intervals, 'image', t)).toContain('negativePrice')
    })

    it('still rejects max <= min on a single tier', () => {
      const intervals: IntervalFormEntry[] = [
        makeInterval({ tier_label: '1K', min_tokens: 100, max_tokens: 50, per_request_price: 0.04 }),
      ]
      expect(validateIntervals(intervals, 'image', t)).toContain('maxGreaterThanMin')
    })
  })
})

describe('time pricing', () => {
  it('uses a disabled Shanghai default', () => {
    const form = createDefaultTimePricingForm()
    expect(form).toEqual({ timezone: 'Asia/Shanghai', periods: [], weekdays_only: false })
    expect(formTimePricingToAPI(form)).toBeNull()
  })

  it('defaults missing API day scope to every day', () => {
    expect(apiTimePricingToForm({
      timezone: 'Asia/Shanghai',
      periods: [{ start_time: '09:00', end_time: '12:00', multiplier: 2 }],
    }).weekdays_only).toBe(false)
  })

  it('round-trips day scope and formats multiplier', () => {
    const form = apiTimePricingToForm({
      timezone: 'Asia/Shanghai',
      weekdays_only: true,
      periods: [{ start_time: '09:00', end_time: '12:00', multiplier: 2 }],
    })
    expect(form.weekdays_only).toBe(true)
    expect(form.periods[0]).toEqual({
      start_time: '09:00:00',
      end_time: '12:00:00',
      multiplier: '2.00',
    })
    expect(formTimePricingToAPI(form)).toEqual({
      timezone: 'Asia/Shanghai',
      weekdays_only: true,
      periods: [{ start_time: '09:00:00', end_time: '12:00:00', multiplier: 2 }],
    })
  })

  it.each([
    ['separated', [{ start_time: '09:00:00', end_time: '12:00:00', multiplier: '2.00' }, { start_time: '14:00:00', end_time: '18:00:00', multiplier: '2.00' }], null],
    ['adjacent', [{ start_time: '09:00:00', end_time: '12:00:00', multiplier: '2.00' }, { start_time: '12:00:00', end_time: '14:00:00', multiplier: '1.50' }], null],
    ['midnight split', [{ start_time: '22:00:00', end_time: '00:00:00', multiplier: '2.00' }, { start_time: '00:00:00', end_time: '02:00:00', multiplier: '2.00' }], null],
    ['overlap by one second', [{ start_time: '09:00:00', end_time: '12:00:00', multiplier: '2.00' }, { start_time: '11:59:59', end_time: '14:00:00', multiplier: '2.00' }], 'overlap'],
    ['cross midnight', [{ start_time: '22:00:00', end_time: '02:00:00', multiplier: '2.00' }], 'range'],
    ['equal midnight', [{ start_time: '00:00:00', end_time: '00:00:00', multiplier: '2.00' }], 'range'],
    ['missing seconds', [{ start_time: '09:00', end_time: '12:00', multiplier: '2.00' }], 'format'],
    ['zero', [{ start_time: '09:00:00', end_time: '12:00:00', multiplier: '0.00' }], 'multiplier'],
    ['three decimals', [{ start_time: '09:00:00', end_time: '12:00:00', multiplier: '1.001' }], 'multiplier'],
  ])('%s', (_name, periods, errorKey) => {
    const result = validateTimePricing({
      timezone: 'Asia/Shanghai',
      periods: periods as TimePricingPeriodFormEntry[],
    }, t)
    if (errorKey === null) expect(result).toBeNull()
    else expect(result).toContain(String(errorKey))
  })

  it('rejects non-IANA timezone', () => {
    expect(validateTimePricing({
      timezone: 'UTC+8',
      periods: [{ start_time: '09:00:00', end_time: '12:00:00', multiplier: '2.00' }],
    }, t)).toContain('timezone')
  })

  it.each([
    ['missing', undefined],
    ['blank', '   '],
  ])('rejects a %s timezone without throwing during conversion', (_name, timezone) => {
    const form = {
      timezone,
      periods: [{ start_time: '09:00:00', end_time: '12:00:00', multiplier: '2.00' }],
    } as unknown as TimePricingFormEntry

    expect(validateTimePricing(form, t)).toContain('timezone')
    expect(() => formTimePricingToAPI(form)).not.toThrow()
    expect(formTimePricingToAPI(form)?.timezone).toBe('')
  })
})

function makePricingEntry(over: Partial<PricingFormEntry> = {}): PricingFormEntry {
  return {
    models: ['model'],
    billing_mode: 'token',
    input_price: null,
    output_price: null,
    cache_write_price: null,
    cache_read_price: null,
    image_input_price: null,
    image_output_price: null,
    per_request_price: null,
    intervals: [],
    time_pricing: createDefaultTimePricingForm(),
    ...over,
  }
}

function makeAPIRule(over: Partial<AccountStatsPricingRule> = {}): AccountStatsPricingRule {
  return {
    name: 'rule',
    group_ids: [],
    account_ids: [1],
    pricing: [{
      platform: '',
      models: ['model'],
      billing_mode: 'token',
      input_price: 0.000001,
      output_price: null,
      cache_write_price: null,
      cache_read_price: null,
      image_input_price: null,
      image_output_price: null,
      per_request_price: null,
      intervals: [],
      time_pricing: null,
    }],
    ...over,
  }
}

describe('account stats pricing form roundtrip', () => {
  it('round-trips one mixed-platform rule from a disabled section without splitting it', () => {
    const apiRule = makeAPIRule({
      name: 'mixed',
      pricing: [
        { ...makeAPIRule().pricing[0], platform: '', models: ['shared-*'] },
        { ...makeAPIRule().pricing[0], platform: 'openai', models: ['gpt-5'] },
      ],
    })
    const formRule = accountStatsPricingRuleToForm(apiRule, 0)

    const rules = accountStatsPricingRulesToAPI([{
      platform: 'anthropic',
      enabled: false,
      account_stats_pricing_rules: [formRule],
    }])

    expect(rules).toHaveLength(1)
    expect(rules[0].name).toBe('mixed')
    expect(rules[0].pricing.map(entry => ({ platform: entry.platform, models: entry.models }))).toEqual([
      { platform: '', models: ['shared-*'] },
      { platform: 'openai', models: ['gpt-5'] },
    ])
  })

  it('preserves wildcard entry platform and original cross-section rule order', () => {
    const first = accountStatsPricingRuleToForm(makeAPIRule({ name: 'first' }), 0)
    const second = accountStatsPricingRuleToForm(makeAPIRule({
      name: 'second',
      pricing: [{ ...makeAPIRule().pricing[0], platform: 'openai', models: ['gpt'] }],
    }), 1)
    const rules = accountStatsPricingRulesToAPI([
      { platform: 'openai', enabled: true, account_stats_pricing_rules: [second] },
      { platform: 'anthropic', enabled: false, account_stats_pricing_rules: [first] },
    ])

    expect(rules.map(rule => rule.name)).toEqual(['first', 'second'])
    expect(rules[0].pricing[0].platform).toBe('')
    expect(rules[1].pricing[0].platform).toBe('openai')
  })

  it('uses the section platform only for new entries and ignores new rules in disabled sections', () => {
    const rules = accountStatsPricingRulesToAPI([
      {
        platform: 'deepseek',
        enabled: true,
        account_stats_pricing_rules: [{ name: 'new', group_ids: [], account_ids: [1], pricing: [makePricingEntry()] }],
      },
      {
        platform: 'openai',
        enabled: false,
        account_stats_pricing_rules: [{ name: 'disabled-new', group_ids: [], account_ids: [1], pricing: [makePricingEntry()] }],
      },
    ])

    expect(rules).toHaveLength(1)
    expect(rules[0].pricing[0].platform).toBe('deepseek')
  })

  it('keeps account-only wildcard rules anchored to an existing section', () => {
    expect(accountStatsRuleSectionPlatform(
      makeAPIRule(),
      new Map(),
      ['deepseek'],
      ['anthropic', 'deepseek'],
    )).toBe('deepseek')
  })

  it('returns a supported inferred platform even when its section must be created disabled', () => {
    expect(accountStatsRuleSectionPlatform(
      makeAPIRule({
        pricing: [{ ...makeAPIRule().pricing[0], platform: 'openai' }],
      }),
      new Map(),
      ['anthropic'],
      ['anthropic', 'openai'],
    )).toBe('openai')
  })

  it('reports invalid schedules from enabled account-stat entries only', () => {
    const invalid = makePricingEntry({
      time_pricing: {
        timezone: 'Asia/Shanghai',
        weekdays_only: false,
        periods: [{ start_time: '12:00:00', end_time: '09:00:00', multiplier: '2.00' }],
      },
    })
    const result = findAccountStatsTimePricingError([{
      platform: 'deepseek',
      enabled: true,
      account_stats_pricing_rules: [{ name: 'rule', group_ids: [], account_ids: [1], pricing: [invalid] }],
    }], t)

    expect(result?.entry).toBe(invalid)
    expect(result?.error).toContain('range')
  })

  it('validates retained original rules in disabled sections but skips new ones', () => {
    const invalid = makePricingEntry({
      time_pricing: {
        timezone: 'Asia/Shanghai',
        weekdays_only: false,
        periods: [{ start_time: '12:00:00', end_time: '09:00:00', multiplier: '2.00' }],
      },
    })
    const disabledNew = {
      name: 'new', group_ids: [], account_ids: [1], pricing: [invalid],
    }
    const disabledOriginal = {
      name: 'original', group_ids: [], account_ids: [1], pricing: [invalid], original_index: 0,
    }

    expect(findAccountStatsTimePricingError([{
      platform: 'deepseek', enabled: false, account_stats_pricing_rules: [disabledNew],
    }], t)).toBeNull()
    expect(findAccountStatsTimePricingError([{
      platform: 'deepseek', enabled: false, account_stats_pricing_rules: [disabledOriginal],
    }], t)?.error).toContain('range')
  })
})
