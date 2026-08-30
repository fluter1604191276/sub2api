import type {
  UserAvailableChannel,
  UserAvailableGroup,
  UserChannelPlatformSection
} from '@/api/channels'

export type AvailableChannelAccess = 'all' | 'public' | 'exclusive'

export interface AvailableChannelFilters {
  query?: string
  platform?: string
  access?: AvailableChannelAccess
}

export interface AvailableChannelSummary {
  channels: number
  platforms: number
  groups: number
  models: number
}

function groupMatchesAccess(group: UserAvailableGroup, access: AvailableChannelAccess): boolean {
  return access === 'all' || (access === 'exclusive' ? group.is_exclusive : !group.is_exclusive)
}

function sectionMatchesSearch(section: UserChannelPlatformSection, query: string): boolean {
  if (!query) return true
  return (
    section.platform.toLowerCase().includes(query) ||
    section.groups.some((group) => group.name.toLowerCase().includes(query)) ||
    section.supported_models.some((model) => model.name.toLowerCase().includes(query))
  )
}

/**
 * Keep the channel -> platform -> group/model shape while applying catalog filters.
 * A channel/description match keeps the complete matching platform context visible.
 */
export function filterAvailableChannels(
  channels: UserAvailableChannel[],
  filters: AvailableChannelFilters = {}
): UserAvailableChannel[] {
  const query = (filters.query || '').trim().toLowerCase()
  const platform = filters.platform || 'all'
  const access = filters.access || 'all'

  return channels
    .map((channel) => {
      const channelTextHit =
        !query ||
        channel.name.toLowerCase().includes(query) ||
        (channel.description || '').toLowerCase().includes(query)
      const matchingSections = channel.platforms
        .filter((section) => platform === 'all' || section.platform === platform)
        .map((section) => ({
          section,
          groups: section.groups.filter((group) => groupMatchesAccess(group, access)),
          searchHit: channelTextHit || sectionMatchesSearch(section, query)
        }))
        .filter(
          ({ groups, searchHit }) => searchHit && (access === 'all' || groups.length > 0)
        )
        .map(({ section, groups }) => ({ ...section, groups }))

      return matchingSections.length > 0 ? { ...channel, platforms: matchingSections } : null
    })
    .filter((channel): channel is UserAvailableChannel => channel !== null)
}

export function summarizeAvailableChannels(
  channels: UserAvailableChannel[]
): AvailableChannelSummary {
  const sections = channels.flatMap((channel) => channel.platforms)
  const groups = sections.flatMap((section) => section.groups)
  const models = sections.flatMap((section) => section.supported_models)

  return {
    channels: channels.length,
    platforms: new Set(sections.map((section) => section.platform)).size,
    groups: new Set(groups.map((group) => group.id)).size,
    models: new Set(models.map((model) => `${model.platform}:${model.name}`)).size
  }
}
