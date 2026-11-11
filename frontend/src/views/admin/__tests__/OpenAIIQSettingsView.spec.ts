import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import OpenAIIQSettingsView from '../OpenAIIQSettingsView.vue'

const { getOpenAIIQSettings, getOpenAIIQDiagnostics, updateOpenAIIQSettings, recheckOpenAIIQGroup, showError, showSuccess } = vi.hoisted(() => ({
  getOpenAIIQSettings: vi.fn(),
  getOpenAIIQDiagnostics: vi.fn(),
  updateOpenAIIQSettings: vi.fn(),
  recheckOpenAIIQGroup: vi.fn(),
  showError: vi.fn(),
  showSuccess: vi.fn(),
}))
vi.mock('@/api/openaiIQ', () => ({
  getOpenAIIQ: vi.fn(),
  getOpenAIIQSettings,
  getOpenAIIQDiagnostics,
  recheckOpenAIIQGroup,
  updateOpenAIIQSettings,
}))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError, showSuccess }) }))
vi.mock('vue-i18n', async (importOriginal) => ({
  ...(await importOriginal<typeof import('vue-i18n')>()),
  useI18n: () => ({ t: (key: string, values?: { amount?: number }) => values?.amount === undefined ? key : `${key} ${values.amount}` }),
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
    unconfirmed_usd: 0.05,
    unconfirmed_since: '2026-11-04T01:00:00Z',
    remaining_usd: 0.9,
  },
}

function mountView() {
  return mount(OpenAIIQSettingsView, {
    global: {
      stubs: {
        AppLayout: { template: '<div><slot /></div>' },
        RouterLink: { template: '<a><slot /></a>' },
        ConfirmDialog: {
          props: ['show', 'message'],
          template: '<div v-if="show"><p>{{ message }}</p><button data-testid="confirm-recheck" @click="$emit(\'confirm\')">confirm</button><button data-testid="cancel-recheck" @click="$emit(\'cancel\')">cancel</button></div>',
        },
      },
    },
  })
}

describe('OpenAIIQSettingsView', () => {
  afterEach(() => {
    vi.useRealTimers()
  })
  beforeEach(() => {
    vi.clearAllMocks()
    getOpenAIIQSettings.mockResolvedValue(structuredClone(view))
    updateOpenAIIQSettings.mockResolvedValue(structuredClone(view))
    recheckOpenAIIQGroup.mockResolvedValue({ run_id: 77 })
    getOpenAIIQDiagnostics.mockResolvedValue({ runs: [], total: 0, page: 1, page_size: 20 })
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
    expect(wrapper.text()).toContain('0.05')
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

  it('loads full diagnostic history independently from editable settings and paginates it', async () => {
    getOpenAIIQDiagnostics.mockResolvedValueOnce({
      runs: [{ id: 7, trigger: 'manual', model: 'gpt-6-astra', question: 'Q', expected_answer: '15', status: 'complete', started_at: '2026-11-04T01:00:00Z', finished_at: null, results: [{ id: 8, group_id: 2, group_name: 'A', rate_multiplier: 1, model: 'gpt-6-astra', response_model: 'gpt-6-astra-2026', expected_answer: '15', model_answer: '16', output: 'full upstream output', status: 'error', checked_at: null, error: 'upstream interrupted', reservation: { reserved_usd: 0.1, actual_cost_usd: null, request_id: 'client:iq-long-request-id', usage_log_id: null, account_id: 29, settled_at: null } }] }],
      total: 21, page: 1, page_size: 20,
    })
    const wrapper = mountView()
    await flushPromises()

    expect(getOpenAIIQDiagnostics).toHaveBeenCalledWith(1, 20)
    expect(wrapper.text()).toContain('upstream interrupted')
    expect(wrapper.text()).toContain('full upstream output')
    expect(wrapper.text()).toContain('client:iq-long-request-id')
    expect(wrapper.text()).toContain('admin.openaiIQ.diagnosticsResponseModel')
    expect(wrapper.text()).toContain('admin.openaiIQ.triggerManual')
    await wrapper.findAll('button').find(button => button.text() === 'admin.openaiIQ.nextPage')?.trigger('click')
    await flushPromises()
    expect(getOpenAIIQDiagnostics).toHaveBeenCalledWith(2, 20)
  })

  it('keeps save enabled when diagnostics fail after settings load', async () => {
    getOpenAIIQDiagnostics.mockRejectedValueOnce(new Error('diagnostics unavailable'))
    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.text()).toContain('admin.openaiIQ.diagnosticsLoadError')
    expect(wrapper.get('[data-testid="save-openai-iq"]').attributes('disabled')).toBeUndefined()
  })

  it('confirms a saved reservation threshold before starting a manual group recheck', async () => {
    const wrapper = mountView()
    await flushPromises()

    await wrapper.get('[data-testid="openai-iq-recheck-2"]').trigger('click')
    expect(wrapper.text()).toContain('admin.openaiIQ.recheckConfirm')
    expect(wrapper.text()).toContain('0.1')

    await wrapper.get('[data-testid="confirm-recheck"]').trigger('click')
    await flushPromises()
    expect(recheckOpenAIIQGroup).toHaveBeenCalledWith(2, 0.1)
    expect(showSuccess).toHaveBeenCalledWith('admin.openaiIQ.recheckStarted')
    expect(wrapper.get('[data-testid="openai-iq-recheck-2"]').attributes('disabled')).toBeDefined()
    wrapper.unmount()
  })

  it('uses the saved threshold and ignores a later unsaved edit', async () => {
    const wrapper = mountView()
    await flushPromises()
    const requestLimit = wrapper.get('[data-testid="openai-iq-request-limit"]')
    await requestLimit.setValue(0.9)
    await wrapper.get('[data-testid="openai-iq-recheck-2"]').trigger('click')
    await wrapper.get('[data-testid="confirm-recheck"]').trigger('click')
    await flushPromises()

    expect(recheckOpenAIIQGroup).toHaveBeenCalledWith(2, 0.1)
    wrapper.unmount()
  })

  it('shows a disabled recheck action with guidance for groups outside the saved configuration', async () => {
    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.get('[data-testid="openai-iq-recheck-1"]').attributes('disabled')).toBeDefined()
    expect(wrapper.get('[data-testid="openai-iq-recheck-requires-save-1"]').text()).toContain('admin.openaiIQ.recheckRequiresSavedGroup')
    expect(wrapper.get('[data-testid="openai-iq-recheck-2"]').attributes('disabled')).toBeUndefined()
    wrapper.unmount()
  })

  it('does not let an expired running history entry block a new recheck', async () => {
    getOpenAIIQDiagnostics.mockResolvedValueOnce({
      runs: [{ id: 31, trigger: 'scheduled', lease_active: false, model: 'gpt-6-astra', question: 'Q', expected_answer: '21', status: 'running', started_at: '2026-11-11T00:00:00Z', finished_at: null, results: [] }],
      total: 1,
      page: 1,
      page_size: 20,
    })
    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.get('[data-testid="openai-iq-recheck-2"]').attributes('disabled')).toBeUndefined()
    wrapper.unmount()
  })

  it('returns to the first diagnostic page after a recheck started from a later history page', async () => {
    const priorRun = { id: 12, trigger: 'scheduled', model: 'gpt-6-astra', question: 'Q', expected_answer: '21', status: 'complete', started_at: '2026-11-11T00:00:00Z', finished_at: '2026-11-11T00:00:01Z', results: [] }
    getOpenAIIQDiagnostics
      .mockResolvedValueOnce({ runs: [priorRun], total: 40, page: 1, page_size: 20 })
      .mockResolvedValueOnce({ runs: [priorRun], total: 40, page: 2, page_size: 20 })
      .mockResolvedValueOnce({ runs: [priorRun], total: 40, page: 1, page_size: 20 })
    const wrapper = mountView()
    await flushPromises()

    await wrapper.findAll('button').find(button => button.text() === 'admin.openaiIQ.nextPage')?.trigger('click')
    await flushPromises()
    expect(getOpenAIIQDiagnostics).toHaveBeenLastCalledWith(2, 20)

    await wrapper.get('[data-testid="openai-iq-recheck-2"]').trigger('click')
    await wrapper.get('[data-testid="confirm-recheck"]').trigger('click')
    await flushPromises()

    expect(getOpenAIIQDiagnostics).toHaveBeenLastCalledWith(1, 20)
    wrapper.unmount()
  })

  it('shows an error rather than a success when the manual run ends without a usable result', async () => {
    const wrapper = mountView()
    await flushPromises()
    getOpenAIIQDiagnostics.mockResolvedValueOnce({
      runs: [{ id: 77, trigger: 'manual', model: 'gpt-6-astra', question: 'Q', expected_answer: '21', status: 'complete', started_at: '2026-11-11T00:00:00Z', finished_at: '2026-11-11T00:00:01Z', results: [{ id: 78, run_id: 77, group_id: 2, group_name: 'B', rate_multiplier: 1, model: 'gpt-6-astra', response_model: '', expected_answer: '21', model_answer: '', output: '', status: 'error', checked_at: null, error: 'upstream unavailable', reservation: null }] }],
      total: 1,
      page: 1,
      page_size: 20,
    })

    await wrapper.get('[data-testid="openai-iq-recheck-2"]').trigger('click')
    await wrapper.get('[data-testid="confirm-recheck"]').trigger('click')
    await flushPromises()

    expect(showError).toHaveBeenCalledWith('upstream unavailable')
    expect(showSuccess).not.toHaveBeenCalledWith('admin.openaiIQ.recheckComplete')
    wrapper.unmount()
  })

  it('stops polling and lets an administrator retry when its manual run lease expires', async () => {
    const wrapper = mountView()
    await flushPromises()
    getOpenAIIQDiagnostics.mockResolvedValueOnce({
      runs: [{ id: 77, trigger: 'manual', lease_active: false, model: 'gpt-6-astra', question: 'Q', expected_answer: '21', status: 'running', started_at: '2026-11-11T00:00:00Z', finished_at: null, results: [] }],
      total: 1,
      page: 1,
      page_size: 20,
    })

    await wrapper.get('[data-testid="openai-iq-recheck-2"]').trigger('click')
    await wrapper.get('[data-testid="confirm-recheck"]').trigger('click')
    await flushPromises()

    expect(showError).toHaveBeenCalledWith('admin.openaiIQ.recheckExpired')
    expect(wrapper.get('[data-testid="openai-iq-recheck-2"]').attributes('disabled')).toBeUndefined()
    wrapper.unmount()
  })

  it('does not select a group checkbox when opening its recheck confirmation', async () => {
    const selected = structuredClone(view)
    selected.settings.group_ids = [1]
    getOpenAIIQSettings.mockResolvedValueOnce(selected)
    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.get('[data-testid="openai-iq-recheck-2"]').attributes('disabled')).toBeDefined()
    expect(wrapper.text()).toContain('admin.openaiIQ.recheckRequiresSavedGroup')
    expect((wrapper.get('input[type="checkbox"][value="1"]').element as HTMLInputElement).checked).toBe(true)
    await wrapper.get('[data-testid="openai-iq-recheck-1"]').trigger('click')
    expect((wrapper.get('input[type="checkbox"][value="1"]').element as HTMLInputElement).checked).toBe(true)
    wrapper.unmount()
  })

  it('waits for a slow diagnostics request before scheduling the next poll', async () => {
    vi.useFakeTimers()
    let resolveSlowPage!: (value: { runs: unknown[]; total: number; page: number; page_size: number }) => void
    getOpenAIIQDiagnostics
      .mockResolvedValueOnce({ runs: [{ id: 31, trigger: 'manual', model: 'gpt-6-astra', question: 'Q', expected_answer: '21', status: 'running', started_at: '2026-11-11T00:00:00Z', finished_at: null, results: [] }], total: 1, page: 1, page_size: 20 })
      .mockImplementationOnce(() => new Promise(resolve => { resolveSlowPage = resolve }))
    const wrapper = mountView()
    await flushPromises()

    await vi.advanceTimersByTimeAsync(2000)
    expect(getOpenAIIQDiagnostics).toHaveBeenCalledTimes(2)
    await vi.advanceTimersByTimeAsync(10000)
    expect(getOpenAIIQDiagnostics).toHaveBeenCalledTimes(2)

    resolveSlowPage({ runs: [], total: 1, page: 1, page_size: 20 })
    await flushPromises()
    wrapper.unmount()
  })

  it('does not schedule a new poll when an in-flight diagnostics request resolves after unmount', async () => {
    vi.useFakeTimers()
    let resolveSlowPage!: (value: { runs: unknown[]; total: number; page: number; page_size: number }) => void
    getOpenAIIQDiagnostics
      .mockResolvedValueOnce({ runs: [{ id: 31, trigger: 'manual', model: 'gpt-6-astra', question: 'Q', expected_answer: '21', status: 'running', started_at: '2026-11-11T00:00:00Z', finished_at: null, results: [] }], total: 1, page: 1, page_size: 20 })
      .mockImplementationOnce(() => new Promise(resolve => { resolveSlowPage = resolve }))
    const wrapper = mountView()
    await flushPromises()
    await vi.advanceTimersByTimeAsync(2000)
    expect(getOpenAIIQDiagnostics).toHaveBeenCalledTimes(2)

    wrapper.unmount()
    resolveSlowPage({ runs: [], total: 1, page: 1, page_size: 20 })
    await flushPromises()
    await vi.advanceTimersByTimeAsync(10000)
    expect(getOpenAIIQDiagnostics).toHaveBeenCalledTimes(2)
  })
})
