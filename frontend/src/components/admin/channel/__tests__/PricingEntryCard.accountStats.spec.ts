import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import { defineComponent, h } from 'vue'

import PricingEntryCard from '../PricingEntryCard.vue'
import type { PricingFormEntry } from '../types'

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string) => key,
  }),
}))

vi.mock('@/api/admin/channels', () => ({
  default: {
    getModelDefaultPricing: vi.fn(),
  },
}))

const SelectStub = defineComponent({
  props: {
    modelValue: [String, Number, Boolean, null],
    options: { type: Array, required: true },
  },
  emits: ['update:modelValue'],
  setup(props, { attrs, emit }) {
    return () => h('select', {
      ...attrs,
      value: props.modelValue ?? '',
      onChange: (event: Event) => emit('update:modelValue', (event.target as HTMLSelectElement).value),
    }, (props.options as Array<{ value: string, label: string }>).map(option =>
      h('option', { value: option.value }, option.label)
    ))
  },
})

function makeEntry(overrides: Partial<PricingFormEntry>): PricingFormEntry {
  return {
    models: ['gpt-image-1'],
    billing_mode: 'image',
    input_price: null,
    output_price: null,
    cache_write_price: null,
    cache_read_price: null,
    image_output_price: null,
    per_request_price: null,
    intervals: [],
    image_operation: null,
    ...overrides,
  }
}

function mountCard(entry: PricingFormEntry, accountStats = false) {
  return mount(PricingEntryCard, {
    props: {
      entry,
      accountStats,
      platform: 'openai',
    },
    global: {
      stubs: {
        Select: SelectStub,
        Icon: true,
        IntervalRow: true,
        ModelTagInput: true,
      },
    },
  })
}

describe('PricingEntryCard account stats image operation', () => {
  it('hides the operation selector for primary image pricing', () => {
    const wrapper = mountCard(makeEntry({ billing_mode: 'image' }))

    expect(wrapper.find('[data-testid="account-stats-image-operation"]').exists()).toBe(false)
  })

  it('shows the operation selector for account-stats image pricing', () => {
    const wrapper = mountCard(makeEntry({ billing_mode: 'image' }), true)

    expect(wrapper.find('[data-testid="account-stats-image-operation"]').exists()).toBe(true)
    expect(wrapper.text()).toContain('admin.channels.form.imageOperation')
  })

  it('hides the operation selector for account-stats token pricing', () => {
    const wrapper = mountCard(makeEntry({ billing_mode: 'token' }), true)

    expect(wrapper.find('[data-testid="account-stats-image-operation"]').exists()).toBe(false)
  })

  it('clears the image operation when account-stats image mode changes away from image', async () => {
    const wrapper = mountCard(makeEntry({
      billing_mode: 'image',
      image_operation: 'generation',
    }), true)

    await wrapper.findAll('select')[0].setValue('token')

    expect(wrapper.emitted('update')?.[0]?.[0]).toMatchObject({
      billing_mode: 'token',
      image_operation: null,
      intervals: [],
    })
  })
})
