import type {
  UserAvailableChannel,
  UserAvailableGroup,
  UserChannelPlatformSection
} from '@/api/channels'
import { modelProvider } from './modelProvider'

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
 * Split protocol sections into supplier sections for presentation.
 *
 * The API intentionally keeps the original protocol platform because it is
 * needed for routing and billing. A single OpenAI-compatible channel can
 * nevertheless contain Kimi or DeepSeek models, so the user-facing catalogue
 * needs a separate supplier view.
 */
function catalogSections(section: UserChannelPlatformSection): UserChannelPlatformSection[] {
  const byProvider = new Map<string, UserAvailableChannel['platforms'][number]['supported_models']>()
  for (const model of section.supported_models) {
    const provider = modelProvider(model.name, model.platform || section.platform)
    const models = byProvider.get(provider) ?? []
    models.push({ ...model, platform: provider })
    byProvider.set(provider, models)
  }

  if (byProvider.size === 0) {
    return [section]
  }

  return [...byProvider.entries()]
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([provider, models]) => ({
      ...section,
      platform: provider,
      // Keep the protocol platform on the group for routing semantics. This
      // field is consumed only by the catalogue presentation components.
      groups: section.groups.map((group) => ({
        ...group,
        display_platform: provider
      })),
      supported_models: models
    }))
}

function channelCatalogSections(channel: UserAvailableChannel): UserChannelPlatformSection[] {
  return channel.platforms.flatMap(catalogSections)
}

export function availableCatalogPlatforms(channels: UserAvailableChannel[]): string[] {
  return [
    ...new Set(channels.flatMap(channelCatalogSections).map((section) => section.platform))
  ].sort()
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
      const matchingSections = channelCatalogSections(channel)
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
  const sections = channels.flatMap(channelCatalogSections)
  const groups = sections.flatMap((section) => section.groups)
  const models = sections.flatMap((section) => section.supported_models)

  return {
    channels: channels.length,
    platforms: new Set(sections.map((section) => section.platform)).size,
    groups: new Set(groups.map((group) => group.id)).size,
    models: new Set(models.map((model) => `${model.platform}:${model.name}`)).size
  }
}
