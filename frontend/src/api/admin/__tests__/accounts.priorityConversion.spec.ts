import { describe, expect, it, vi } from 'vitest'
const { post } = vi.hoisted(() => ({ post: vi.fn().mockResolvedValue({ data: { rows: [], updated: 0 } }) }))
vi.mock('@/api/client', () => ({ apiClient: { post } }))
import { previewPriorityConversion, applyPriorityConversion } from '../accounts'

describe('account priority conversion API', () => {
  it('keeps preview separate and confirms each account snapshot without a shared priority', async () => {
    await previewPriorityConversion([1, 2])
    expect(post).toHaveBeenLastCalledWith('/admin/accounts/priority-conversion/preview', { account_ids: [1, 2] })
    await applyPriorityConversion([
      { account_id: 1, name: 'a', rate_multiplier: '2.0070', old_priority: 7, new_priority: 2007, applied: false },
      { account_id: 2, name: 'b', rate_multiplier: '0.0000', old_priority: 50, new_priority: 1, applied: false },
      { account_id: 3, name: 'bad', rate_multiplier: null, old_priority: 50, new_priority: null, reason: 'missing_multiplier', applied: false }
    ])
    expect(post).toHaveBeenLastCalledWith('/admin/accounts/priority-conversion/apply', { accounts: [
      { account_id: 1, rate_multiplier: '2.0070', old_priority: 7 },
      { account_id: 2, rate_multiplier: '0.0000', old_priority: 50 }
    ] })
  })
})
