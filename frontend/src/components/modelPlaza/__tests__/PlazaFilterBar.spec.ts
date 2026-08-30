import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import PlazaFilterBar from '../PlazaFilterBar.vue'

vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string) => key })
}))

const groups = [
  {
    id: 1,
    name: 'Public OpenAI',
    platform: 'openai',
    rate: 1,
    isExclusive: false,
    subscriptionType: 'standard'
  },
  {
    id: 2,
    name: 'Exclusive OpenAI',
    platform: 'openai',
    rate: 1,
    isExclusive: true,
    subscriptionType: 'standard'
  },
  {
    id: 3,
    name: 'Subscription Anthropic',
    platform: 'anthropic',
    rate: 2,
    isExclusive: false,
    subscriptionType: 'subscription'
  }
]

function mountFilter(overrides: Record<string, unknown> = {}) {
  return mount(PlazaFilterBar, {
    props: {
      platforms: ['anthropic', 'openai'],
      groups,
      rates: [1, 2],
      platform: 'openai',
      access: 'all',
      groupId: 'all',
      rate: 'all',
      search: '',
      ...overrides
    },
    global: {
      stubs: {
        Icon: true,
        PlatformIcon: true
      }
    }
  })
}

describe('PlazaFilterBar 范围筛选联动', () => {
  it('在当前平台和倍率下置灰没有结果的订阅范围', () => {
    const wrapper = mountFilter({ rate: 1 })

    expect(wrapper.get('[data-testid="plaza-access-all"]').attributes('disabled')).toBeUndefined()
    expect(wrapper.get('[data-testid="plaza-access-public"]').attributes('disabled')).toBeUndefined()
    expect(wrapper.get('[data-testid="plaza-access-exclusive"]').attributes('disabled')).toBeUndefined()
    expect(wrapper.get('[data-testid="plaza-access-subscription"]').attributes('disabled')).toBeDefined()
  })

  it('点击范围筛选会发出对应更新事件', async () => {
    const wrapper = mountFilter()

    await wrapper.get('[data-testid="plaza-access-public"]').trigger('click')

    expect(wrapper.emitted('update:access')).toEqual([['public']])
  })

  it('选择订阅分组后仍允许订阅范围并置灰其他范围', () => {
    const wrapper = mountFilter({ platform: 'all', rate: 2, groupId: 3 })

    expect(wrapper.get('[data-testid="plaza-access-subscription"]').attributes('disabled')).toBeUndefined()
    expect(wrapper.get('[data-testid="plaza-access-public"]').attributes('disabled')).toBeDefined()
    expect(wrapper.get('[data-testid="plaza-access-exclusive"]').attributes('disabled')).toBeDefined()
  })
})
