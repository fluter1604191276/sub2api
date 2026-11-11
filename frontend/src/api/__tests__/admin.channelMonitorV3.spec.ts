import { afterEach, describe, expect, it, vi } from 'vitest'
import { apiClient } from '../client'
import { getConfig, updateGroup, updateSettings } from '../admin/channelMonitorV3'

afterEach(() => vi.restoreAllMocks())

describe('channel monitor V3 admin API', () => {
  it('loads config and saves its narrow settings payloads', async () => {
    const get = vi.spyOn(apiClient, 'get').mockResolvedValue({ data: { settings: { enabled: false, daily_budget_usd: 2 }, groups: [] } })
    const put = vi.spyOn(apiClient, 'put').mockResolvedValue({ data: { enabled: true, daily_budget_usd: 3 } })

    await getConfig()
    await updateSettings({ enabled: true, daily_budget_usd: 3 })
    await updateGroup(7, { visible: false, probe_interval_seconds: 0, test_model: '' })

    expect(get).toHaveBeenCalledWith('/admin/channel-monitor-v3')
    expect(put).toHaveBeenNthCalledWith(1, '/admin/channel-monitor-v3/settings', { enabled: true, daily_budget_usd: 3 })
    expect(put).toHaveBeenNthCalledWith(2, '/admin/channel-monitor-v3/groups/7', { visible: false, probe_interval_seconds: 0, test_model: '' })
  })
})
