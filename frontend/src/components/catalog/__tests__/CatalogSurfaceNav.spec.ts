import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import CatalogSurfaceNav from '../CatalogSurfaceNav.vue'

vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string) => key })
}))

function mountNav(current: 'channels' | 'plaza') {
  return mount(CatalogSurfaceNav, {
    props: { current },
    global: {
      stubs: {
        RouterLink: { template: '<a><slot /></a>' },
        Icon: { template: '<i />' }
      }
    }
  })
}

describe('CatalogSurfaceNav', () => {
  it('marks the active catalog surface and keeps both destinations visible', () => {
    const wrapper = mountNav('plaza')
    const links = wrapper.findAll('a')

    expect(links).toHaveLength(2)
    expect(links[0].attributes('aria-current')).toBeUndefined()
    expect(links[1].attributes('aria-current')).toBe('page')
    expect(links[0].classes()).toContain('text-gray-500')
    expect(links[1].classes()).toContain('bg-gray-100')
  })
})
