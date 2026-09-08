import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'

import AccountUnifiedQualityCell from '../AccountUnifiedQualityCell.vue'

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string, params: Record<string, string | number> = {}) => {
      const value = ({
        'admin.accounts.quality.unified.confidence': '{percent}%置信',
        'admin.accounts.quality.unified.title': '{grade}级 {score}分 {source} {confidence}% {count}/{firstCount}',
        'admin.accounts.quality.unified.sources.realtime_blend': '实时+基线',
        'admin.accounts.quality.unified.sources.realtime_only': '仅实时',
        'admin.accounts.quality.unified.sources.historical': '24h基线',
        'admin.accounts.quality.unified.sources.unscored': '待评分',
        'admin.accounts.quality.activity.active': '活跃',
        'admin.accounts.quality.activity.idle': '未参与',
		'admin.accounts.quality.activity.paused': '已暂停',
		'admin.accounts.quality.activity.unassigned': '未分组',
      }[key] ?? key)
      return Object.entries(params).reduce(
        (result, [name, replacement]) => result.replace(`{${name}}`, String(replacement)),
        value,
      )
    },
  }),
}))

describe('AccountUnifiedQualityCell', () => {
  it('shows the blended grade, score, activity, source, and confidence', () => {
    const wrapper = mount(AccountUnifiedQualityCell, {
      props: {
        quality: {
          score: 82,
          grade: 'A+',
          confidence: 0.87,
          source: 'realtime_blend',
          sample_count: 100,
          first_token_sample_count: 94,
        },
        activity: {
          state: 'active',
          successful_request_count: 12,
          failed_request_count: 1,
          last_success_at: '2026-08-12T00:00:00Z',
          last_error_at: null,
        },
      },
    })

    expect(wrapper.text()).toContain('A+')
    expect(wrapper.text()).toContain('82/100')
    expect(wrapper.text()).toContain('活跃')
    expect(wrapper.text()).toContain('实时+基线')
    expect(wrapper.text()).toContain('87%置信')
    expect(wrapper.find('[data-unified-quality-grade="A+"]').classes()).toContain('bg-blue-100')
  })

  it('keeps a historical-only score visible with its lower-confidence source', () => {
    const wrapper = mount(AccountUnifiedQualityCell, {
      props: {
        quality: {
          score: 85,
          grade: 'S-',
          confidence: 0.7,
          source: 'historical',
          sample_count: 100,
          first_token_sample_count: 90,
        },
      },
    })

    expect(wrapper.text()).toContain('S-')
    expect(wrapper.text()).toContain('24h基线')
    expect(wrapper.text()).toContain('70%置信')
  })

  it('uses an account availability override instead of showing stale active state', () => {
	const wrapper = mount(AccountUnifiedQualityCell, {
	  props: {
		quality: {
		  score: 91,
		  grade: 'S',
		  confidence: 0.9,
		  source: 'realtime_blend',
		  sample_count: 100,
		  first_token_sample_count: 100,
		},
		activity: {
		  state: 'active',
		  successful_request_count: 12,
		  failed_request_count: 0,
		  last_success_at: '2026-08-12T00:00:00Z',
		  last_error_at: null,
		},
		activityStateOverride: 'paused',
	  },
	})

	expect(wrapper.text()).toContain('已暂停')
	expect(wrapper.text()).not.toContain('活跃')
	expect(wrapper.find('.text-gray-400').exists()).toBe(true)
  })

  it('shows an availability override even when there is no activity aggregate', () => {
	const wrapper = mount(AccountUnifiedQualityCell, {
	  props: {
		quality: {
		  score: 80,
		  grade: 'A',
		  confidence: 0.7,
		  source: 'historical',
		  sample_count: 100,
		  first_token_sample_count: 90,
		},
		activityStateOverride: 'unassigned',
	  },
	})

	expect(wrapper.text()).toContain('未分组')
  })

  it('shows a dash when there is no score', () => {
    const wrapper = mount(AccountUnifiedQualityCell, {
      props: {
        quality: {
          score: null,
          confidence: 0,
          source: 'unscored',
          sample_count: 0,
          first_token_sample_count: 0,
        },
      },
    })

    expect(wrapper.text()).toBe('-')
  })
})
