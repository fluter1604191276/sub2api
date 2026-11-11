import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

const { getPublicChannelMonitorV3 } = vi.hoisted(() => ({ getPublicChannelMonitorV3: vi.fn() }))

vi.mock('@/api/channelMonitorV3', () => ({ getPublicChannelMonitorV3 }))
vi.mock('vue-i18n', async () => ({
  ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'),
  useI18n: () => ({
    t: (key: string, values?: { count?: number }) => values?.count === undefined ? key : `${values.count} samples`,
  }),
}))

import ChannelStatusV3View from '@/views/user/ChannelStatusV3View.vue'

const publicResponse = {
  groups: [{
    group_id: 7,
    name: 'Public OpenAI',
    last_10: { grade: 'A', sample_count: 1 },
    last_100: { grade: '', sample_count: 1 },
  }],
}

function mountView() {
  return mount(ChannelStatusV3View)
}

describe('ChannelStatusV3View', () => {
  beforeEach(() => {
    getPublicChannelMonitorV3.mockReset().mockResolvedValue(publicResponse)
  })

  it('fetches and renders only group names, recent grades, and sample counts', async () => {
    const wrapper = mountView()
    await flushPromises()
    expect(getPublicChannelMonitorV3).toHaveBeenCalledOnce()
    expect(wrapper.get('[data-testid="group-row"]').text()).toContain('Public OpenAI')
    expect(wrapper.text()).toContain('A')
    expect(wrapper.text()).toContain('channelMonitorV3.noData')
    expect(wrapper.text()).toContain('1 samples')
    expect(wrapper.text()).not.toMatch(/TTFT|p90|latency|duration|last used/i)
  })

  it('uses one sample as a valid grade and exposes descriptive grade text', async () => {
    const wrapper = mountView()
    await flushPromises()
    const grades = wrapper.findAll('[aria-label]')
    expect(grades.some((element) => element.attributes('aria-label')?.includes('A'))).toBe(true)
    expect(grades.some((element) => element.attributes('aria-label')?.includes('1 samples'))).toBe(true)
  })

  it('shows an empty state when no active visible groups are returned', async () => {
    getPublicChannelMonitorV3.mockResolvedValueOnce({ groups: [] })
    const wrapper = mountView()
    await flushPromises()
    expect(wrapper.get('[data-testid="empty"]').exists()).toBe(true)
  })

  it('shows an error and refreshes on demand', async () => {
    getPublicChannelMonitorV3.mockRejectedValueOnce(new Error('unavailable'))
    const wrapper = mountView()
    await flushPromises()
    expect(wrapper.get('[data-testid="error"]').exists()).toBe(true)
    await wrapper.get('[data-testid="refresh"]').trigger('click')
    await flushPromises()
    expect(getPublicChannelMonitorV3).toHaveBeenCalledTimes(2)
    expect(wrapper.find('[data-testid="group-row"]').exists()).toBe(true)
  })
})
