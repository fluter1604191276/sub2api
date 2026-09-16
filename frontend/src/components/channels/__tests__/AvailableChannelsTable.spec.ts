import { createPinia } from 'pinia'
import { mount } from '@vue/test-utils'
import { defineComponent } from 'vue'
import { describe, expect, it, vi } from 'vitest'
import AvailableChannelsTable from '../AvailableChannelsTable.vue'
import type { UserAvailableChannel } from '@/api/channels'
import { useAppStore } from '@/stores/app'
import type { PublicSettings } from '@/types'

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

const RouterLinkStub = defineComponent({
  name: 'RouterLink',
  props: { to: { type: Object, required: true } },
  template: '<a data-router-link><slot /></a>',
})

const SupportedModelChipStub = defineComponent({
  name: 'SupportedModelChip',
  props: ['model', 'noPricingLabel', 'pricingHeading', 'pricingContexts'],
  template: '<span data-model-chip>{{ model.name }}:{{ noPricingLabel }}:{{ pricingHeading }}</span>',
})

function mountTable(props = {}, modelPlazaEnabled = false) {
  const pinia = createPinia()
  const appStore = useAppStore(pinia)
  appStore.cachedPublicSettings = { model_plaza_enabled: modelPlazaEnabled } as PublicSettings

  return mount(AvailableChannelsTable, { props: { ...baseProps, ...props }, global: { plugins: [pinia], stubs: {
    Icon: { props: ['name'], template: '<i :data-icon="name" />' }, PlatformIcon: { template: '<i data-platform-icon />' },
    GroupBadge: { props: ['name', 'platform', 'rateMultiplier', 'userRateMultiplier'], template: '<span data-group-badge>{{ name }}:{{ platform }}:{{ rateMultiplier }}:{{ userRateMultiplier }}</span>' },
    SupportedModelChip: SupportedModelChipStub,
    RouterLink: RouterLinkStub,
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
    expect(wrapper.find('[data-model-chip]').text()).toBe('claude-test:No pricing:')
  })

  it('passes the GLM base-times-current-rate reference before the unchanged channel base price', () => {
    const pricing = {
      currency: 'CNY' as const,
      billing_mode: 'token' as const,
      input_price: 1.4e-6,
      output_price: 4.4e-6,
      cache_write_price: 0.26e-6,
      cache_read_price: null,
      image_input_price: null,
      image_output_price: null,
      per_request_price: null,
      intervals: [],
    }
    const wrapper = mountTable({
      rows: [{ name: 'GLM', description: '', platforms: [{
        platform: 'zhipu',
        groups: [{ id: 9, name: 'Domestic', platform: 'zhipu', subscription_type: 'standard', rate_multiplier: 0.7, peak_rate_enabled: false, peak_start: '', peak_end: '', peak_rate_multiplier: 1, is_exclusive: false }],
        supported_models: [{ name: 'glm-test', platform: 'zhipu', pricing }],
      }] }],
      userGroupRates: { 9: 0.6 },
    })
    const contexts = wrapper.findComponent(SupportedModelChipStub).props('pricingContexts')

    expect(contexts[0]).toMatchObject({
      key: 'group-9',
      heading: 'availableChannels.pricing.effectivePrice',
      multiplierLabel: 'availableChannels.pricing.multiplier',
      pricing: { currency: 'CNY' },
    })
    expect(contexts[0].pricing.input_price).toBeCloseTo(0.84e-6)
    expect(contexts[0].pricing.output_price).toBeCloseTo(2.64e-6)
    expect(contexts[0].pricing.cache_write_price).toBeCloseTo(0.156e-6)
    expect(contexts[1]).toMatchObject({ key: 'base', pricing })
  })

  it.each([
    ['image pricing without an independent group image rate', {
      currency: 'CNY' as const,
      billing_mode: 'image' as const,
      input_price: null,
      output_price: null,
      cache_write_price: null,
      cache_read_price: null,
      image_input_price: null,
      image_output_price: null,
      per_request_price: 0.2,
      intervals: [],
    }],
    ['tiered token pricing without group long-context policy', {
      currency: 'USD' as const,
      billing_mode: 'token' as const,
      input_price: 1e-6,
      output_price: 2e-6,
      cache_write_price: null,
      cache_read_price: null,
      image_input_price: null,
      image_output_price: null,
      per_request_price: null,
      intervals: [{
        min_tokens: 100_000,
        max_tokens: null,
        input_price: 2e-6,
        output_price: 4e-6,
        cache_write_price: null,
        cache_read_price: null,
        per_request_price: null,
      }],
    }],
  ])('shows base only for %s', (_label, pricing) => {
    const wrapper = mountTable({
      rows: [{ name: 'Guarded', description: '', platforms: [{
        platform: 'zhipu',
        groups: [{ id: 9, name: 'Domestic', platform: 'zhipu', subscription_type: 'standard', rate_multiplier: 0.6, peak_rate_enabled: false, peak_start: '', peak_end: '', peak_rate_multiplier: 1, is_exclusive: false }],
        supported_models: [{ name: 'guarded-model', platform: 'zhipu', pricing }],
      }] }],
      userGroupRates: { 9: 0.5 },
    }, true)
    const contexts = wrapper.findComponent(SupportedModelChipStub).props('pricingContexts')

    expect(contexts).toHaveLength(1)
    expect(contexts[0]).toMatchObject({ key: 'base', pricing })
    expect(wrapper.findAllComponents(RouterLinkStub)).toHaveLength(1)
  })

  it('links each accessible group to its own plaza pricing when enabled', () => {
    const wrapper = mountTable({}, true)
    const links = wrapper.findAllComponents(RouterLinkStub)

    expect(links).toHaveLength(2)
    expect(links.map((link) => link.props('to'))).toEqual([
      { path: '/model-plaza', query: { embedded: '1', group: '1' } },
      { path: '/model-plaza', query: { embedded: '1', group: '2' } },
    ])
    expect(links.every((link) => link.attributes('title') === 'availableChannels.viewGroupPricing')).toBe(true)
  })

  it('hides group pricing links when the model plaza is disabled', () => {
    const wrapper = mountTable({}, false)
    expect(wrapper.findAllComponents(RouterLinkStub)).toHaveLength(0)
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
