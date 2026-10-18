import { beforeEach, describe, expect, it, vi } from 'vitest'

const { get } = vi.hoisted(() => ({ get: vi.fn() }))
vi.mock('../client', () => ({ apiClient: { get }, buildGatewayUrl: vi.fn() }))

import { listResponseDiagnostics } from '@/api/admin/ops'

describe('admin response diagnostics API', () => {
  beforeEach(() => get.mockReset())

  it('passes supported terminal filters unchanged', async () => {
    const response = { items: [], total: 0, page: 2, page_size: 20 }
    get.mockResolvedValue({ data: response })
    const params = { page: 2, page_size: 20, status: 'incomplete' as const, reason: 'max_output_tokens', account_id: 7900, user_id: 331, request_id: 'req_1' }

    await expect(listResponseDiagnostics(params)).resolves.toEqual(response)
    expect(get).toHaveBeenCalledWith('/admin/ops/response-diagnostics', { params })
  })
})
