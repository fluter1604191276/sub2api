import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import OpsUpstreamAttemptLedger from '../OpsUpstreamAttemptLedger.vue'

const listDailyCosts = vi.fn()
const showError = vi.fn()

vi.mock('@/api/admin/ops', () => ({ opsAPI: { listUpstreamAttemptDailyCosts: (...args: unknown[]) => listDailyCosts(...args) } }))
vi.mock('@/stores', () => ({ useAppStore: () => ({ showError: (...args: unknown[]) => showError(...args) }) }))
vi.mock('vue-i18n', async importOriginal => {
  const actual = await importOriginal<typeof import('vue-i18n')>()
  return { ...actual, useI18n: () => ({ t: (key: string) => key }) }
})

const row = { day: '2026-10-18T00:00:00Z', account_id: 9, attempt_count: 2, known_cost_usd: 0.125, unknown_count: 1 }

describe('OpsUpstreamAttemptLedger', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    listDailyCosts.mockResolvedValue([row])
  })

  async function mountLedger() {
    const wrapper = mount(OpsUpstreamAttemptLedger)
    await flushPromises()
    return wrapper
  }

  it('shows Shanghai day and unknown attempts without converting them to zero', async () => {
    const wrapper = await mountLedger()
    const text = wrapper.text()
    expect(text).toContain('2026-10-18')
    expect(text).toContain('0.125000')
    expect(text).toContain('1')
    expect(text).not.toContain('10/17/2026')
  })

  it('passes account and inclusive Beijing date filters', async () => {
    const wrapper = await mountLedger()
    const inputs = wrapper.findAll('input')
    await inputs[0]!.setValue('9')
    await inputs[1]!.setValue('2026-10-01')
    await inputs[2]!.setValue('2026-10-18')
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(listDailyCosts).toHaveBeenLastCalledWith({
      account_id: 9,
      start_time: '2026-10-01T00:00:00+08:00',
      end_time: '2026-10-19T00:00:00+08:00',
      time_range: undefined,
    })
  })

  it('allows 30 inclusive days and rejects 31', async () => {
    const wrapper = await mountLedger()
    listDailyCosts.mockClear()
    const inputs = wrapper.findAll('input')
    await inputs[1]!.setValue('2026-10-01')
    await inputs[2]!.setValue('2026-10-30')
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(listDailyCosts).toHaveBeenCalledOnce()

    listDailyCosts.mockClear()
    await inputs[2]!.setValue('2026-10-31')
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(listDailyCosts).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('admin.ops.upstreamAttempts.invalidDateRange')
  })

  it('rejects invalid input and reports request errors', async () => {
    const wrapper = await mountLedger()
    listDailyCosts.mockClear()
    await wrapper.findAll('input')[0]!.setValue('1.5')
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(listDailyCosts).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('admin.ops.upstreamAttempts.invalidAccountId')

    listDailyCosts.mockRejectedValueOnce(new Error('offline'))
    await wrapper.findAll('input')[0]!.setValue('9')
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(showError).toHaveBeenCalledWith('admin.ops.upstreamAttempts.loadFailed')
  })

  it('does not let a stale response replace newer filters', async () => {
    let resolveFirst: (value: typeof row[]) => void = () => undefined
    listDailyCosts.mockReset()
    listDailyCosts.mockReturnValueOnce(new Promise<typeof row[]>(resolve => { resolveFirst = resolve }))
    const wrapper = mount(OpsUpstreamAttemptLedger)
    await flushPromises()
    listDailyCosts.mockResolvedValueOnce([{ ...row, account_id: 10, unknown_count: 3 }])
    await wrapper.findAll('input')[0]!.setValue('10')
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    resolveFirst([row])
    await flushPromises()
    const rowText = wrapper.find('tbody tr').text()
    expect(rowText).toContain('10')
    expect(rowText).toContain('3')
    expect(rowText).not.toContain(' 9 ')
  })
})
