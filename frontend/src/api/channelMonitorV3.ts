import { apiClient } from './client'

export interface ChannelMonitorV3Grade {
  grade: string
  sample_count: number
}

export interface PublicChannelMonitorV3Group {
  group_id: number
  name: string
  last_10: ChannelMonitorV3Grade
  last_100: ChannelMonitorV3Grade
}

export interface PublicChannelMonitorV3Response {
  groups: PublicChannelMonitorV3Group[]
}

export async function getPublicChannelMonitorV3(): Promise<PublicChannelMonitorV3Response> {
  const { data } = await apiClient.get<PublicChannelMonitorV3Response>('/channel-monitor-v3')
  return data
}
