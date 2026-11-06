import { beforeEach, describe, expect, it, vi } from 'vitest'

const { get, put } = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: { get, put } }))

import { getOpenAIIQ, getOpenAIIQDiagnostics, getOpenAIIQSettings, updateOpenAIIQSettings } from '@/api/openaiIQ'

describe('OpenAI IQ API', () => {
  beforeEach(() => {
    get.mockReset()
    put.mockReset()
  })

  it('uses separate user result and admin settings endpoints', async () => {
    const results = { enabled: true, results: [], summary: {} }
    const settings = {
      settings: {
        enabled: false,
        question: 'Question',
        expected_answer: '15',
        group_ids: [],
        model: 'gpt-6-astra',
        interval_seconds: 3600,
        daily_budget_usd: 1,
        per_request_limit_usd: 0.05,
        max_output_tokens: 1024,
      },
      groups: [],
      test_user_id: null,
      test_user_balance: 0,
      budget: { date: '2026-11-04', spent_usd: 0.018, reserved_usd: 0, unconfirmed_usd: 0, unconfirmed_since: null, remaining_usd: 0.982 },
    }
    const diagnostics = { runs: [], total: 0, page: 1, page_size: 20 }
    get.mockResolvedValueOnce({ data: results }).mockResolvedValueOnce({ data: settings }).mockResolvedValueOnce({ data: diagnostics })
    put.mockResolvedValueOnce({ data: settings })

    await expect(getOpenAIIQ()).resolves.toEqual(results)
    await expect(getOpenAIIQSettings()).resolves.toEqual(settings)
    await expect(updateOpenAIIQSettings(settings.settings)).resolves.toEqual(settings)
    await expect(getOpenAIIQDiagnostics()).resolves.toEqual(diagnostics)
    expect(get).toHaveBeenNthCalledWith(1, '/openai-iq')
    expect(get).toHaveBeenNthCalledWith(2, '/admin/openai-iq/settings')
    expect(put).toHaveBeenCalledWith('/admin/openai-iq/settings', settings.settings)
    expect(get).toHaveBeenNthCalledWith(3, '/admin/openai-iq/diagnostics', { params: { page: 1, page_size: 20 } })
  })
})
