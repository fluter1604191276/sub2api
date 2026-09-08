import { beforeEach, describe, expect, it, vi } from 'vitest'

const { post } = vi.hoisted(() => ({ post: vi.fn() }))

vi.mock('@/api/client', () => ({
  apiClient: { post },
}))

import { bulkUpdateInterval } from '@/api/admin/channelMonitor'

describe('admin channel monitor bulk interval API', () => {
  beforeEach(() => {
    post.mockReset()
    post.mockResolvedValue({ data: { affected: 2 } })
  })

  it('sends selected monitor IDs and the requested interval', async () => {
    await expect(bulkUpdateInterval({ monitor_ids: [7, 3], interval_seconds: 15 })).resolves.toEqual({ affected: 2 })

    expect(post).toHaveBeenCalledWith('/admin/channel-monitors/batch-interval', {
      monitor_ids: [7, 3],
      interval_seconds: 15,
    })
  })
})
