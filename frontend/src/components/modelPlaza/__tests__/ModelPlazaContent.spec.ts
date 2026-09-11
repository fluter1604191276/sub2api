import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import ModelPlazaContent from '../ModelPlazaContent.vue'
import type { ModelPlazaGroup, ModelPlazaResponse, PlazaModel } from '@/api/modelPlaza'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key }),
  }
})

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({ isAuthenticated: true }),
}))

function model(name: string, platform: string): PlazaModel {
  return {
    name,
    platform,
    pricing: null,
    official_pricing: null,
  }
}

function group(overrides: Partial<ModelPlazaGroup>): ModelPlazaGroup {
  return {
    id: 1,
    name: 'Group',
    description: '',
    platform: 'openai',
    subscription_type: 'standard',
    rate_multiplier: 1,
    peak_rate_enabled: false,
    peak_start: '',
    peak_end: '',
    peak_rate_multiplier: 1,
    is_exclusive: false,
    image_rate_independent: false,
    image_rate_multiplier: 1,
    long_context_pricing_enabled: false,
    models: [],
    ...overrides,
  }
}

const response: ModelPlazaResponse = {
  description: '',
  groups: [
    group({
      id: 1,
      name: 'DeepSeek OpenAI format',
      platform: 'openai',
      models: [model('deepseek-v4-pro', 'openai')],
    }),
    group({
      id: 2,
      name: 'DeepSeek adaptive format',
      platform: 'deepseek',
      models: [model('deepseek-v4-pro', 'deepseek')],
    }),
  ],
}

function mountContent(overrides: Record<string, unknown> = {}) {
  return mount(ModelPlazaContent, {
    props: { response, loading: false, ...overrides },
    global: {
      stubs: {
        CatalogSurfaceNav: true,
        Icon: true,
        PlatformIcon: true,
        PlazaGroupSection: {
          props: ['group', 'priceView'],
          template:
            '<section data-testid="plaza-group" :data-platform="group.platform" :data-price-view="priceView">{{ group.name }}|{{ group.models.map((item) => item.name).join(",") }}</section>',
        },
      },
    },
  })
}

describe('ModelPlazaContent protocol platform filtering', () => {
  it('filters model families independently of request platform and does not mutate the response', async () => {
    const catalog = { ...response, groups: [group({ models: [model('[special]kimi-k3', 'openai'), model('deepseek-v4-pro', 'openai')] })] }
    const before = JSON.stringify(catalog)
    const wrapper = mountContent({ response: catalog })
    await wrapper.get('[data-testid="plaza-platform-openai"]').trigger('click')
    await wrapper.get('[data-testid="plaza-family"]').setValue('kimi')
    expect(wrapper.get('[data-testid="plaza-group"]').text()).toContain('kimi-k3')
    expect(wrapper.get('[data-testid="plaza-group"]').text()).not.toContain('deepseek-v4-pro')
    expect(wrapper.get('[data-testid="plaza-group"]').attributes('data-platform')).toBe('openai')
    expect(JSON.stringify(catalog)).toBe(before)
  })

  it('focuses a group link and stays empty for an inaccessible group', async () => {
    const wrapper = mountContent({ requestedGroupId: 2 })
    expect(wrapper.findAll('[data-testid="plaza-group"]')).toHaveLength(1)
    expect(wrapper.get('[data-testid="plaza-group"]').text()).toContain('DeepSeek adaptive')
    await wrapper.setProps({ requestedGroupId: 999 })
    expect(wrapper.findAll('[data-testid="plaza-group"]')).toHaveLength(0)
    await wrapper.setProps({ requestedGroupId: null })
    expect(wrapper.findAll('[data-testid="plaza-group"]')).toHaveLength(2)
  })

  it('uses standard pricing when user rates could not be resolved', () => {
    const wrapper = mountContent({ response: { ...response, catalog_metadata: { source: 'configured_channels', availability: 'configured_not_live', pricing_basis: 'before_group_multiplier', policy: 'presentation_only', generated_at: new Date().toISOString(), user_rate_resolution: 'unavailable_fallback_to_group' } } })
    expect(wrapper.get('[data-testid="plaza-price-user"]').attributes('disabled')).toBeDefined()
    expect(wrapper.get('[role="status"]').text()).toContain('modelPlaza.rateUnavailable')
    expect(wrapper.get('[data-testid="plaza-group"]').attributes('data-price-view')).toBe('standard')
  })

  it('keeps the selected price view consistent with multiplier filtering', async () => {
    const wrapper = mountContent({ response: { ...response, groups: [group({ rate_multiplier: 0.5, user_rate_multiplier: 0.2, models: [model('gpt-5.5', 'openai')] })], catalog_metadata: { source: 'configured_channels', availability: 'configured_not_live', pricing_basis: 'before_group_multiplier', policy: 'presentation_only', generated_at: new Date().toISOString(), user_rate_resolution: 'resolved' } } })
    expect(wrapper.get('[data-testid="plaza-group"]').attributes('data-price-view')).toBe('user')
    await wrapper.get('[data-testid="plaza-price-standard"]').trigger('click')
    expect(wrapper.get('[data-testid="plaza-group"]').attributes('data-price-view')).toBe('standard')
  })

  it('derives platform choices from groups instead of model supplier names', () => {
    const wrapper = mountContent()

    expect(wrapper.find('[data-testid="plaza-platform-openai"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="plaza-platform-deepseek"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="plaza-platform-kimi"]').exists()).toBe(false)
  })

  it('keeps all models in an OpenAI-compatible group when filtering OpenAI', async () => {
    const wrapper = mountContent()

    await wrapper.get('[data-testid="plaza-platform-openai"]').trigger('click')

    const groups = wrapper.findAll('[data-testid="plaza-group"]')
    expect(groups).toHaveLength(1)
    expect(groups[0].attributes('data-platform')).toBe('openai')
    expect(groups[0].text()).toContain('DeepSeek OpenAI format|deepseek-v4-pro')
  })
})
