import { apiClient } from '../client'

export interface ChannelMonitorV3Settings {
  enabled: boolean
  daily_budget_usd: number
}

export interface ChannelMonitorV3Group {
  group_id: number
  name: string
  platform: string
  status: string
  visible: boolean
  probe_interval_seconds: number
  test_model: string
}

export interface ChannelMonitorV3Config {
  settings: ChannelMonitorV3Settings
  groups: ChannelMonitorV3Group[]
  budget?: ChannelMonitorV3Budget
}

export interface ChannelMonitorV3Budget {
  date: string
  spent_usd: number
  reserved_usd: number
  remaining_usd: number
}

export interface UpdateChannelMonitorV3Settings {
  enabled: boolean
  daily_budget_usd: number
}

export interface UpdateChannelMonitorV3Group {
  visible: boolean
  probe_interval_seconds: number
  test_model: string
}

export async function getConfig(): Promise<ChannelMonitorV3Config> {
  const { data } = await apiClient.get<ChannelMonitorV3Config>('/admin/channel-monitor-v3')
  return data
}

export async function updateSettings(settings: UpdateChannelMonitorV3Settings): Promise<ChannelMonitorV3Settings> {
  const { data } = await apiClient.put<ChannelMonitorV3Settings>('/admin/channel-monitor-v3/settings', settings)
  return data
}

export async function updateGroup(groupID: number, config: UpdateChannelMonitorV3Group): Promise<ChannelMonitorV3Group> {
  const { data } = await apiClient.put<ChannelMonitorV3Group>(`/admin/channel-monitor-v3/groups/${groupID}`, config)
  return data
}

export const channelMonitorV3API = {
  getConfig,
  updateSettings,
  updateGroup,
}

export default channelMonitorV3API
