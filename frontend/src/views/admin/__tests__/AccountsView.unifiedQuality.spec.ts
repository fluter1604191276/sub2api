import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

import AccountsView from '../AccountsView.vue'

const {
  listAccounts,
  listWithEtag,
  getBatchTodayStats,
  getBatchQualityStats,
  getAllProxies,
  getAllGroups,
} = vi.hoisted(() => ({
  listAccounts: vi.fn(),
  listWithEtag: vi.fn(),
  getBatchTodayStats: vi.fn(),
  getBatchQualityStats: vi.fn(),
  getAllProxies: vi.fn(),
  getAllGroups: vi.fn(),
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      list: listAccounts,
      listWithEtag,
      getBatchTodayStats,
      getBatchQualityStats,
      listSyncedModels: vi.fn().mockResolvedValue([]),
      getUpstreamBillingProbeSettings: vi.fn().mockResolvedValue({ enabled: true, interval_minutes: 30 }),
      delete: vi.fn(),
      batchClearError: vi.fn(),
      batchRefresh: vi.fn(),
      toggleSchedulable: vi.fn(),
    },
    proxies: { getAll: getAllProxies },
    groups: { getAll: getAllGroups },
  },
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showError: vi.fn(), showSuccess: vi.fn(), showInfo: vi.fn() }),
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({ token: 'test-token' }),
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key }),
  }
})

const DataTableStub = {
  props: ['columns', 'data'],
  template: `
    <div>
      <div v-for="row in data" :key="row.id" :data-test="'unified-quality-' + row.id">
        <slot name="cell-unified_quality" :row="row" />
      </div>
    </div>
  `,
}

function mountView() {
  return mount(AccountsView, {
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
        AccountGroupsCell: true,
        AccountUsageCell: true,
        Icon: true,
      },
    },
  })
}

describe('admin AccountsView unified quality column', () => {
  beforeEach(() => {
    localStorage.clear()
    listAccounts.mockReset()
    listWithEtag.mockReset()
    getBatchTodayStats.mockReset()
    getBatchQualityStats.mockReset()
    getAllProxies.mockReset()
    getAllGroups.mockReset()

    listAccounts.mockResolvedValue({
      items: [{
        id: 1,
        name: 'shared-account',
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
        updated_at: '2026-01-01T00:00:00Z',
      }],
      total: 1,
      page: 1,
      page_size: 20,
      pages: 1,
    })
    listWithEtag.mockResolvedValue({ notModified: true, etag: null, data: null })
    getBatchTodayStats.mockResolvedValue({ stats: {} })
    getBatchQualityStats.mockResolvedValue({
      stats: {
        '1': {
          last_10: {},
          last_100: {},
          window_hours: 24,
          recent_1h: {},
          activity: { state: 'active', successful_request_count: 4, failed_request_count: 0 },
          unified: {
            score: 82,
            grade: 'A+',
            confidence: 0.87,
            source: 'realtime_blend',
            sample_count: 10,
            first_token_sample_count: 10,
          },
          score_version: 2,
        },
      },
    })
    getAllProxies.mockResolvedValue([])
    getAllGroups.mockResolvedValue([])
  })

  it('shows the unified score by default', async () => {
    const wrapper = mountView()
    await flushPromises()

    const columns = wrapper.getComponent(DataTableStub).props('columns') as Array<{ key: string }>
    expect(columns.some(column => column.key === 'unified_quality')).toBe(true)
    expect(wrapper.find('[data-test="unified-quality-1"]').text()).toContain('A+')
    expect(getBatchQualityStats).toHaveBeenCalledWith([1])
  })

  it('still fetches quality data when only the unified column is visible', async () => {
    localStorage.setItem('account-hidden-columns', JSON.stringify(['quality_stats', 'quality_stats_1h']))

    mountView()
    await flushPromises()

    expect(getBatchQualityStats).toHaveBeenCalledWith([1])
  })

  it('marks an unassigned account even when its historical activity says active', async () => {
	const wrapper = mountView()
	await flushPromises()

	const cell = wrapper.find('[data-test="unified-quality-1"]')
	expect(cell.text()).toContain('admin.accounts.quality.activity.unassigned')
	expect(cell.text()).not.toContain('admin.accounts.quality.activity.active')
  })
})
