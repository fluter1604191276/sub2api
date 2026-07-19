import { afterEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'

import AccountGroupsCell from '../AccountGroupsCell.vue'
import GroupBadge from '@/components/common/GroupBadge.vue'

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ cachedPublicSettings: null })
}))

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string, params?: { count?: number }) => params?.count == null ? key : `${key}:${params.count}`
  })
}))

const group = (id: number, name: string) => ({
  id,
  name,
  platform: 'openai' as const,
  subscription_type: 'standard' as const,
  rate_multiplier: 1
})

afterEach(() => {
  document.body.innerHTML = ''
})

describe('AccountGroupsCell', () => {
  it('wraps visible group names instead of truncating them', () => {
    const longName = 'codex 福利高性价比 pro 长名称渠道'
    const wrapper = mount(AccountGroupsCell, {
      props: {
        groups: [group(1, longName), group(2, 'codex plus 稳定渠道')]
      }
    })

    const badges = wrapper.findAllComponents(GroupBadge)
    expect(badges).toHaveLength(2)
    expect(badges[0].props('fullName')).toBe(true)

    const name = wrapper.find(`[title="${longName}"]`)
    expect(name.text()).toBe(longName)
    expect(name.classes()).toContain('whitespace-normal')
    expect(name.classes()).not.toContain('truncate')
    expect(wrapper.find('.overflow-hidden').exists()).toBe(false)
  })

  it('shows every full group name in the overflow popover', async () => {
    const groups = [
      group(1, 'codex 福利高性价比 pro 长名称渠道'),
      group(2, 'codex plus 稳定渠道'),
      group(3, 'codex 图片与文字混合渠道'),
      group(4, 'codex 备用兜底渠道')
    ]
    const wrapper = mount(AccountGroupsCell, {
      attachTo: document.body,
      props: { groups, maxDisplay: 3 }
    })

    expect(wrapper.get('button').text()).toBe('+2')
    await wrapper.get('button').trigger('click')

    const popover = document.body.querySelector('.fixed.z-50')
    expect(popover).not.toBeNull()
    for (const item of groups) {
      expect(popover?.textContent).toContain(item.name)
    }
    expect(popover?.querySelectorAll('[title]').length).toBe(groups.length)

    wrapper.unmount()
  })
})
