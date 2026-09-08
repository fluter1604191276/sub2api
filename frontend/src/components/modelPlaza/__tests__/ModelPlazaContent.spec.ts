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

function mountContent() {
  return mount(ModelPlazaContent, {
    props: { response, loading: false },
    global: {
      stubs: {
        CatalogSurfaceNav: true,
        Icon: true,
        PlatformIcon: true,
        PlazaGroupSection: {
          props: ['group'],
          template:
            '<section data-testid="plaza-group" :data-platform="group.platform">{{ group.name }}|{{ group.models.map((item) => item.name).join(",") }}</section>',
        },
      },
    },
  })
}

describe('ModelPlazaContent protocol platform filtering', () => {
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
