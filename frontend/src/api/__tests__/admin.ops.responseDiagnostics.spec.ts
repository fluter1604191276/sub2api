import { beforeEach, describe, expect, it, vi } from 'vitest'

const { get } = vi.hoisted(() => ({ get: vi.fn() }))
vi.mock('../client', () => ({ apiClient: { get }, buildGatewayUrl: vi.fn() }))

import { listResponseDiagnostics, listUpstreamAttemptDailyCosts } from '@/api/admin/ops'

describe('admin response diagnostics API', () => {
  beforeEach(() => get.mockReset())

  it('passes supported terminal filters unchanged', async () => {
    const response = { items: [], total: 0, page: 2, page_size: 20 }
    get.mockResolvedValue({ data: response })
    const params = { page: 2, page_size: 20, status: 'incomplete' as const, reason: 'max_output_tokens', account_id: 7900, user_id: 331, request_id: 'req_1' }

    await expect(listResponseDiagnostics(params)).resolves.toEqual(response)
    expect(get).toHaveBeenCalledWith('/admin/ops/response-diagnostics', { params })
  })

  it('gets daily shadow-ledger reconciliation by account', async () => {
    const response = [{ day: '2026-10-18T00:00:00Z', account_id: 9, attempt_count: 2, known_cost_usd: 0.125, unknown_count: 1 }]
    get.mockResolvedValue({ data: response })
    const params = { time_range: '24h', account_id: 9 }

    await expect(listUpstreamAttemptDailyCosts(params)).resolves.toEqual(response)
    expect(get).toHaveBeenCalledWith('/admin/ops/upstream-attempts/daily-costs', { params })
  })
})
