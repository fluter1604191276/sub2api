import type { UserChannelPlatformSection, UserSupportedModel } from '@/api/channels'
import { GROUP_PLATFORM_OPTIONS, sortGroupPlatforms } from '@/constants/platforms'

export const AVAILABLE_CHANNEL_CATEGORY_ORDER: string[] = GROUP_PLATFORM_OPTIONS.map(option => option.value)

export type AvailableChannelCategory = string

/**
 * Maps a model to the catalogue section using the channel protocol platform.
 * Model names are intentionally ignored: compatible upstreams may expose
 * domestic models through OpenAI/Anthropic protocols.
 */
export function getAvailableChannelCategory(
  model: UserSupportedModel,
  platformHint = '',
): AvailableChannelCategory {
  return (platformHint || model.platform).trim().toLowerCase() || 'other'
}

export interface AvailableChannelDisplayRow {
  category: AvailableChannelCategory
  channelName: string
  description: string
  section: UserChannelPlatformSection
  models: UserSupportedModel[]
}

/**
 * Flattens the API's channel -> platform shape into stable catalogue rows.
 * Each protocol section stays intact, including sections without models.
 */
export function buildAvailableChannelDisplayRows(
  channels: Array<{ name: string; description: string; platforms: UserChannelPlatformSection[] }>,
): AvailableChannelDisplayRow[] {
  const rows: AvailableChannelDisplayRow[] = []

  for (const channel of channels) {
    for (const section of channel.platforms) {
      rows.push({
        category: section.platform,
        channelName: channel.name,
        description: channel.description,
        section,
        models: section.supported_models,
      })
    }
  }

  const platforms = sortGroupPlatforms(rows.map(row => row.category))
  return rows.sort((a, b) => {
    const categoryDiff = platforms.indexOf(a.category) - platforms.indexOf(b.category)
    if (categoryDiff !== 0) return categoryDiff
    const platformDiff = a.section.platform.localeCompare(b.section.platform)
    if (platformDiff !== 0) return platformDiff
    return a.channelName.localeCompare(b.channelName)
  })
}
