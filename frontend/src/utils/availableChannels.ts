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

const DOMESTIC_PREFIXES = [
  'deepseek',
  'kimi',
  'glm',
  'minimax',
  'qwen',
  'wen',
  'doubao',
  'seed',
  'hunyuan',
  'ernie',
  'yi-',
  'moonshot',
  'baichuan',
  'mimo',
  'step',
  'internlm',
  'ling',
]

const IMAGE_MARKERS = [
  'image',
  'video',
  'seedance',
  'veo',
  'sora',
]

function startsWithAny(value: string, prefixes: string[]): boolean {
  return prefixes.some((prefix) => value === prefix || value.startsWith(`${prefix}-`))
}

function containsAny(value: string, markers: string[]): boolean {
  return markers.some((marker) => value.includes(marker))
}

/**
 * Maps a model to the public catalogue section used by Available Channels.
 * Model identity wins over protocol platform because some domestic upstreams
 * expose OpenAI- or Anthropic-compatible endpoints.
 */
export function getAvailableChannelCategory(
  model: UserSupportedModel,
  platformHint = '',
): AvailableChannelCategory {
  const name = model.name.trim().toLowerCase()
  const platform = (model.platform || platformHint).trim().toLowerCase()

  if (containsAny(name, IMAGE_MARKERS)) return 'image'
  if (name.startsWith('claude-')) return 'claude'
  if (name.startsWith('gpt-') || name.startsWith('codex') || (platform === 'openai' && name.startsWith('o'))) {
    return 'codex'
  }
  if (name.startsWith('gemini-')) return 'gemini'
  if (name.startsWith('grok-')) return 'grok'
  if (startsWithAny(name, DOMESTIC_PREFIXES)) return 'domestic'
  if (platform === 'anthropic') return 'claude'
  if (platform === 'gemini') return 'gemini'
  if (platform === 'grok') return 'grok'
  if (platform === 'openai') return 'codex'
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
