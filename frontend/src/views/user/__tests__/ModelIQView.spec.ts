import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import ModelIQView from '../ModelIQView.vue'

const { getOpenAIIQ } = vi.hoisted(() => ({
  getOpenAIIQ: vi.fn(),
}))
vi.mock('@/api/openaiIQ', () => ({
  getOpenAIIQ,
  getOpenAIIQSettings: vi.fn(),
  updateOpenAIIQSettings: vi.fn(),
}))
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
  summary: {
    total: 2,
    correct: 1,
    incorrect: 1,
    no_results: 0,
  },
  results: [
    {
      group_id: 1,
      group_name: 'Primary',
      rate_multiplier: 1,
      model: 'gpt-6-astra',
      question: 'What is 2 + 2?',
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
      question: 'What is 3 + 3?',
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
  it('shows a server-filtered valid result and its own question disclosure', async () => {
    const wrapper = mountView()
    await flushPromises()
    expect(wrapper.text()).toContain('Primary')
    expect(wrapper.text()).toContain('modelIQ.summary.correct')
    expect(wrapper.text()).toContain('What is 2 + 2?')
  })

  it('keeps results separate for two models sharing a group and preserves each result timestamp and question', async () => {
    const twoModels = structuredClone(response)
    twoModels.results = [
      { ...twoModels.results[0], group_id: 1, model: 'gpt-6-astra', question: 'Old question', checked_at: '2026-11-04T01:00:01Z' },
      { ...twoModels.results[0], group_id: 1, model: 'gpt-6-beta', question: 'New question', checked_at: '2026-11-05T02:00:01Z' },
    ]
    getOpenAIIQ.mockResolvedValueOnce(twoModels)
    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.findAll('article')).toHaveLength(2)
    expect(wrapper.text()).toContain('Old question')
    expect(wrapper.text()).toContain('New question')
    expect(wrapper.text()).toContain('Nov 4, 2026')
    expect(wrapper.text()).toContain('Nov 5, 2026')
  })

  it('keeps raw output as text and renders public result states only', async () => {
    getOpenAIIQ.mockResolvedValueOnce({
      ...structuredClone(response),
      results: ['correct', 'incorrect', 'no_results'].map((status, index) => ({
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
    expect(wrapper.text()).toContain('modelIQ.noResultsForGroup')
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

    getOpenAIIQ.mockRejectedValueOnce(new Error('sentinel-internal-request-id-42'))
    const failureWrapper = mountView()
    await flushPromises()
    expect(failureWrapper.text()).toContain('modelIQ.loadError')
    expect(failureWrapper.text()).not.toContain('sentinel-internal-request-id-42')
  })

  it('hides the detection time for neutral cards even if a stale timestamp is supplied', async () => {
    getOpenAIIQ.mockResolvedValueOnce({
      ...structuredClone(response),
      results: [null, '2026-11-04T01:00:01Z'].map((checked_at, index) => ({
        ...response.results[0],
        group_id: index + 1,
        status: 'no_results',
        checked_at,
        output: '',
        model_answer: '',
      })),
    })
    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.findAll('article')).toHaveLength(2)
    expect(wrapper.text()).toContain('modelIQ.noResultsForGroup')
    expect(wrapper.findAll('article footer')).toHaveLength(0)
    expect(wrapper.text()).not.toContain('modelIQ.checkedAt')
    expect(wrapper.text()).not.toContain('common.notAvailable')
  })
})
