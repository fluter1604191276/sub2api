import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import AccountPriorityConversionDialog from '../AccountPriorityConversionDialog.vue'
import type { PriorityConversionRow } from '@/api/admin/accounts'

const { preview, apply } = vi.hoisted(() => ({ preview: vi.fn(), apply: vi.fn() }))
vi.mock('@/api/admin/accounts', () => ({ previewPriorityConversion: preview, applyPriorityConversion: apply }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
const BaseDialog = { props: ['show'], emits: ['close'], template: '<div v-if="show"><slot /><slot name="footer" /></div>' }
const rows: PriorityConversionRow[] = [
  { account_id: 1, name: 'manual priority', rate_multiplier: '2.0070', old_priority: 7, new_priority: 2007, applied: false },
  { account_id: 2, name: 'zero multiplier', rate_multiplier: '0.0000', old_priority: 50, new_priority: 1, applied: false },
  { account_id: 3, name: 'invalid multiplier', rate_multiplier: '-1.0000', old_priority: 50, new_priority: null, reason: 'negative_multiplier', applied: false }
]
const open = () => mount(AccountPriorityConversionDialog, { props: { show: true, accountIds: [1, 2, 3] }, global: { stubs: { BaseDialog } } })
beforeEach(() => { preview.mockReset().mockResolvedValue({ rows, updated: 0 }); apply.mockReset() })

describe('manual account priority conversion', () => {
  it('previews per-account multiplier/old/new values and cancels with zero writes', async () => {
    const wrapper = open(); await flushPromises()
    expect(preview).toHaveBeenCalledWith([1, 2, 3])
    expect(wrapper.text()).toContain('2.0070'); expect(wrapper.text()).toContain('2007')
    expect(wrapper.text()).toContain('negative_multiplier')
    expect(apply).not.toHaveBeenCalled()
    await wrapper.find('button.btn-secondary').trigger('click')
    expect(wrapper.emitted('close')).toHaveLength(1); expect(apply).not.toHaveBeenCalled()
  })
  it('confirms only valid rows once and retains exceptions after a partial result', async () => {
    apply.mockResolvedValue({ rows: [{ ...rows[0], applied: true }, { ...rows[1], reason: 'preview_changed' }], updated: 1 })
    const wrapper = open(); await flushPromises(); await wrapper.find('button.btn-primary').trigger('click'); await flushPromises()
    expect(apply).toHaveBeenCalledExactlyOnceWith(rows.slice(0, 2))
    expect(wrapper.emitted('updated')).toHaveLength(1)
    expect(wrapper.text()).toContain('negative_multiplier'); expect(wrapper.text()).toContain('preview_changed')
    expect(wrapper.find('button.btn-primary').exists()).toBe(false)
  })
  it('does not allow confirmation when every multiplier is invalid', async () => {
    preview.mockResolvedValue({ rows: [rows[2]], updated: 0 })
    const wrapper = open(); await flushPromises()
    expect(wrapper.find('button.btn-primary').attributes('disabled')).toBeDefined(); expect(apply).not.toHaveBeenCalled()
  })
  it('does not repeat an ambiguous failed write automatically', async () => {
    apply.mockRejectedValue(new Error('connection closed after possible commit'))
    const wrapper = open(); await flushPromises(); await wrapper.find('button.btn-primary').trigger('click'); await flushPromises()
    expect(apply).toHaveBeenCalledTimes(1); expect(wrapper.text()).toContain('applyFailed')
    expect(wrapper.find('button.btn-primary').exists()).toBe(false)
  })
  it('ignores an old preview after close and reopen', async () => {
    let finish!: (value: unknown) => void
    preview.mockImplementationOnce(() => new Promise(resolve => { finish = resolve }))
    const wrapper = open(); await wrapper.setProps({ show: false }); await wrapper.setProps({ show: true }); await flushPromises()
    finish({ rows: [{ ...rows[0], name: 'obsolete preview' }], updated: 0 }); await flushPromises()
    expect(wrapper.text()).not.toContain('obsolete preview'); expect(wrapper.text()).toContain('manual priority')
  })
})
