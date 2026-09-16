import { nextTick } from 'vue'
import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import SupportedModelChip from '../SupportedModelChip.vue'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key })
  }
})

describe('SupportedModelChip', () => {
  it('仅配置区间倍率时按基础价展示 token 档位', async () => {
    const wrapper = mount(SupportedModelChip, {
      attachTo: document.body,
      props: {
        model: {
          name: 'gpt-test',
          platform: '',
          pricing: {
            billing_mode: 'token',
            input_price: 10e-6,
            output_price: 50e-6,
            cache_write_price: null,
            cache_read_price: null,
            image_input_price: null,
            image_output_price: null,
            per_request_price: null,
            intervals: [{
              min_tokens: 272000,
              max_tokens: null,
              input_price: null,
              output_price: null,
              cache_write_price: null,
              cache_read_price: null,
              input_multiplier: 2,
              output_multiplier: 1.5,
              per_request_price: null
            }]
          }
        },
        showPlatform: false
      }
    })

    await wrapper.find('[tabindex="0"]').trigger('mouseenter')
    await nextTick()

    expect(document.body.textContent).toContain('$20 / $75')
    wrapper.unmount()
  })

  it('shows an optional base-price heading without applying a group rate', async () => {
    const wrapper = mount(SupportedModelChip, {
      attachTo: document.body,
      props: {
        model: {
          name: 'shared-model',
          platform: '',
          pricing: {
            billing_mode: 'token',
            input_price: 10e-6,
            output_price: 50e-6,
            cache_write_price: null,
            cache_read_price: null,
            image_input_price: null,
            image_output_price: null,
            per_request_price: null,
            intervals: []
          }
        },
        pricingHeading: 'availableChannels.pricing.basePrice',
        showPlatform: false
      }
    })

    await wrapper.find('[tabindex="0"]').trigger('mouseenter')
    await nextTick()

    expect(document.body.textContent).toContain('availableChannels.pricing.basePrice')
    expect(document.body.textContent).toContain('$10')
    expect(document.body.textContent).toContain('$50')
    expect(document.body.textContent).not.toContain('$12')
    expect(document.body.textContent).not.toContain('$60')
    wrapper.unmount()
  })

  it('shows effective pricing first and keeps the labeled base price distinct', async () => {
    const base = {
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
    const wrapper = mount(SupportedModelChip, {
      attachTo: document.body,
      props: {
        model: { name: 'glm-test', platform: 'zhipu', pricing: base },
        pricingContexts: [
          { key: 'effective', heading: 'Domestic effective', multiplierLabel: 'base × 0.6', pricing: { ...base, input_price: 0.84e-6, output_price: 2.64e-6, cache_write_price: 0.156e-6 } },
          { key: 'base', heading: 'Channel base', pricing: base },
        ],
      },
    })

    await wrapper.find('[tabindex="0"]').trigger('mouseenter')
    await nextTick()
    const text = document.body.textContent || ''
    expect(text.indexOf('Domestic effective')).toBeLessThan(text.indexOf('Channel base'))
    expect(text).toContain('base × 0.6')
    expect(text).toContain('$0.84')
    expect(text).toContain('$2.64')
    expect(text).toContain('$0.156')
    expect(text).toContain('$1.4')
    expect(text).toContain('$4.4')
    wrapper.unmount()
  })

  it('keeps the scrollable popover open while it is hovered or focused', async () => {
    vi.useFakeTimers()
    const wrapper = mount(SupportedModelChip, {
      attachTo: document.body,
      props: {
        model: { name: 'interactive-model', platform: '', pricing: null },
        noPricingLabel: 'No pricing',
        showPlatform: false,
      },
    })

    const trigger = wrapper.find('[tabindex="0"]')
    await trigger.trigger('mouseenter')
    await nextTick()

    const popover = document.body.querySelector<HTMLElement>('[role="tooltip"]')
    expect(popover).not.toBeNull()
    expect(popover?.classList.contains('pointer-events-auto')).toBe(true)
    expect(popover?.classList.contains('overflow-y-auto')).toBe(true)
    expect(popover?.getAttribute('tabindex')).toBe('0')

    await trigger.trigger('mouseleave')
    popover?.dispatchEvent(new MouseEvent('mouseenter'))
    vi.advanceTimersByTime(100)
    await nextTick()
    expect(popover?.style.display).not.toBe('none')

    popover?.focus()
    popover?.dispatchEvent(new MouseEvent('mouseleave'))
    vi.advanceTimersByTime(100)
    await nextTick()
    expect(popover?.style.display).not.toBe('none')

    popover?.blur()
    vi.advanceTimersByTime(100)
    await nextTick()
    expect(popover?.style.display).toBe('none')

    wrapper.unmount()
    vi.useRealTimers()
  })
})
