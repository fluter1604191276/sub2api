import { defineComponent } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import {
  BUILTIN_PLATFORM_CATALOG,
  resetPlatformCatalog,
  setPlatformCatalog
} from '@/constants/platformCatalog'
import ChannelsView from '@/views/admin/ChannelsView.vue'

const { listChannels, getGroups, getModelCatalog, syncPricingModels, showSuccess, showError } = vi.hoisted(() => ({
  listChannels: vi.fn(),
  getGroups: vi.fn(),
  getModelCatalog: vi.fn(),
  syncPricingModels: vi.fn(),
  showSuccess: vi.fn(),
  showError: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    channels: { list: listChannels, getModelCatalog, syncPricingModels },
    groups: { getAll: getGroups },
    settings: { getWebSearchEmulationConfig: vi.fn().mockResolvedValue({ enabled: false }) }
  }
}))

vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showSuccess, showError }) }))

vi.mock('vue-i18n', async () => ({
  ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'),
  useI18n: () => ({ t: (key: string) => key })
}))

function mountView() {
  return mount(ChannelsView, {
    global: {
      stubs: {
        AppLayout: defineComponent({ template: '<main><slot /></main>' }),
        TablePageLayout: defineComponent({
          template: '<section><slot name="filters" /><slot name="table" /><slot name="pagination" /></section>'
        }),
        BaseDialog: defineComponent({
          props: ['show'],
          template: '<section v-if="show"><slot /><slot name="footer" /></section>'
        }),
        PricingEntryCard: defineComponent({
          props: ['entry'],
          template: '<div class="pricing-entry">{{ entry.models.join(", ") }}</div>'
        }),
        DataTable: defineComponent({
          props: ['data'],
          template: '<section><template v-for="row in data"><slot name="cell-configured_model_count" :row="row" /><slot name="cell-actions" :row="row" /></template></section>'
        }),
        Pagination: true,
        ConfirmDialog: true,
        EmptyState: true,
        Select: true,
        Icon: true,
        PlatformIcon: true,
        Toggle: true
      }
    }
  })
}

async function openDialog(wrapper: ReturnType<typeof mountView>) {
  await flushPromises()
  await wrapper.findAll('button').find(button => button.text() === 'admin.channels.createChannel')!.trigger('click')
  await flushPromises()
}

async function enablePlatform(wrapper: ReturnType<typeof mountView>, platform: string) {
  const label = wrapper.findAll('label').find(label => label.text() === `admin.groups.platforms.${platform}`)
  expect(label).toBeDefined()
  await label!.get('input[type="checkbox"]').setValue(true)
  await wrapper.findAll('button.channel-tab').find(button => button.text() === `admin.groups.platforms.${platform}`)!.trigger('click')
}

function visibleSyncButtons(wrapper: ReturnType<typeof mountView>) {
  return wrapper.findAll('button').filter(button => button.isVisible() && [
    'admin.channels.form.syncLatestModels',
    'admin.channels.form.syncingModels'
  ].includes(button.text()))
}

describe('ChannelsView pricing model sync', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    localStorage.clear()
    listChannels.mockResolvedValue({ items: [], total: 0 })
    getGroups.mockResolvedValue([])
    getModelCatalog.mockResolvedValue({ source: 'configured_accounts', availability: 'configured_not_live', channels: [] })
    syncPricingModels.mockReset()
  })

  afterEach(() => {
    resetPlatformCatalog()
  })

  it.each([
    ['openai', true],
    ['opencode_go', true],
    ['typesafe', true],
    ['command_code', false],
    ['cline', false]
  ] as const)('offers pricing model sync for %s only when its pricing catalog exists', async (platform, expected) => {
    const wrapper = mountView()
    await openDialog(wrapper)
    await enablePlatform(wrapper, platform)

    expect(visibleSyncButtons(wrapper)).toHaveLength(expected ? 1 : 0)
    expect(syncPricingModels).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('reacts to a new platform gaining a pricing catalog', async () => {
    const platform = { id: 'acme_router', display_name: 'Acme Router', gateway: 'openai' as const, cn_provider: false }
    setPlatformCatalog({
      ...BUILTIN_PLATFORM_CATALOG,
      platforms: [...BUILTIN_PLATFORM_CATALOG.platforms, platform]
    })
    const wrapper = mountView()
    await openDialog(wrapper)
    await enablePlatform(wrapper, platform.id)
    expect(visibleSyncButtons(wrapper)).toHaveLength(0)

    setPlatformCatalog({
      ...BUILTIN_PLATFORM_CATALOG,
      platforms: [...BUILTIN_PLATFORM_CATALOG.platforms, { ...platform, litellm_provider: 'acme' }]
    })
    await flushPromises()
    expect(visibleSyncButtons(wrapper)).toHaveLength(1)
    wrapper.unmount()
  })

  it('blocks additional syncs while one request is in flight and adds its models once', async () => {
    let resolveSync!: (value: { models: string[] }) => void
    syncPricingModels.mockReturnValue(new Promise(resolve => { resolveSync = resolve }))
    const wrapper = mountView()
    await openDialog(wrapper)
    await enablePlatform(wrapper, 'openai')
    await visibleSyncButtons(wrapper)[0].trigger('click')
    await visibleSyncButtons(wrapper)[0].trigger('click')
    await enablePlatform(wrapper, 'opencode_go')

    expect(visibleSyncButtons(wrapper)[0].attributes('disabled')).toBeDefined()
    await visibleSyncButtons(wrapper)[0].trigger('click')
    expect(syncPricingModels).toHaveBeenCalledOnce()
    expect(syncPricingModels).toHaveBeenCalledWith('openai')

    resolveSync({ models: ['gpt-latest'] })
    await flushPromises()
    expect(wrapper.findAll('.pricing-entry')).toHaveLength(1)
    expect(wrapper.get('.pricing-entry').text()).toBe('gpt-latest')
    expect(visibleSyncButtons(wrapper)[0].attributes('disabled')).toBeUndefined()
    expect(showError).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('shows configured account models read-only without adding them to price rules', async () => {
    const channel = {
      id: 23,
      name: 'OpenAI channel',
      description: '',
      status: 'active',
      billing_model_source: 'channel_mapped',
      restrict_models: false,
      group_ids: [4],
      model_pricing: [{
        platform: 'openai', models: ['gpt-old'], billing_mode: 'token',
        input_price: null, output_price: null, cache_write_price: null, cache_read_price: null,
        image_input_price: null, image_output_price: null, per_request_price: null, intervals: [], time_pricing: null
      }],
      model_mapping: {}, apply_pricing_to_account_stats: false, account_stats_pricing_rules: [],
      created_at: '', updated_at: ''
    }
    listChannels.mockResolvedValue({ items: [channel], total: 1 })
    getGroups.mockResolvedValue([{ id: 4, name: 'OpenAI', platform: 'openai', rate_multiplier: 1 }])
    getModelCatalog.mockResolvedValue({
      source: 'configured_accounts', availability: 'configured_not_live',
      channels: [{ channel_id: 23, models: [{ name: 'gpt-6.1-sol', platform: 'openai' }] }]
    })

    const wrapper = mountView()
    await flushPromises()
    expect(wrapper.get('[data-testid="configured-model-count"]').text()).toBe('1')
    expect(wrapper.get('[data-testid="configured-model-count"]').attributes('title')).toContain('gpt-6.1-sol (openai)')
    await wrapper.findAll('button').find(button => button.text() === 'common.edit')!.trigger('click')
    await flushPromises()

    expect(wrapper.get('[data-testid="configured-model-overview"]').text()).toContain('gpt-6.1-sol')
    expect(wrapper.get('[data-testid="configured-model-overview"]').text()).not.toContain('gpt-old')
    expect(wrapper.get('.pricing-entry').text()).toBe('gpt-old')
    expect(getModelCatalog).toHaveBeenCalled()
    wrapper.unmount()
  })

  it('refreshes the read-only model overview from the latest account configuration', async () => {
    const channel = {
      id: 23, name: 'OpenAI channel', description: '', status: 'active', billing_model_source: 'channel_mapped',
      restrict_models: false, group_ids: [4], model_pricing: [], model_mapping: {},
      apply_pricing_to_account_stats: false, account_stats_pricing_rules: [], created_at: '', updated_at: ''
    }
    listChannels.mockResolvedValue({ items: [channel], total: 1 })
    getGroups.mockResolvedValue([{ id: 4, name: 'OpenAI', platform: 'openai', rate_multiplier: 1 }])
    getModelCatalog
      .mockResolvedValueOnce({ source: 'configured_accounts', availability: 'configured_not_live', channels: [{ channel_id: 23, models: [{ name: 'gpt-old', platform: 'openai' }] }] })
      .mockResolvedValueOnce({ source: 'configured_accounts', availability: 'configured_not_live', channels: [{ channel_id: 23, models: [{ name: 'gpt-6.1-sol', platform: 'openai' }] }] })
      .mockResolvedValueOnce({ source: 'configured_accounts', availability: 'configured_not_live', channels: [{ channel_id: 23, models: [{ name: 'gpt-6.1-astra', platform: 'openai' }] }] })

    const wrapper = mountView()
    await flushPromises()
    await wrapper.findAll('button').find(button => button.text() === 'common.edit')!.trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="configured-model-overview"]').text()).toContain('gpt-6.1-sol')

    await wrapper.find('[title="common.refresh"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="configured-model-overview"]').text()).toContain('gpt-6.1-astra')
    expect(wrapper.get('[data-testid="configured-model-overview"]').text()).not.toContain('gpt-6.1-sol')
    wrapper.unmount()
  })

  it('does not present a stale model as configured when the catalog request fails', async () => {
    const channel = {
      id: 23, name: 'OpenAI channel', description: '', status: 'active', billing_model_source: 'channel_mapped',
      restrict_models: false, group_ids: [4], model_pricing: [], model_mapping: {},
      apply_pricing_to_account_stats: false, account_stats_pricing_rules: [], created_at: '', updated_at: ''
    }
    listChannels.mockResolvedValue({ items: [channel], total: 1 })
    getGroups.mockResolvedValue([{ id: 4, name: 'OpenAI', platform: 'openai', rate_multiplier: 1 }])
    getModelCatalog.mockRejectedValue(new Error('unavailable'))

    const wrapper = mountView()
    await flushPromises()
    await wrapper.findAll('button').find(button => button.text() === 'common.edit')!.trigger('click')
    await flushPromises()

    expect(wrapper.get('[data-testid="configured-model-overview"]').text()).toContain('admin.channels.form.configuredModelsUnavailable')
    expect(wrapper.get('[data-testid="configured-model-overview"]').text()).not.toContain('gpt-6.1-sol')
    wrapper.unmount()
  })
})
