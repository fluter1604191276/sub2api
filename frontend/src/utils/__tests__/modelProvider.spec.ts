import { describe, expect, it } from 'vitest'
import { modelProvider } from '../modelProvider'

describe('modelProvider', () => {
  it.each([
    ['kimi-k3', 'openai', 'kimi'],
    ['k2p5', 'openai', 'kimi'],
    ['deepseek-v4-pro', 'openai', 'deepseek'],
    ['deepseek', 'openai', 'deepseek'],
    ['glm-5.3', 'openai', 'zhipu'],
    ['grok-4.5', 'openai', 'grok'],
    ['claude-opus-5', 'anthropic', 'anthropic'],
    ['gpt-5.6-sol', 'openai', 'openai'],
    ['MiniMax-M3', 'openai', 'minimax'],
    ['qwen3-max', 'openai', 'qwen']
  ])('detects %s as %s', (model, protocol, expected) => {
    expect(modelProvider(model, protocol)).toBe(expected)
  })

  it('honors a provider prefix', () => {
    expect(modelProvider('deepseek/deepseek-v4-pro', 'openai')).toBe('deepseek')
    expect(modelProvider('moonshot/k3', 'openai')).toBe('kimi')
    expect(modelProvider('models/kimi/k2.7', 'openai')).toBe('kimi')
  })

  it('falls back to the protocol for unknown models', () => {
    expect(modelProvider('custom-model', 'openai')).toBe('openai')
  })
})
