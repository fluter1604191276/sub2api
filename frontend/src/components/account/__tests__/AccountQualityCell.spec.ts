import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'

import AccountQualityCell from '../AccountQualityCell.vue'

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string, params: Record<string, string | number> = {}) => {
      const value = ({
      'admin.accounts.quality.last10': '近10',
      'admin.accounts.quality.last100': '近100',
      'admin.accounts.quality.firstTokenShort': '首字',
      'admin.accounts.quality.totalShort': '总',
      'admin.accounts.quality.durationOnly': '仅按总耗时评分',
      'admin.accounts.quality.activity.active': '活跃',
      'admin.accounts.quality.activity.low_sample': '样本少',
      'admin.accounts.quality.activity.degraded': '波动',
      'admin.accounts.quality.activity.failing': '失败中',
      'admin.accounts.quality.activity.idle': '未参与',
      'admin.accounts.quality.activity.unassigned': '未分组',
      'admin.accounts.quality.activity.paused': '暂停调度',
      'admin.accounts.quality.activity.counts': '{success}成/{failed}败',
      'admin.accounts.quality.activity.lastSuccessMinutes': '最近成功 {count} 分钟前',
      'admin.accounts.quality.activity.noSuccess24h': '24h无成功',
      }[key] ?? key)
      return Object.entries(params).reduce(
        (result, [name, replacement]) => result.replace(`{${name}}`, String(replacement)),
        value,
      )
    },
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

  it('shows realtime participation separately from the latency score', () => {
    const wrapper = mount(AccountQualityCell, {
      props: {
        stats: {
          last_10: {
            sample_count: 10,
            first_token_sample_count: 10,
            average_first_token_ms: 1200,
            average_duration_ms: 8000,
            quality_score: 94,
            quality_grade: 'S',
            score_basis: 'ttft_duration',
          },
          last_100: {
            sample_count: 24,
            first_token_sample_count: 24,
            average_first_token_ms: 1800,
            average_duration_ms: 10000,
            quality_score: 90,
            quality_grade: 'S',
            score_basis: 'ttft_duration',
          },
          window_hours: 1,
        },
        activity: {
          state: 'active',
          successful_request_count: 24,
          failed_request_count: 1,
          last_success_at: new Date(Date.now() - 5 * 60_000).toISOString(),
          last_error_at: null,
        },
        showActivity: true,
      },
    })

    expect(wrapper.text()).toContain('活跃')
    expect(wrapper.text()).toContain('24成/1败')
    expect(wrapper.text()).toContain('最近成功 5 分钟前')
    expect(wrapper.find('[data-quality-activity="active"]').classes()).toContain('bg-emerald-100')
  })

  it('does not paint an idle account red and allows scheduling state overrides', async () => {
    const wrapper = mount(AccountQualityCell, {
      props: {
        stats: {
          last_10: {
            sample_count: 0,
            first_token_sample_count: 0,
            average_first_token_ms: null,
            average_duration_ms: null,
            quality_score: null,
          },
          last_100: {
            sample_count: 0,
            first_token_sample_count: 0,
            average_first_token_ms: null,
            average_duration_ms: null,
            quality_score: null,
          },
          window_hours: 1,
        },
        activity: {
          state: 'idle',
          successful_request_count: 0,
          failed_request_count: 0,
          last_success_at: null,
          last_error_at: null,
        },
        showActivity: true,
      },
    })

    expect(wrapper.text()).toContain('未参与')
    expect(wrapper.find('[data-quality-activity="idle"]').classes()).toContain('bg-gray-100')
    expect(wrapper.find('[data-quality-activity="idle"]').classes()).not.toContain('bg-red-100')

    await wrapper.setProps({ activityStateOverride: 'unassigned' })
    expect(wrapper.text()).toContain('未分组')
  })
})
