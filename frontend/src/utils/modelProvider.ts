/**
 * Returns the model supplier used by the catalogue filter.
 *
 * Group/model `platform` remains the request protocol and must not be changed
 * for OpenAI-compatible Kimi, DeepSeek, or other suppliers. The catalogue
 * uses this separate display dimension so a protocol-compatible model is still
 * discoverable under its supplier.
 */
export function modelProvider(model: string, protocolPlatform = ''): string {
  let normalized = model.trim().toLowerCase()
  normalized = normalized.replace(/^models\//, '')

  const slash = normalized.indexOf('/')
  if (slash > 0) {
    const prefix = normalized.slice(0, slash)
    const provider = providerAlias(prefix)
    if (provider) return provider
    normalized = normalized.slice(slash + 1)
  }

  if (
    normalized === 'kimi' ||
    normalized === 'k2' ||
    normalized === 'k2.5' ||
    normalized === 'k2p5' ||
    normalized === 'k3' ||
    normalized.startsWith('k2-') ||
    normalized.startsWith('k2.5-') ||
    normalized.startsWith('k2p5-') ||
    normalized.startsWith('k3-') ||
    normalized.startsWith('kimi-') ||
    normalized.startsWith('moonshot-')
  ) return 'kimi'
  if (normalized === 'deepseek' || normalized.startsWith('deepseek-')) return 'deepseek'
  if (normalized === 'glm' || normalized.startsWith('glm-')) return 'zhipu'
  if (normalized === 'grok' || normalized.startsWith('grok-')) return 'grok'
  if (normalized.startsWith('claude-') || normalized.startsWith('anthropic.claude-')) return 'anthropic'
  if (normalized.startsWith('gemini-') || normalized.startsWith('learnlm-')) return 'gemini'
  if (
    normalized.startsWith('gpt-') ||
    normalized.startsWith('chatgpt-') ||
    normalized.startsWith('codex-') ||
    normalized.startsWith('text-embedding-') ||
    normalized.startsWith('text-moderation-') ||
    normalized.startsWith('omni-moderation-') ||
    normalized.startsWith('dall-e-') ||
    normalized.startsWith('gpt-image-') ||
    normalized.startsWith('tts-') ||
    normalized.startsWith('whisper-') ||
    /^o[1-5](?:-|$)/.test(normalized)
  ) return 'openai'
  if (normalized === 'minimax' || normalized.startsWith('minimax-')) return 'minimax'
  if (normalized === 'qwen' || normalized.startsWith('qwen')) return 'qwen'

  return protocolPlatform.trim().toLowerCase()
}

function providerAlias(value: string): string {
  switch (value) {
    case 'anthropic':
    case 'claude':
      return 'anthropic'
    case 'openai':
    case 'chatgpt':
      return 'openai'
    case 'google':
    case 'google-ai-studio':
    case 'gemini':
      return 'gemini'
    case 'xai':
    case 'x-ai':
    case 'grok':
      return 'grok'
    case 'kimi':
    case 'moonshot':
      return 'kimi'
    case 'zhipu':
    case 'glm':
    case 'bigmodel':
      return 'zhipu'
    case 'deepseek':
      return 'deepseek'
    case 'minimax':
      return 'minimax'
    case 'qwen':
      return 'qwen'
    default:
      return ''
  }
}
