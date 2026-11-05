import { defineComponent } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import ChannelMonitorV3View from '@/views/admin/ChannelMonitorV3View.vue'

const { getConfig, updateSettings, updateGroup, showSuccess, showError } = vi.hoisted(() => ({
  getConfig: vi.fn(),
  updateSettings: vi.fn(),
  updateGroup: vi.fn(),
  showSuccess: vi.fn(),
  showError: vi.fn(),
}))

vi.mock('@/api/admin/channelMonitorV3', () => ({
  channelMonitorV3API: { getConfig, updateSettings, updateGroup },
  default: { getConfig, updateSettings, updateGroup },
}))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showSuccess, showError }) }))
vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return { ...actual, useI18n: () => ({ t: (key: string) => key }) }
})

const AppLayoutStub = defineComponent({ template: '<main><slot /></main>' })
const IconStub = defineComponent({ template: '<i />' })
const config = {
  settings: { enabled: false, daily_budget_usd: 2 },
  groups: [{ group_id: 7, name: 'OpenAI', platform: 'openai', status: 'active', visible: true, probe_interval_seconds: 600, test_model: 'gpt-5.5' }],
  budget: { date: '2026-11-05', spent_usd: 0.125, reserved_usd: 0.25, remaining_usd: 1.625 },
}

function mountView() {
  return mount(ChannelMonitorV3View, { global: { stubs: { AppLayout: AppLayoutStub, Icon: IconStub, RouterLink: true } } })
}

describe('ChannelMonitorV3View', () => {
  beforeEach(() => {
    for (const fn of [getConfig, updateSettings, updateGroup, showSuccess, showError]) fn.mockReset()
    getConfig.mockResolvedValue({ settings: { ...config.settings }, groups: config.groups.map((group) => ({ ...group })), budget: { ...config.budget } })
    updateSettings.mockResolvedValue({ enabled: true, daily_budget_usd: 3 })
    updateGroup.mockResolvedValue({ ...config.groups[0], visible: false, probe_interval_seconds: 0, test_model: '' })
  })

  it('loads groups and saves global and per-group settings', async () => {
    const wrapper = mountView()
    await flushPromises()
    expect(getConfig).toHaveBeenCalledOnce()
    expect(wrapper.text()).toContain('OpenAI')
    expect(wrapper.get('[data-testid="budget-status"]').text()).toContain('$0.1250')
    expect(wrapper.get('[data-testid="budget-status"]').text()).toContain('$0.2500')

    await wrapper.get('[data-testid="global-enabled"]').setValue(true)
    await wrapper.get('[data-testid="daily-budget"]').setValue(3)
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(updateSettings).toHaveBeenCalledWith({ enabled: true, daily_budget_usd: 3 })

    await wrapper.get('[data-testid="group-visible-7"]').setValue(false)
    await wrapper.get('[data-testid="group-interval-7"]').setValue(0)
    await wrapper.get('[data-testid="group-model-7"]').setValue('')
    await wrapper.get('[data-testid="save-group-7"]').trigger('click')
    await flushPromises()
    expect(updateGroup).toHaveBeenCalledWith(7, { visible: false, probe_interval_seconds: 0, test_model: '' })
  })

  it('validates budget and interval before saving', async () => {
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-testid="daily-budget"]').setValue(0)
    await wrapper.find('form').trigger('submit')
    await wrapper.get('[data-testid="group-interval-7"]').setValue(59)
    await wrapper.get('[data-testid="save-group-7"]').trigger('click')
    await wrapper.get('[data-testid="group-interval-7"]').setValue(600)
    await wrapper.get('[data-testid="group-model-7"]').setValue('bad model')
    await wrapper.get('[data-testid="save-group-7"]').trigger('click')
    expect(updateSettings).not.toHaveBeenCalled()
    expect(updateGroup).not.toHaveBeenCalled()
    expect(showError).toHaveBeenCalledWith('admin.channelMonitorV3.invalidBudget')
    expect(showError).toHaveBeenCalledWith('admin.channelMonitorV3.invalidInterval')
    expect(showError).toHaveBeenCalledWith('admin.channelMonitorV3.invalidModel')
  })

  it('reports load and save errors', async () => {
    getConfig.mockRejectedValueOnce(new Error('load failed'))
    const failed = mountView()
    await flushPromises()
    expect(failed.get('[data-testid="load-error"]').exists()).toBe(true)

    const wrapper = mountView()
    await flushPromises()
    updateSettings.mockRejectedValueOnce(new Error('save failed'))
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(showError).toHaveBeenCalled()
  })
})
