import type { UserChannelPlatformSection, UserSupportedModel } from '@/api/channels'

export const AVAILABLE_CHANNEL_CATEGORY_ORDER = [
  'claude',
  'codex',
  'gemini',
  'grok',
  'domestic',
  'image',
  'other',
] as const

export type AvailableChannelCategory = (typeof AVAILABLE_CHANNEL_CATEGORY_ORDER)[number]

const DOMESTIC_PLATFORMS = new Set([
  'deepseek', 'kimi', 'zhipu', 'glm', 'minimax', 'qwen', 'doubao', 'volcengine',
  'hunyuan', 'ernie', 'baichuan', 'moonshot', 'yi', 'stepfun', 'internlm'
])

/**
 * Maps a model to the catalogue section using the channel protocol platform.
 * Model names are intentionally ignored: compatible upstreams may expose
 * domestic models through OpenAI/Anthropic protocols.
 */
export function getAvailableChannelCategory(
  model: UserSupportedModel,
  platformHint = '',
): AvailableChannelCategory {
  const platform = (model.platform || platformHint).trim().toLowerCase()
  if (platform === 'anthropic') return 'claude'
  if (platform === 'gemini') return 'gemini'
  if (platform === 'grok') return 'grok'
  if (platform === 'openai') return 'codex'
  if (DOMESTIC_PLATFORMS.has(platform)) return 'domestic'
  if (platform === 'image' || platform === 'video') return 'image'
  return 'other'
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
 * A platform section can contain multiple model families, so it is split by
 * category while retaining the same accessible groups and pricing metadata.
 */
export function buildAvailableChannelDisplayRows(
  channels: Array<{ name: string; description: string; platforms: UserChannelPlatformSection[] }>,
): AvailableChannelDisplayRow[] {
  const rows: AvailableChannelDisplayRow[] = []

  for (const channel of channels) {
    for (const section of channel.platforms) {
      const modelsByCategory = new Map<AvailableChannelCategory, UserSupportedModel[]>()
      for (const model of section.supported_models) {
        const category = getAvailableChannelCategory(model, section.platform)
        const models = modelsByCategory.get(category) || []
        models.push(model)
        modelsByCategory.set(category, models)
      }

      for (const category of AVAILABLE_CHANNEL_CATEGORY_ORDER) {
        const models = modelsByCategory.get(category)
        if (!models?.length) continue
        rows.push({
          category,
          channelName: channel.name,
          description: channel.description,
          section: { ...section, supported_models: models },
          models,
        })
      }
    }
  }

  return rows.sort((a, b) => {
    const categoryDiff = AVAILABLE_CHANNEL_CATEGORY_ORDER.indexOf(a.category) - AVAILABLE_CHANNEL_CATEGORY_ORDER.indexOf(b.category)
    if (categoryDiff !== 0) return categoryDiff
    const platformDiff = a.section.platform.localeCompare(b.section.platform)
    if (platformDiff !== 0) return platformDiff
    return a.channelName.localeCompare(b.channelName)
  })
}
