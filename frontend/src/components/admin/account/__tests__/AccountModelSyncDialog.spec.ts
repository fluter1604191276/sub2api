import { describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import AccountModelSyncDialog from '../AccountModelSyncDialog.vue'

const { applyModelMappings, showSuccess, showError } = vi.hoisted(() => ({ applyModelMappings: vi.fn(), showSuccess: vi.fn(), showError: vi.fn() }))
vi.mock('@/api/admin', () => ({ adminAPI: { accounts: { applyModelMappings } } }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showSuccess, showError }) }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string, values?: Record<string, unknown>) => values ? `${key}:${JSON.stringify(values)}` : key }) }))

const BaseDialog = { props: ['show'], emits: ['close'], template: '<div v-if="show"><slot /></div>' }
const entries = Array.from({ length: 401 }, (_, i) => ({ account_id: i + 1, account_name: `a${i + 1}`, status: 'upstream', version: `v${i + 1}`, upstream_models: [`m${i + 1}`], added: [`m${i + 1}`], removed: [] }))

describe('AccountModelSyncDialog', () => {
  it('hides removals and skips removal-only entries in add mode', async () => {
    applyModelMappings.mockReset().mockResolvedValue({ results: [] })
    const wrapper = mount(AccountModelSyncDialog, { props: { show: true, entries: [
      { ...entries[0], added: [], removed: ['stale-model'] }, entries[1]
    ] }, global: { stubs: { BaseDialog } } })
    await wrapper.find('input[value="add"]').setValue()
    expect(wrapper.text()).not.toContain('stale-model')
    await wrapper.find('button').trigger('click')
    await flushPromises()
    expect(applyModelMappings.mock.calls[0][0]).toHaveLength(1)
    expect(applyModelMappings.mock.calls[0][0][0].mode).toBe('add')
  })

  it('locks the mode while a batch is pending', async () => {
    let finish!: (value: { results: unknown[] }) => void
    applyModelMappings.mockReset().mockImplementationOnce(() => new Promise(resolve => { finish = resolve }))
    const wrapper = mount(AccountModelSyncDialog, { props: { show: true, entries: [entries[0]] }, global: { stubs: { BaseDialog } } })
    await wrapper.find('button').trigger('click')
    expect(wrapper.find('input[value="add"]').attributes('disabled')).toBeDefined()
    expect(applyModelMappings.mock.calls[0][0][0].mode).toBe('sync')
    finish({ results: [] })
    await flushPromises()
  })

  it('splits selected entries into 200-item requests and keeps partial results visible', async () => {
    applyModelMappings.mockReset()
    applyModelMappings
      .mockResolvedValueOnce({ results: Array.from({ length: 200 }, (_, i) => ({ account_id: i + 1, status: 'applied' })) })
      .mockResolvedValueOnce({ results: [{ account_id: 201, status: 'conflict' }, ...Array.from({ length: 199 }, (_, i) => ({ account_id: i + 202, status: 'applied' }))] })
      .mockResolvedValueOnce({ results: [{ account_id: 401, status: 'failed' }] })
    const wrapper = mount(AccountModelSyncDialog, { props: { show: true, entries }, global: { stubs: { BaseDialog } } })
    await wrapper.find('button').trigger('click')
    await flushPromises()
    expect(applyModelMappings).toHaveBeenCalledTimes(3)
    expect(applyModelMappings.mock.calls.map(call => call[0].length)).toEqual([200, 200, 1])
    expect(wrapper.emitted('close')).toBeUndefined()
    expect(wrapper.text()).toContain('admin.accounts.modelSync.statusApplied')
    expect(wrapper.text()).toContain('admin.accounts.modelSync.statusConflict')
  })
})
