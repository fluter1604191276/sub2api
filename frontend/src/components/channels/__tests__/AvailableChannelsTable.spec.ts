import { createPinia } from 'pinia'
import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import AvailableChannelsTable from '../AvailableChannelsTable.vue'
import type { UserAvailableChannel } from '@/api/channels'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return { ...actual, useI18n: () => ({ t: (key: string) => key }) }
})

const rows: UserAvailableChannel[] = [{ name: 'Primary channel', description: 'Fast and reliable access', platforms: [{
  platform: 'anthropic',
  groups: [
    { id: 1, name: 'Exclusive Pro', platform: 'anthropic', subscription_type: 'standard', rate_multiplier: 1.2, peak_rate_enabled: true, peak_start: '08:00', peak_end: '10:00', peak_rate_multiplier: 1.5, is_exclusive: true },
    { id: 2, name: 'Public', platform: 'anthropic', subscription_type: 'standard', rate_multiplier: 1, peak_rate_enabled: false, peak_start: '', peak_end: '', peak_rate_multiplier: 1, is_exclusive: false },
  ],
  supported_models: [{ name: 'claude-test', platform: 'anthropic', pricing: null }],
}] }]

const baseProps = {
  columns: { name: 'Channel', description: 'Description', platform: 'Platform', groups: 'Groups and rates', supportedModels: 'Models and pricing' },
  rows, loading: false, pricingKeyPrefix: 'availableChannels.pricing', noPricingLabel: 'No pricing', noModelsLabel: 'No models', emptyLabel: 'No channels', userGroupRates: { 1: 0.8 },
}

function mountTable(props = {}) {
  return mount(AvailableChannelsTable, { props: { ...baseProps, ...props }, global: { plugins: [createPinia()], stubs: {
    Icon: { props: ['name'], template: '<i :data-icon="name" />' }, PlatformIcon: { template: '<i data-platform-icon />' },
    GroupBadge: { props: ['name', 'platform', 'rateMultiplier', 'userRateMultiplier'], template: '<span data-group-badge>{{ name }}:{{ platform }}:{{ rateMultiplier }}:{{ userRateMultiplier }}</span>' },
    SupportedModelChip: { props: ['model', 'noPricingLabel'], template: '<span data-model-chip>{{ model.name }}:{{ noPricingLabel }}</span>' },
  } } })
}

describe('AvailableChannelsTable', () => {
  it('renders protocol-platform category sections with group rates and model pricing', () => {
    const wrapper = mountTable()
    expect(wrapper.find('.available-category').exists()).toBe(true)
    expect(wrapper.text()).toContain('Primary channel')
    expect(wrapper.text()).toContain('Fast and reliable access')
    expect(wrapper.text()).toContain('availableChannels.exclusive')
    expect(wrapper.text()).toContain('availableChannels.public')
    expect(wrapper.find('[data-group-badge]').text()).toBe('Exclusive Pro:anthropic:1.2:0.8')
    expect(wrapper.findAll('[data-group-badge]')).toHaveLength(2)
    expect(wrapper.find('[data-icon="clock"]').exists()).toBe(true)
    expect(wrapper.text()).toContain('×1.5')
    expect(wrapper.find('[data-model-chip]').text()).toBe('claude-test:No pricing')
  })

  it('uses the group request protocol rather than the model supplier name', () => {
    const wrapper = mountTable({ rows: [{ name: 'Compatible channel', description: '', platforms: [{ platform: 'openai', groups: [{ id: 7, name: 'DeepSeek OpenAI format', platform: 'openai', subscription_type: 'standard', rate_multiplier: 1, peak_rate_enabled: false, peak_start: '', peak_end: '', peak_rate_multiplier: 1, is_exclusive: false }], supported_models: [{ name: 'deepseek-v4-pro', platform: 'deepseek', pricing: null }] }] }] })
    expect(wrapper.find('[data-group-badge]').text()).toBe('DeepSeek OpenAI format:openai:1:')
    expect(wrapper.find('h2').text()).toBe('OpenAI')
    expect(wrapper.findAll('.available-category')).toHaveLength(1)
  })

  it('preserves empty protocol sections and the layout scroll hook', () => {
    const wrapper = mountTable({ rows: [{ name: 'Empty channel', description: '', platforms: [{ platform: 'antigravity', groups: [], supported_models: [] }] }] })
    expect(wrapper.classes()).toContain('table-wrapper')
    expect(wrapper.find('.available-channel-row').text()).toContain('Empty channel')
    expect(wrapper.find('.available-channel-row').text()).toContain('No models')
    expect(wrapper.find('h2').text()).toBe('Antigravity')
  })

  it('keeps a readable empty state when no channel rows are available', () => {
    const wrapper = mountTable({ rows: [] })
    expect(wrapper.find('.catalogue-empty').text()).toContain('No channels')
  })

  it('shows loading state without rendering stale rows', () => {
    const wrapper = mountTable({ loading: true, rows: [] })
    expect(wrapper.find('[data-icon="refresh"]').exists()).toBe(true)
    expect(wrapper.find('.available-category').exists()).toBe(false)
  })
})
