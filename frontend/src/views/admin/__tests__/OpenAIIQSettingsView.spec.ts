import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import OpenAIIQSettingsView from '../OpenAIIQSettingsView.vue'

const { getOpenAIIQSettings, updateOpenAIIQSettings, showError, showSuccess } = vi.hoisted(() => ({
  getOpenAIIQSettings: vi.fn(),
  updateOpenAIIQSettings: vi.fn(),
  showError: vi.fn(),
  showSuccess: vi.fn(),
}))
vi.mock('@/api/openaiIQ', () => ({
  getOpenAIIQ: vi.fn(),
  getOpenAIIQSettings,
  updateOpenAIIQSettings,
}))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError, showSuccess }) }))
vi.mock('vue-i18n', async (importOriginal) => ({
  ...(await importOriginal<typeof import('vue-i18n')>()),
  useI18n: () => ({ t: (key: string) => key }),
}))

const view = {
  settings: {
    enabled: false,
    question: 'Q',
    expected_answer: '21',
    group_ids: [2],
    model: 'gpt-6-astra',
    interval_seconds: 3600,
    daily_budget_usd: 1,
    per_request_limit_usd: 0.1,
    max_output_tokens: 4096,
  },
  groups: [
    { id: 1, name: 'A', rate_multiplier: 1 },
    { id: 2, name: 'B', rate_multiplier: 1.5 },
  ],
  test_user_id: null,
  test_user_balance: 0,
  budget: {
    date: '2026-11-04',
    spent_usd: 0.018,
    reserved_usd: 0.082,
    remaining_usd: 0.9,
  },
}

function mountView() {
  return mount(OpenAIIQSettingsView, {
    global: {
      stubs: {
        AppLayout: { template: '<div><slot /></div>' },
        RouterLink: { template: '<a><slot /></a>' },
      },
    },
  })
}

describe('OpenAIIQSettingsView', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    getOpenAIIQSettings.mockResolvedValue(structuredClone(view))
    updateOpenAIIQSettings.mockResolvedValue(structuredClone(view))
  })
  it('saves selected groups and does not expose raw keys', async () => {
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-testid="openai-iq-enabled"]').setValue(true)
    await wrapper.get('#openai-iq-settings').trigger('submit')
    await flushPromises()
    expect(updateOpenAIIQSettings).toHaveBeenCalledWith(expect.objectContaining({
      enabled: true,
      group_ids: [2],
      model: 'gpt-6-astra',
    }))
    expect(wrapper.text().toLowerCase()).not.toContain('sk-')
    expect(showSuccess).toHaveBeenCalledWith('admin.openaiIQ.saveSuccess')
  })

  it('separates settled account spending from pending reservations', async () => {
    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.text()).toContain('0.018')
    expect(wrapper.text()).toContain('0.082')
    expect(wrapper.text()).toContain('0.9')
  })

  it('shows a retry state after the initial load fails', async () => {
    getOpenAIIQSettings.mockRejectedValueOnce(new Error('offline'))
    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.text()).toContain('offline')
    expect(wrapper.get('[data-testid="save-openai-iq"]').attributes('disabled')).toBeDefined()

    getOpenAIIQSettings.mockResolvedValueOnce(structuredClone(view))
    await wrapper.get('button.btn-secondary').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('admin.openaiIQ.controlTitle')
  })

  it('rejects incomplete enabled settings before the API call', async () => {
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-testid="openai-iq-enabled"]').setValue(true)
    await wrapper.get('[data-testid="openai-iq-question"]').setValue('')
    await wrapper.get('#openai-iq-settings').trigger('submit')

    expect(updateOpenAIIQSettings).not.toHaveBeenCalled()
    expect(showError).toHaveBeenCalledWith('admin.openaiIQ.invalidSettings')
  })

  it('lets an administrator remove a selected group that became unavailable', async () => {
    const unavailableView = structuredClone(view)
    unavailableView.settings.group_ids.push(99)
    getOpenAIIQSettings.mockResolvedValueOnce(unavailableView)
    const wrapper = mountView()
    await flushPromises()

    await wrapper.get('[data-testid="openai-iq-unavailable-99"]').setValue(false)
    await wrapper.get('#openai-iq-settings').trigger('submit')
    await flushPromises()

    expect(updateOpenAIIQSettings).toHaveBeenCalledWith(expect.objectContaining({ group_ids: [2] }))
  })
})
