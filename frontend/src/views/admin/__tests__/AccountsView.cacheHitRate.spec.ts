import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

import AccountsView from '../AccountsView.vue'

const {
  listAccounts,
  listWithEtag,
  getBatchTodayStats,
  getBatchCacheHitStats,
  getBatchQualityStats,
  getAllProxies,
  getAllGroups
} = vi.hoisted(() => ({
  listAccounts: vi.fn(),
  listWithEtag: vi.fn(),
  getBatchTodayStats: vi.fn(),
  getBatchCacheHitStats: vi.fn(),
  getBatchQualityStats: vi.fn(),
  getAllProxies: vi.fn(),
  getAllGroups: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      list: listAccounts,
      listWithEtag,
      getBatchTodayStats,
      getBatchCacheHitStats,
      getBatchQualityStats,
      listSyncedModels: vi.fn().mockResolvedValue([]),
      getUpstreamBillingProbeSettings: vi.fn().mockResolvedValue({ enabled: true, interval_minutes: 30 }),
      delete: vi.fn(),
      batchClearError: vi.fn(),
      batchRefresh: vi.fn(),
      toggleSchedulable: vi.fn()
    },
    proxies: { getAll: getAllProxies },
    groups: { getAll: getAllGroups }
  }
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showError: vi.fn(), showSuccess: vi.fn(), showInfo: vi.fn() })
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({ token: 'test-token', isSimpleMode: false })
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return { ...actual, useI18n: () => ({ t: (key: string) => key }) }
})

const DataTableStub = {
  props: ['columns', 'data'],
  template: `
    <div data-test="data-table">
      <div v-for="row in data" :key="row.id" :data-test="'cache-hit-rate-' + row.id">
        <slot name="cell-cache_hit_rate" :row="row" />
      </div>
    </div>
  `
}

const mountView = () => mount(AccountsView, {
  global: {
    stubs: {
      AppLayout: { template: '<div><slot /></div>' },
      TablePageLayout: { template: '<div><slot name="filters" /><slot name="table" /><slot name="pagination" /></div>' },
      DataTable: DataTableStub,
      HelpTooltip: true,
      Pagination: true,
      ConfirmDialog: true,
      AccountTableActions: { template: '<div><slot name="beforeCreate" /><slot name="after" /></div>' },
      AccountTableFilters: { template: '<div></div>' },
      AccountBulkActionsBar: true,
      AccountActionMenu: true,
      ImportDataModal: true,
      ReAuthAccountModal: true,
      AccountTestModal: true,
      AccountStatsModal: true,
      ScheduledTestsPanel: true,
      SyncFromCrsModal: true,
      TempUnschedStatusModal: true,
      ErrorPassthroughRulesModal: true,
      TLSFingerprintProfilesModal: true,
      CreateAccountModal: true,
      EditAccountModal: true,
      BulkEditAccountModal: true,
      PlatformTypeBadge: true,
      AccountCapacityCell: true,
      AccountStatusIndicator: true,
      AccountTodayStatsCell: true,
      AccountQualityCell: true,
      AccountUnifiedQualityCell: true,
      AccountGroupsCell: true,
      AccountUsageCell: true,
      Icon: true
    }
  }
})

const account = {
  id: 1,
  name: 'cache-account',
  platform: 'openai',
  type: 'apikey',
  status: 'active',
  schedulable: true,
  concurrency: 1,
  priority: 0,
  error_message: null,
  last_used_at: null,
  expires_at: null,
  auto_pause_on_expired: false,
  created_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-01-01T00:00:00Z'
}

describe('admin AccountsView cache hit rate column', () => {
  beforeEach(() => {
    localStorage.clear()
    localStorage.setItem('account-hidden-columns', JSON.stringify([]))
    localStorage.setItem('account-hidden-columns-version', 'scheduler-score-hidden-by-default')
    localStorage.setItem('account-cache-hit-rate-column-version', 'hidden-by-default')

    for (const fn of [listAccounts, listWithEtag, getBatchTodayStats, getBatchCacheHitStats, getBatchQualityStats, getAllProxies, getAllGroups]) {
      fn.mockReset()
    }
    listAccounts.mockResolvedValue({ items: [account], total: 1, page: 1, page_size: 20, pages: 1 })
    listWithEtag.mockResolvedValue({ notModified: true, etag: null, data: null })
    getBatchTodayStats.mockResolvedValue({ stats: {} })
    getBatchCacheHitStats.mockResolvedValue({
      stats: {
        '1': {
          requests: 3,
          input_tokens: 100,
          cache_creation_tokens: 25,
          cache_read_tokens: 75,
          cache_hit_rate: 37.5
        }
      }
    })
    getBatchQualityStats.mockResolvedValue({ stats: {} })
    getAllProxies.mockResolvedValue([])
    getAllGroups.mockResolvedValue([])
  })

  it('fetches and renders the 24h cache hit rate for the current page', async () => {
    const wrapper = mountView()
    await flushPromises()

    const columns = wrapper.getComponent(DataTableStub).props('columns') as Array<{ key: string }>
    expect(columns.some(column => column.key === 'cache_hit_rate')).toBe(true)
    expect(getBatchCacheHitStats).toHaveBeenCalledWith([1])
    expect(wrapper.find('[data-test="cache-hit-rate-1"]').text()).toContain('37.5%')
    expect(wrapper.find('[data-test="cache-hit-rate-1"]').text()).toContain('3 req')
  })

  it('keeps account rows available when cache statistics fail', async () => {
    getBatchCacheHitStats.mockRejectedValue(new Error('stats unavailable'))

    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.getComponent(DataTableStub).props('data')).toHaveLength(1)
    expect(wrapper.find('[data-test="cache-hit-rate-1"]').text()).toBe('-')
  })

  it('does not request cache statistics while the column is hidden', async () => {
    localStorage.setItem('account-hidden-columns', JSON.stringify(['cache_hit_rate']))

    const wrapper = mountView()
    await flushPromises()

    const columns = wrapper.getComponent(DataTableStub).props('columns') as Array<{ key: string }>
    expect(columns.some(column => column.key === 'cache_hit_rate')).toBe(false)
    expect(getBatchCacheHitStats).not.toHaveBeenCalled()
  })
})
