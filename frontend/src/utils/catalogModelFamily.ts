export const CATALOG_MODEL_FAMILIES = [
  { value: 'openai', label: 'GPT / Codex', pattern: /^(?:gpt-|chatgpt-|codex(?:-|$)|o[134](?:-|$))/ },
  { value: 'claude', label: 'Claude', pattern: /^claude(?:-|$)/ },
  { value: 'gemini', label: 'Gemini / Gemma', pattern: /^(?:gemini|gemma)(?:-|$)/ },
  { value: 'kimi', label: 'Kimi', pattern: /^(?:kimi|moonshot)(?:-|$)/ },
  { value: 'deepseek', label: 'DeepSeek', pattern: /^deepseek(?:-|$)/ },
  { value: 'glm', label: 'GLM', pattern: /^(?:glm|chatglm)(?:-|\d|$)/ },
  { value: 'qwen', label: 'Qwen', pattern: /^qwen(?:-|\d|$)/ },
  { value: 'minimax', label: 'MiniMax', pattern: /^minimax(?:-|$)/ },
  { value: 'grok', label: 'Grok', pattern: /^grok(?:-|$)/ },
] as const

export type CatalogModelFamily = typeof CATALOG_MODEL_FAMILIES[number]['value'] | 'other'

// Presentation only: a model's family must never change its protocol platform.
export function catalogModelFamily(name: string): CatalogModelFamily {
  const canonical = (name.trim().split('/').at(-1) ?? '')
    .replace(/^(?:\[[^\]]*\]\s*)+/, '').toLowerCase()
  return CATALOG_MODEL_FAMILIES.find((family) => family.pattern.test(canonical))?.value ?? 'other'
}
