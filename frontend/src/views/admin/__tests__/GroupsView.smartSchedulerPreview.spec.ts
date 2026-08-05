import { defineComponent } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type {
  AccountQualityActivity,
  AccountQualityPeriod,
  AccountQualityWindow,
  AdminGroup,
  SmartSchedulerPreview,
  SmartSchedulerPreviewItem,
} from '@/types'
import GroupsView from '@/views/admin/GroupsView.vue'

const {
  listGroups,
  getAllGroups,
  getModelsListCandidates,
  getUsageSummary,
  getCapacitySummary,
  getBatchQualityStats,
  getLiveCapability,
  getSmartSchedulerPreview,
  listAccounts,
  showError,
  showSuccess,
  isCurrentStep,
  nextStep,
} = vi.hoisted(() => ({
  listGroups: vi.fn(),
  getAllGroups: vi.fn(),
  getModelsListCandidates: vi.fn(),
  getUsageSummary: vi.fn(),
  getCapacitySummary: vi.fn(),
  getBatchQualityStats: vi.fn(),
  getLiveCapability: vi.fn(),
  getSmartSchedulerPreview: vi.fn(),
  listAccounts: vi.fn(),
  showError: vi.fn(),
  showSuccess: vi.fn(),
  isCurrentStep: vi.fn(),
  nextStep: vi.fn(),
}))

const messages: Record<string, string> = {
  'admin.groups.smartScheduler.action': 'Smart scheduler',
  'admin.groups.smartScheduler.title': 'Smart scheduler · {name}',
  'admin.groups.smartScheduler.description': 'Read-only smart scheduler preview.',
  'admin.groups.smartScheduler.requestedModel': 'Requested model',
  'admin.groups.smartScheduler.requestedModelPlaceholder': 'gpt-5.5',
  'admin.groups.smartScheduler.endpoint': 'Endpoint',
  'admin.groups.smartScheduler.refresh': 'Refresh preview',
  'admin.groups.smartScheduler.generatedAt': 'Generated {time}',
  'admin.groups.smartScheduler.algorithm': 'Algorithm {version}',
  'admin.groups.smartScheduler.noModel': 'No model',
  'admin.groups.smartScheduler.anyEndpoint': 'Any endpoint',
  'admin.groups.smartScheduler.chatCompletions': 'Chat completions',
  'admin.groups.smartScheduler.responses': 'Responses',
  'admin.groups.smartScheduler.messages': 'Messages',
  'admin.groups.smartScheduler.primary': 'Primary',
  'admin.groups.smartScheduler.warm': 'Warm observe',
  'admin.groups.smartScheduler.isolated': 'Isolated',
  'admin.groups.smartScheduler.total': 'Total',
  'admin.groups.smartScheduler.noAccounts': 'No accounts',
  'admin.groups.smartScheduler.noEvidence': 'No evidence',
  'admin.groups.smartScheduler.rank': 'Rank',
  'admin.groups.smartScheduler.account': 'Account',
  'admin.groups.smartScheduler.score': 'Score',
  'admin.groups.smartScheduler.rawScore': 'Raw {score}',
  'admin.groups.smartScheduler.exploration': 'Exploration preview',
  'admin.groups.smartScheduler.explorationRate': 'Explore {rate}',
  'admin.groups.smartScheduler.explorationCandidate': 'Explore candidate',
  'admin.groups.smartScheduler.fallbackEvidence': 'Fallback evidence',
  'admin.groups.smartScheduler.evidenceScopes.model_endpoint': 'Model + endpoint',
  'admin.groups.smartScheduler.evidenceScopes.model': 'Model',
  'admin.groups.smartScheduler.evidenceScopes.endpoint': 'Endpoint',
  'admin.groups.smartScheduler.evidenceScopes.account': 'Account global',
  'admin.groups.smartScheduler.quality1h': '1h quality',
  'admin.groups.smartScheduler.quality24h': '24h quality',
  'admin.groups.smartScheduler.errors': 'Errors',
  'admin.groups.smartScheduler.cost': 'Cost',
  'admin.groups.smartScheduler.load': 'Load',
  'admin.groups.smartScheduler.decision': 'Decision',
  'admin.groups.smartScheduler.firstToken': 'TTFT',
  'admin.groups.smartScheduler.generationSpeed': 'Generation',
  'admin.groups.smartScheduler.failureSummary': 'Provider {provider}, transient {transient}, rate limit {rateLimit}',
  'admin.groups.smartScheduler.clientSummary': 'Client {client}, platform {platform}, uncertain {uncertain}',
  'admin.groups.smartScheduler.loadSummary': 'Load {current}/{max}, waiting {waiting}, rate {rate}',
  'admin.groups.smartScheduler.noLoad': 'No load',
  'admin.groups.smartScheduler.noScore': 'No score',
  'admin.groups.smartScheduler.poolLabels.primary': 'Primary',
  'admin.groups.smartScheduler.poolLabels.warm': 'Warm observe',
  'admin.groups.smartScheduler.poolLabels.isolated': 'Isolated',
  'admin.groups.smartScheduler.confidenceLabels.high': 'High',
  'admin.groups.smartScheduler.confidenceLabels.medium': 'Medium',
  'admin.groups.smartScheduler.confidenceLabels.low': 'Low',
  'common.close': 'Close',
}

vi.mock('@/api/admin', () => ({
  adminAPI: {
    groups: {
      list: listGroups,
      getAll: getAllGroups,
      getModelsListCandidates,
      getUsageSummary,
      getCapacitySummary,
      getBatchQualityStats,
      getLiveCapability,
      getSmartSchedulerPreview,
      create: vi.fn(),
      update: vi.fn(),
      delete: vi.fn(),
      updateSortOrder: vi.fn(),
      duplicate: vi.fn(),
    },
    accounts: {
      list: listAccounts,
      getById: vi.fn(),
    },
  },
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError,
    showSuccess,
  }),
}))

vi.mock('@/stores/onboarding', () => ({
  useOnboardingStore: () => ({
    isCurrentStep,
    nextStep,
  }),
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, unknown>) => {
        const template = messages[key] ?? key
        if (!params) return template
        return Object.entries(params).reduce(
          (text, [paramKey, value]) => text.replace(`{${paramKey}}`, String(value)),
          template,
        )
      },
    }),
  }
})

const group: AdminGroup = {
  id: 42,
  name: 'OpenAI Primary',
  description: null,
  platform: 'openai',
  rate_multiplier: 1,
  rpm_limit: 0,
  is_exclusive: false,
  status: 'active',
  subscription_type: 'standard',
  daily_limit_usd: null,
  weekly_limit_usd: null,
  monthly_limit_usd: null,
  allow_image_generation: false,
  allow_batch_image_generation: false,
  batch_image_discount_multiplier: 0.5,
  batch_image_hold_multiplier: 0.6,
  image_rate_independent: false,
  image_rate_multiplier: 1,
  image_price_1k: null,
  image_price_2k: null,
  image_price_4k: null,
  video_rate_independent: false,
  video_rate_multiplier: 1,
  video_price_480p: null,
  video_price_720p: null,
  video_price_1080p: null,
  web_search_price_per_call: null,
  peak_rate_enabled: false,
  peak_start: '',
  peak_end: '',
  peak_rate_multiplier: 1,
  claude_code_only: false,
  fallback_group_id: null,
  fallback_group_id_on_invalid_request: null,
  allow_messages_dispatch: false,
  default_mapped_model: '',
  messages_dispatch_model_config: undefined,
  require_oauth_only: false,
  require_privacy_set: false,
  created_at: '2026-08-01T00:00:00Z',
  updated_at: '2026-08-01T00:00:00Z',
  model_routing: null,
  model_routing_enabled: false,
  mcp_xml_inject: true,
  supported_model_scopes: [],
  account_count: 3,
  active_account_count: 3,
  rate_limited_account_count: 0,
  models_list_config: undefined,
  sort_order: 10,
}

const qualityWindow = (score: number | null = 92): AccountQualityWindow => ({
  sample_count: 12,
  first_token_sample_count: 10,
  average_first_token_ms: 420,
  average_duration_ms: 1600,
  p50_first_token_ms: 350,
  p90_first_token_ms: 650,
  generation_sample_count: 10,
  p50_generation_tokens_per_second: 48,
  p10_generation_tokens_per_second: 24,
  routing_first_token_ms: 440,
  routing_generation_tokens_per_second: 40.8,
  quality_score: score,
  quality_grade: score == null ? undefined : 'A',
})

const qualityPeriod = (score: number | null = 92): AccountQualityPeriod => ({
  last_10: qualityWindow(score),
  last_100: qualityWindow(score),
  window_hours: 24,
})

const activity: AccountQualityActivity = {
  state: 'active',
  successful_request_count: 30,
  failed_request_count: 1,
  last_success_at: '2026-08-01T01:00:00Z',
  last_error_at: null,
}

const schedulerItem = (
  overrides: Partial<SmartSchedulerPreviewItem>,
): SmartSchedulerPreviewItem => ({
  rank: 1,
  account_id: 100,
  account_name: 'Primary Account',
  platform: 'openai',
  priority: 100,
  status: 'active',
  schedulable: true,
  pool: 'primary',
  decision: 'primary',
  reason: 'best score',
  score: 96,
  raw_score: 98,
  confidence: 0.91,
  confidence_label: 'high',
  evidence_scope: 'model_endpoint',
  evidence_fallback: false,
  exploration_candidate: false,
  quality_1h: qualityPeriod(96),
  quality_24h: qualityPeriod(95),
  activity,
  error_successful_request_count: 30,
  provider_failure_count: 0,
  provider_transient_failure_count: 0,
  rate_limit_count: 0,
  client_excluded_count: 0,
  platform_failure_count: 0,
  uncertain_failure_count: 0,
  cost_multiplier: 1,
  load: {
    current_concurrency: 1,
    waiting_count: 0,
    load_rate: 0.2,
    max_concurrency: 5,
  },
  model_supported: true,
  endpoint_supported: true,
  model_mapping: '',
  last_used_at: null,
  ...overrides,
})

const preview: SmartSchedulerPreview = {
  group: { id: group.id, name: group.name },
  platform: 'openai',
  requested_model: 'gpt-5.5',
  endpoint: 'responses',
  algorithm_version: 'preview-v2',
  generated_at: '2026-08-01T02:03:04Z',
  total_accounts: 3,
  primary_count: 1,
  warm_count: 1,
  isolated_count: 1,
  exploration_rate: 0.075,
  production_control_active: false,
  load_snapshot_available: true,
  warnings: [],
  items: [
    schedulerItem({ account_id: 100, account_name: 'Primary Account', pool: 'primary', reason: 'best score' }),
    schedulerItem({ rank: 2, account_id: 101, account_name: 'Warm Account', pool: 'warm', reason: 'observe before primary', score: 72, confidence_label: 'medium', evidence_scope: 'account', evidence_fallback: true, exploration_candidate: true }),
    schedulerItem({ rank: 3, account_id: 102, account_name: 'Isolated Account', pool: 'isolated', reason: 'excluded by failures', score: 25, schedulable: false, confidence_label: 'low', load: null }),
  ],
}

const AppLayoutStub = defineComponent({
  template: '<main><slot /></main>',
})

const TablePageLayoutStub = defineComponent({
  template: '<section><slot name="filters" /><slot name="table" /><slot name="pagination" /></section>',
})

const DataTableStub = defineComponent({
  props: {
    data: { type: Array, default: () => [] },
  },
  template: '<div><div v-for="row in data" :key="row.id"><slot name="cell-actions" :row="row" /></div></div>',
})

const BaseDialogStub = defineComponent({
  props: {
    show: { type: Boolean, default: false },
    title: { type: String, default: '' },
  },
  template: '<section v-if="show" role="dialog"><h2>{{ title }}</h2><slot /><slot name="footer" /></section>',
})

const SelectStub = defineComponent({
  props: {
    modelValue: { type: String, default: '' },
    options: { type: Array, default: () => [] },
  },
  emits: ['update:modelValue', 'change'],
  template: `
    <select
      :value="modelValue"
      @change="$emit('update:modelValue', $event.target.value); $emit('change')"
    >
      <option v-for="option in options" :key="String(option.value)" :value="option.value">
        {{ option.label }}
      </option>
    </select>
  `,
})

function mountView() {
  return mount(GroupsView, {
    global: {
      stubs: {
        AppLayout: AppLayoutStub,
        TablePageLayout: TablePageLayoutStub,
        DataTable: DataTableStub,
        Pagination: true,
        BaseDialog: BaseDialogStub,
        ConfirmDialog: true,
        EmptyState: true,
        Select: SelectStub,
        PlatformIcon: true,
        Icon: true,
        GroupCapacityBadge: true,
        GroupRateMultipliersModal: true,
        GroupRPMOverridesModal: true,
        VueDraggable: { template: '<div><slot /></div>' },
      },
    },
  })
}

async function mountLoadedView() {
  const wrapper = mountView()
  await flushPromises()
  return wrapper
}

async function openSmartSchedulerPreview(wrapper: ReturnType<typeof mount>) {
  await wrapper.get('button[title="Smart scheduler"]').trigger('click')
  await flushPromises()
}

describe('admin GroupsView smart scheduler preview', () => {
  beforeEach(() => {
    localStorage.clear()
    vi.spyOn(console, 'error').mockImplementation(() => {})

    for (const fn of [
      listGroups,
      getAllGroups,
      getModelsListCandidates,
      getUsageSummary,
      getCapacitySummary,
      getBatchQualityStats,
      getLiveCapability,
      getSmartSchedulerPreview,
      listAccounts,
      showError,
      showSuccess,
      isCurrentStep,
      nextStep,
    ]) {
      fn.mockReset()
    }

    listGroups.mockResolvedValue({
      items: [group],
      total: 1,
      page: 1,
      page_size: 20,
      pages: 1,
    })
    getAllGroups.mockResolvedValue([])
    getModelsListCandidates.mockResolvedValue([])
    getUsageSummary.mockResolvedValue([])
    getCapacitySummary.mockResolvedValue([])
    getBatchQualityStats.mockResolvedValue({ stats: {} })
    getLiveCapability.mockResolvedValue({ supported: false })
    getSmartSchedulerPreview.mockResolvedValue(preview)
    listAccounts.mockResolvedValue({ items: [], total: 0, page: 1, page_size: 20, pages: 0 })
    isCurrentStep.mockReturnValue(false)
  })

  afterEach(() => {
    vi.restoreAllMocks()
    localStorage.clear()
  })

  it('opens the smart scheduler preview modal from the group action', async () => {
    const wrapper = await mountLoadedView()

    await openSmartSchedulerPreview(wrapper)

    expect(wrapper.get('[role="dialog"]').text()).toContain('Smart scheduler · OpenAI Primary')
    expect(wrapper.get('[role="dialog"]').text()).toContain('Read-only smart scheduler preview.')
    wrapper.unmount()
  })

  it('requests the default smart scheduler preview when the modal opens', async () => {
    const wrapper = await mountLoadedView()

    await openSmartSchedulerPreview(wrapper)

    expect(getSmartSchedulerPreview).toHaveBeenCalledTimes(1)
    expect(getSmartSchedulerPreview).toHaveBeenCalledWith(42, {})
    wrapper.unmount()
  })

  it('requests a filtered smart scheduler preview when refreshed', async () => {
    const wrapper = await mountLoadedView()
    await openSmartSchedulerPreview(wrapper)

    await wrapper.get('input[placeholder="gpt-5.5"]').setValue('  gpt-5.5-mini  ')
    const endpointSelect = wrapper
      .findAll('select')
      .find((select) => select.text().includes('Any endpoint'))
    expect(endpointSelect).toBeTruthy()
    await endpointSelect!.setValue('responses')
    await wrapper.get('button[title="Refresh preview"]').trigger('click')
    await flushPromises()

    expect(getSmartSchedulerPreview).toHaveBeenLastCalledWith(42, {
      model: 'gpt-5.5-mini',
      endpoint: 'responses',
    })
    wrapper.unmount()
  })

  it('renders smart scheduler accounts in their preview pools', async () => {
    const wrapper = await mountLoadedView()

    await openSmartSchedulerPreview(wrapper)

    const dialogText = wrapper.get('[role="dialog"]').text()
    expect(dialogText).toContain('Primary Account')
    expect(dialogText).toContain('Warm Account')
    expect(dialogText).toContain('Isolated Account')
    expect(dialogText).toContain('best score')
    expect(dialogText).toContain('observe before primary')
    expect(dialogText).toContain('excluded by failures')
    expect(dialogText).toContain('Algorithm preview-v2')
    expect(dialogText).toContain('Explore 7.5%')
    expect(dialogText).toContain('Model + endpoint')
    expect(dialogText).toContain('Fallback evidence')
    expect(dialogText).toContain('Explore candidate')
    expect(dialogText).toContain('Raw 98')
    expect(dialogText).toContain('TTFT 440ms')
    expect(dialogText).toContain('Generation 40.8 tok/s')
    wrapper.unmount()
  })
})
