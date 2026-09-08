import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import PublicCatalogView from '../PublicCatalogView.vue'

const { getVisibility, updateVisibility, showError, showSuccess } = vi.hoisted(() => ({
  getVisibility: vi.fn(),
  updateVisibility: vi.fn(),
  showError: vi.fn(),
  showSuccess: vi.fn(),
}))

vi.mock('@/api/admin/publicCatalog', () => ({
  getVisibility,
  updateVisibility,
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showError, showSuccess }),
}))

vi.mock('vue-i18n', async (importOriginal) => ({
  ...(await importOriginal<typeof import('vue-i18n')>()),
  useI18n: () => ({ t: (key: string) => key }),
}))

const initialView = {
  default_media_visibility: 'hidden' as const,
  models: { 'retired:legacy-image': false },
  candidates: [
    {
      key: 'openai:gpt-5.6-sol',
      platform: 'openai',
      model: 'gpt-5.6-sol',
      billing_mode: 'token',
      is_media: false,
      default_visible: true,
      visible: true,
    },
    {
      key: 'gemini:gemini-3.1-flash-image',
      platform: 'gemini',
      model: 'gemini-3.1-flash-image',
      billing_mode: 'image',
      is_media: true,
      default_visible: false,
      visible: false,
    },
    {
      key: 'video:seedance-2.0',
      platform: 'video',
      model: 'seedance-2.0',
      billing_mode: 'video',
      is_media: true,
      default_visible: false,
      visible: false,
    },
  ],
}

function mountView() {
  return mount(PublicCatalogView, {
    global: {
      stubs: {
        AppLayout: { template: '<div><slot /></div>' },
        Icon: true,
      },
    },
  })
}

describe('PublicCatalogView', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    getVisibility.mockResolvedValue(structuredClone(initialView))
    updateVisibility.mockImplementation(async (payload) => ({
      ...structuredClone(initialView),
      models: { ...payload.models },
      candidates: initialView.candidates.map((candidate) => ({
        ...candidate,
        visible: payload.models[candidate.key] ?? candidate.default_visible,
      })),
    }))
  })

  it('saves a per-model override without dropping stale overrides', async () => {
    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.text()).toContain('admin.publicCatalog.presentationOnlyNotice')
    await wrapper.get('[data-testid="visibility-gemini:gemini-3.1-flash-image"]').trigger('click')
    await wrapper.get('[data-testid="save-public-catalog"]').trigger('click')
    await flushPromises()

    expect(updateVisibility).toHaveBeenCalledWith({
      default_media_visibility: 'hidden',
      models: {
        'retired:legacy-image': false,
        'gemini:gemini-3.1-flash-image': true,
      },
    })
    expect(showSuccess).toHaveBeenCalledWith('admin.publicCatalog.saveSuccess')
  })

  it('bulk visibility changes apply only to the current filtered results', async () => {
    const wrapper = mountView()
    await flushPromises()

    await wrapper.get('[data-testid="catalog-search"]').setValue('seedance')
    await wrapper.get('[data-testid="bulk-show-catalog"]').trigger('click')
    await wrapper.get('[data-testid="save-public-catalog"]').trigger('click')
    await flushPromises()

    const payload = updateVisibility.mock.calls[0][0]
    expect(payload.models).toEqual({
      'retired:legacy-image': false,
      'video:seedance-2.0': true,
    })
    expect(payload.models['gemini:gemini-3.1-flash-image']).toBeUndefined()
    expect(payload.models['openai:gpt-5.6-sol']).toBeUndefined()
  })
})
