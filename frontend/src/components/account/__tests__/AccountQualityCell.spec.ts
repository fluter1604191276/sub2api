import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'

import AccountQualityCell from '../AccountQualityCell.vue'

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string) => ({
      'admin.accounts.quality.last10': '近10',
      'admin.accounts.quality.last100': '近100',
      'admin.accounts.quality.firstTokenShort': '首字',
      'admin.accounts.quality.totalShort': '总',
      'admin.accounts.quality.durationOnly': '仅按总耗时评分',
    }[key] ?? key),
  }),
}))

describe('AccountQualityCell', () => {
  it('shows the letter grade beside the numeric score for both windows', () => {
    const wrapper = mount(AccountQualityCell, {
      props: {
        stats: {
          last_10: {
            sample_count: 10,
            first_token_sample_count: 10,
            average_first_token_ms: 7700,
            average_duration_ms: 20000,
            quality_score: 73,
            quality_grade: 'A-',
            score_basis: 'ttft_duration',
          },
          last_100: {
            sample_count: 100,
            first_token_sample_count: 0,
            average_first_token_ms: null,
            average_duration_ms: 2800,
            quality_score: 69,
            quality_grade: 'B+',
            score_basis: 'duration_only',
          },
          window_hours: 24,
          score_version: 2,
        } as any,
      },
    })

    expect(wrapper.text()).toContain('A- 73')
    expect(wrapper.text()).toContain('B+ 69')
    expect(wrapper.text()).toContain('首字 7.7s')
    expect(wrapper.text()).toContain('总 20s')
    expect(wrapper.find('[data-quality-grade="A-"]').classes()).toContain('bg-blue-100')
    expect(wrapper.find('[data-quality-grade="B+"]').classes()).toContain('bg-amber-100')
  })
})
