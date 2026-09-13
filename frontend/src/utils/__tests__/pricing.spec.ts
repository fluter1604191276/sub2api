import { describe, expect, it } from 'vitest'

import { formatScaled } from '@/utils/pricing'

describe('formatScaled currency display', () => {
  it('uses the CNY symbol without converting the site credit value', () => {
    expect(formatScaled(3e-6, 1_000_000, 0, 'CNY')).toBe('¥3')
  })

  it('keeps USD as the backward-compatible default', () => {
    expect(formatScaled(3e-6, 1_000_000)).toBe('$3')
  })
})
