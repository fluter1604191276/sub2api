import { afterEach, describe, expect, it, vi } from 'vitest'
import { apiClient } from '../client'
import { getPublicChannelMonitorV3 } from '../channelMonitorV3'

afterEach(() => vi.restoreAllMocks())

describe('public channel monitor V3 API', () => {
  it('requests the anonymous V3 status endpoint', async () => {
    const get = vi.spyOn(apiClient, 'get').mockResolvedValue({ data: { groups: [] } })
    await getPublicChannelMonitorV3()
    expect(get).toHaveBeenCalledWith('/channel-monitor-v3')
  })
})
