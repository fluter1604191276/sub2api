import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import OpsResponseDiagnosticsTable from '../OpsResponseDiagnosticsTable.vue'

const mockListResponseDiagnostics = vi.fn()
const mockShowError = vi.fn()

vi.mock('@/api/admin/ops', () => ({
  opsAPI: { listResponseDiagnostics: (...args: unknown[]) => mockListResponseDiagnostics(...args) },
}))
vi.mock('@/stores', () => ({ useAppStore: () => ({ showError: (...args: unknown[]) => mockShowError(...args) }) }))
vi.mock('vue-i18n', async importOriginal => {
  const actual = await importOriginal<typeof import('vue-i18n')>()
  return { ...actual, useI18n: () => ({ t: (key: string, values?: Record<string, unknown>) => values?.count == null ? key : `${key}:${values.count}` }) }
})

const record = {
  id: 1, created_at: '2026-10-18T12:18:07Z', request_id: 'server-req', client_request_id: 'client-req',
  upstream_request_id: 'upstream-req', response_id: 'resp_123', user_id: 331, account_id: 7900, group_id: 33,
  model: 'gpt-6.1-sol', stream: true, http_status: 200, status: 'incomplete' as const, reason: 'max_output_tokens',
  requested_max_output_tokens: 0, forwarded_max_output_tokens: null, upstream_max_output_tokens: 2048,
  input_tokens: 0, cache_read_tokens: null, cache_creation_tokens: 0, output_tokens: 1, reasoning_tokens: null,
}

describe('OpsResponseDiagnosticsTable', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockListResponseDiagnostics.mockResolvedValue({ items: [record], total: 41, page: 1, page_size: 20 })
  })

  async function mountTable() {
    const wrapper = mount(OpsResponseDiagnosticsTable)
    await flushPromises()
    return wrapper
  }

  it('shows zero values, unknown nullable values, separate budgets, and labeled IDs', async () => {
    const wrapper = await mountTable()
    const text = wrapper.text()
    expect(text).toContain('admin.ops.responseDiagnostics.serverRequestId: server-req')
    expect(text).toContain('admin.ops.responseDiagnostics.clientRequestId: client-req')
    expect(text).toContain('admin.ops.responseDiagnostics.upstreamRequestId: upstream-req')
    expect(text).toContain('admin.ops.responseDiagnostics.responseId: resp_123')
    expect(text).toContain('admin.ops.responseDiagnostics.requestedLimit: 0')
    expect(text).toContain('admin.ops.responseDiagnostics.forwardedLimit: admin.ops.responseDiagnostics.unknown')
    expect(text).toContain('admin.ops.responseDiagnostics.upstreamLimit: 2048')
    expect(text).toContain('admin.ops.responseDiagnostics.inputTokens: 0')
    expect(text).toContain('admin.ops.responseDiagnostics.cacheReadTokens: admin.ops.responseDiagnostics.unknown')
  })

  it('keeps a completed upstream status when the client disconnected', async () => {
    mockListResponseDiagnostics.mockResolvedValueOnce({
      items: [{ ...record, status: 'completed', client_disconnected: true }], total: 1, page: 1, page_size: 20,
    })
    const wrapper = await mountTable()
    const rowText = wrapper.find('tbody tr').text()
    expect(rowText).toContain('admin.ops.responseDiagnostics.statuses.completed')
    expect(rowText).toContain('admin.ops.responseDiagnostics.clientDisconnected')
    expect(rowText).not.toContain('admin.ops.responseDiagnostics.statuses.client_disconnected')
  })

  it('submits changed filters, resets the page, and pages the current result', async () => {
    const wrapper = await mountTable()
    const fields = wrapper.findAll('input')
    await fields[2]!.setValue('max_output_tokens')
    await fields[3]!.setValue('7900')
    await fields[4]!.setValue('331')
    await fields[5]!.setValue('server-req')
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(mockListResponseDiagnostics).toHaveBeenLastCalledWith(expect.objectContaining({ page: 1, reason: 'max_output_tokens', account_id: 7900, user_id: 331, request_id: 'server-req' }))

    const next = wrapper.findAll('button').find(button => button.text() === 'common.next')!
    await next.trigger('click')
    await flushPromises()
    expect(mockListResponseDiagnostics).toHaveBeenLastCalledWith(expect.objectContaining({ page: 2, account_id: 7900 }))
    await fields[3]!.setValue('7901')
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(mockListResponseDiagnostics).toHaveBeenLastCalledWith(expect.objectContaining({ page: 1, account_id: 7901 }))
  })

  it('rejects invalid IDs before querying', async () => {
    const wrapper = await mountTable()
    mockListResponseDiagnostics.mockClear()
    await wrapper.findAll('input')[3]!.setValue('not-an-id')
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(mockListResponseDiagnostics).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('admin.ops.responseDiagnostics.invalidId')
  })

  it('reports a failed load', async () => {
    mockListResponseDiagnostics.mockRejectedValueOnce(new Error('unavailable'))
    await mountTable()
    expect(mockShowError).toHaveBeenCalledWith('admin.ops.responseDiagnostics.loadFailed')
  })
})
