import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import ModelIQView from '../ModelIQView.vue'

const { getOpenAIIQ, showError } = vi.hoisted(() => ({
  getOpenAIIQ: vi.fn(),
  showError: vi.fn(),
}))
vi.mock('@/api/openaiIQ', () => ({
  getOpenAIIQ,
  getOpenAIIQSettings: vi.fn(),
  updateOpenAIIQSettings: vi.fn(),
}))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError }) }))
vi.mock('vue-i18n', async (importOriginal) => ({
  ...(await importOriginal<typeof import('vue-i18n')>()),
  useI18n: () => ({ t: (key: string) => key }),
}))

const response = {
  enabled: true,
  model: 'gpt-6-astra',
  question: 'What is 2 + 2?',
  expected_answer: '4',
  interval_seconds: 3600,
  round_started_at: '2026-11-04T01:00:00Z',
  summary: {
    total: 2,
    correct: 1,
    incorrect: 1,
    errors: 0,
    pending: 0,
  },
  results: [
    {
      group_id: 1,
      group_name: 'Primary',
      rate_multiplier: 1,
      model: 'gpt-6-astra',
      response_model: 'gpt-6-astra-2026',
      expected_answer: '4',
      model_answer: '4',
      output: 'The answer is 4.',
      status: 'correct',
      checked_at: '2026-11-04T01:00:01Z',
    },
    {
      group_id: 2,
      group_name: 'Fallback',
      rate_multiplier: 1.2,
      model: 'gpt-6-astra',
      expected_answer: '4',
      model_answer: '5',
      output: '',
      status: 'incorrect',
      checked_at: null,
    },
  ],
}

function mountView() {
  return mount(ModelIQView, {
    global: {
      stubs: {
        AppLayout: { template: '<div><slot /></div>' },
      },
    },
  })
}

describe('ModelIQView', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    getOpenAIIQ.mockResolvedValue(structuredClone(response))
  })
  it('shows server-filtered results, summary, response alias, and question disclosure', async () => {
    const wrapper = mountView()
    await flushPromises()
    expect(wrapper.text()).toContain('Primary')
    expect(wrapper.text()).toContain('modelIQ.summary.correct')
    expect(wrapper.text()).toContain('modelIQ.responseModel')
    expect(wrapper.text()).not.toContain('What is 2 + 2?')
    await wrapper.get('[data-testid="show-question"]').trigger('click')
    expect(wrapper.text()).toContain('What is 2 + 2?')
  })

  it('keeps raw output as text and renders every supported result status', async () => {
    getOpenAIIQ.mockResolvedValueOnce({
      ...structuredClone(response),
      results: ['correct', 'incorrect', 'error', 'skipped'].map((status, index) => ({
        ...response.results[0],
        group_id: index + 1,
        group_name: status,
        status,
        output: '<img src=x onerror=alert(1)>',
      })),
    })
    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.find('img').exists()).toBe(false)
    expect(wrapper.text()).toContain('<img src=x onerror=alert(1)>')
    expect(wrapper.findAll('details')).toHaveLength(4)
  })

  it('shows loading, empty, and retryable failure states', async () => {
    let resolveRequest: ((value: typeof response) => void) | undefined
    getOpenAIIQ.mockImplementationOnce(() => new Promise((resolve) => {
      resolveRequest = resolve
    }))
    const loadingWrapper = mountView()
    expect(loadingWrapper.text()).toContain('common.loading')
    resolveRequest?.({ ...structuredClone(response), results: [] })
    await flushPromises()
    expect(loadingWrapper.text()).toContain('modelIQ.noResults')

    getOpenAIIQ.mockRejectedValueOnce(new Error('offline'))
    const failureWrapper = mountView()
    await flushPromises()
    expect(failureWrapper.text()).toContain('offline')
    expect(showError).toHaveBeenCalledWith('offline')
  })
})
