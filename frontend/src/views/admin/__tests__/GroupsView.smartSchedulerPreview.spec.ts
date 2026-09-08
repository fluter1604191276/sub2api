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
  getRecoveryProbeBilling,
  updateRecoveryProbeBilling,
  listOwnAPIKeys,
  listAccounts,
  createGroup,
  updateGroup,
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
  getRecoveryProbeBilling: vi.fn(),
  updateRecoveryProbeBilling: vi.fn(),
  listOwnAPIKeys: vi.fn(),
  listAccounts: vi.fn(),
  createGroup: vi.fn(),
  updateGroup: vi.fn(),
  showError: vi.fn(),
  showSuccess: vi.fn(),
  isCurrentStep: vi.fn(),
  nextStep: vi.fn(),
}))

const messages: Record<string, string> = {
  'admin.groups.smartScheduler.action': 'Smart scheduler',
  'admin.groups.smartScheduler.title': 'Smart scheduler · {name}',
  'admin.groups.smartScheduler.description': 'Read-only smart scheduler preview.',
  'admin.groups.smartScheduler.enabledStatus': 'This group takes over production routing.',
  'admin.groups.smartScheduler.disabledStatus': 'This group uses the original scheduler.',
  'admin.groups.smartScheduler.enableAction': 'Enable smart scheduling',
  'admin.groups.smartScheduler.disableAction': 'Disable smart scheduling',
  'admin.groups.smartScheduler.toggleHint': 'When disabled, requests use the original scheduler. If scoring is abnormal, routing automatically falls back.',
  'admin.groups.smartScheduler.toggleSuccess': 'Smart scheduler setting updated',
  'admin.groups.smartScheduler.recoveryProbe.title': 'Recovery probe',
  'admin.groups.smartScheduler.recoveryProbe.description': 'Off by default. When enabled, lightweight tests probe isolated or long-idle accounts. The idle threshold is fixed at 1h.',
  'admin.groups.smartScheduler.recoveryProbe.enabled': 'Probe enabled',
  'admin.groups.smartScheduler.recoveryProbe.disabled': 'Probe disabled',
  'admin.groups.smartScheduler.recoveryProbe.mode': 'Probe mode',
  'admin.groups.smartScheduler.recoveryProbe.modes.manual': 'Fixed interval',
  'admin.groups.smartScheduler.recoveryProbe.modes.smart': 'Smart backoff',
  'admin.groups.smartScheduler.recoveryProbe.testModel': 'Test model',
  'admin.groups.smartScheduler.recoveryProbe.testModelPlaceholder': 'Required when enabled, e.g. claude-sonnet-4-6',
  'admin.groups.smartScheduler.recoveryProbe.fixedInterval': 'Fixed interval (seconds)',
  'admin.groups.smartScheduler.recoveryProbe.probesPerRound': 'Probes per round',
  'admin.groups.smartScheduler.recoveryProbe.smartBackoffMax': 'Smart backoff max (seconds)',
  'admin.groups.smartScheduler.recoveryProbe.idleThreshold': 'Idle threshold',
  'admin.groups.smartScheduler.recoveryProbe.idleThresholdFixed': 'Fixed 1h',
  'admin.groups.smartScheduler.recoveryProbe.noTestModel': 'not configured',
  'admin.groups.smartScheduler.recoveryProbe.summary': '{status} · {mode} · model {model} · interval {interval} · {count} per round · backoff max {backoff}',
  'admin.groups.smartScheduler.recoveryProbe.saveSuccess': 'Recovery probe settings saved',
  'admin.groups.smartScheduler.recoveryProbe.accountStatus': 'Probe status',
  'admin.groups.smartScheduler.recoveryProbe.noAccountStatus': 'No probe status',
  'admin.groups.smartScheduler.recoveryProbe.accountModel': 'model {model}',
  'admin.groups.smartScheduler.recoveryProbe.accountAttempts': '{successes} consecutive successes / {failures} failures, {total} total',
  'admin.groups.smartScheduler.recoveryProbe.accountLatency': 'latency {latency}',
  'admin.groups.smartScheduler.recoveryProbe.accountLastProbe': 'last {time}',
  'admin.groups.smartScheduler.recoveryProbe.accountNextProbe': 'next {time}',
  'admin.groups.smartScheduler.recoveryProbe.accountError': '{class}: {error}',
  'admin.groups.smartScheduler.recoveryProbe.unknownErrorClass': 'unknown error',
  'admin.groups.smartScheduler.recoveryProbe.billing.title': 'Probe cost ledger',
  'admin.groups.smartScheduler.recoveryProbe.billing.description': 'Charge probes to my account.',
  'admin.groups.smartScheduler.recoveryProbe.billing.enabled': 'Charge my account',
  'admin.groups.smartScheduler.recoveryProbe.billing.disabled': 'Do not charge',
  'admin.groups.smartScheduler.recoveryProbe.billing.apiKey': 'Ledger API key',
  'admin.groups.smartScheduler.recoveryProbe.billing.selectAPIKey': 'Select one of my keys',
  'admin.groups.smartScheduler.recoveryProbe.billing.dailyBudget': 'Daily budget (USD)',
  'admin.groups.smartScheduler.recoveryProbe.billing.perAttemptLimit': 'Per-attempt reserve (USD)',
  'admin.groups.smartScheduler.recoveryProbe.billing.globalToday': 'Global cost today',
  'admin.groups.smartScheduler.recoveryProbe.billing.groupToday': 'Group cost today',
  'admin.groups.smartScheduler.recoveryProbe.billing.remaining': 'Budget remaining',
  'admin.groups.smartScheduler.recoveryProbe.billing.settlementSummary': 'Settlement today',
  'admin.groups.smartScheduler.recoveryProbe.billing.settlementCounts': '{settled} settled · {unavailable} unavailable · {failed} failed',
  'admin.groups.smartScheduler.recoveryProbe.billing.hint': 'Probe rows are isolated from sales and quality.',
  'admin.groups.smartScheduler.recoveryProbe.billing.loadFailed': 'Failed to load probe billing settings',
  'admin.groups.smartScheduler.recoveryProbe.billing.saveSuccess': 'Probe billing settings saved',
  'admin.groups.smartScheduler.recoveryProbe.billing.saveFailed': 'Failed to save probe billing settings',
  'admin.groups.smartScheduler.recoveryProbe.accountStatuses.none': 'No state',
  'admin.groups.smartScheduler.recoveryProbe.accountStates.pending': 'Pending',
  'admin.groups.smartScheduler.recoveryProbe.accountStates.probing': 'Probing',
  'admin.groups.smartScheduler.recoveryProbe.accountStates.warm': 'Warm',
  'admin.groups.smartScheduler.recoveryProbe.accountStates.eligible': 'Eligible',
  'admin.groups.smartScheduler.recoveryProbe.accountStates.failed': 'Failed',
  'admin.groups.smartScheduler.recoveryProbe.accountStates.paused': 'Paused',
  'admin.groups.smartScheduler.poolErrorPolicy.title': 'Group error and retry policy',
  'admin.groups.smartScheduler.poolErrorPolicy.description': 'Batch policy settings.',
  'admin.groups.smartScheduler.poolErrorPolicy.precedence': 'Account > group > default.',
  'admin.groups.smartScheduler.poolErrorPolicy.poolMode': 'Pool mode',
  'admin.groups.smartScheduler.poolErrorPolicy.retryCount': 'Same-account retries',
  'admin.groups.smartScheduler.poolErrorPolicy.retryStatusCodes': 'Same-account retry codes',
  'admin.groups.smartScheduler.poolErrorPolicy.retryStatusCodesPlaceholder': 'e.g. 529',
  'admin.groups.smartScheduler.poolErrorPolicy.customEnabled': 'Custom error handling',
  'admin.groups.smartScheduler.poolErrorPolicy.customCodes': 'Custom error codes',
  'admin.groups.smartScheduler.poolErrorPolicy.customCodesPlaceholder': 'e.g. 529',
  'admin.groups.smartScheduler.poolErrorPolicy.inherit': 'Inherit',
  'admin.groups.smartScheduler.poolErrorPolicy.override': 'Override',
  'admin.groups.smartScheduler.poolErrorPolicy.enabled': 'Enabled',
  'admin.groups.smartScheduler.poolErrorPolicy.disabled': 'Disabled',
  'admin.groups.smartScheduler.poolErrorPolicy.inheritPlaceholder': 'Blank means inherit',
  'admin.groups.smartScheduler.poolErrorPolicy.hint': 'Configure 529 only when confirmed overload.',
  'admin.groups.smartScheduler.poolErrorPolicy.saveSuccess': 'Group error and retry policy saved',
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
  'admin.groups.smartScheduler.geminiModels': 'Gemini native',
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
  'admin.groups.smartScheduler.probeBootstrap': 'Probe bootstrap',
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
  'admin.groups.smartScheduler.immediateFailureSummary': 'Last 5m immediate supplier failures: provider {provider}, transient {transient}, rate limit {rateLimit}, uncertain {uncertain}',
  'admin.groups.smartScheduler.clientSummary': 'Client {client}, platform {platform}, uncertain {uncertain}',
  'admin.groups.smartScheduler.capacityLimitedSummary': 'No-capacity events in last 1h: {count}. Capacity metrics are not included in account quality score.',
  'admin.groups.smartScheduler.loadSummary': 'Load {current}/{max}, waiting {waiting}, rate {rate}',
  'admin.groups.smartScheduler.noLoad': 'No load',
  'admin.groups.smartScheduler.noScore': 'No score',
  'admin.groups.smartScheduler.poolLabels.primary': 'Primary',
  'admin.groups.smartScheduler.poolLabels.warm': 'Warm observe',
  'admin.groups.smartScheduler.poolLabels.isolated': 'Isolated',
  'admin.groups.smartScheduler.confidenceLabels.high': 'High',
  'admin.groups.smartScheduler.confidenceLabels.medium': 'Medium',
  'admin.groups.smartScheduler.confidenceLabels.low': 'Low',
  'admin.groups.form.smartScheduler': 'Smart scheduler',
  'admin.groups.smartScheduler.formHint': 'When disabled, requests use the original scheduler. If scoring is abnormal, routing automatically falls back.',
  'admin.groups.smartScheduler.formEnabled': 'Enabled',
  'admin.groups.smartScheduler.formDisabled': 'Disabled',
  'admin.groups.enterGroupName': 'Enter group name',
  'admin.groups.form.name': 'Name',
  'admin.groups.form.description': 'Description',
  'admin.groups.form.platform': 'Platform',
  'admin.groups.form.rateMultiplier': 'Rate multiplier',
  'admin.groups.form.rpmLimit': 'RPM',
  'admin.groups.form.rpmLimitPlaceholder': '0 = unlimited',
  'admin.groups.form.rpmLimitHint': 'RPM hint',
  'admin.groups.form.exclusive': 'Exclusive',
  'admin.groups.form.status': 'Status',
  'admin.groups.rateMultiplierHint': 'Rate hint',
  'admin.groups.platformNotEditable': 'Platform cannot be edited',
  'admin.groups.groupCreated': 'Group created',
  'admin.groups.groupUpdated': 'Group updated',
  'admin.groups.creating': 'Creating',
  'admin.groups.updating': 'Updating',
  'admin.groups.subscription.type': 'Subscription type',
  'admin.groups.subscription.standard': 'Standard',
  'admin.groups.subscription.subscription': 'Subscription',
  'admin.groups.subscription.typeHint': 'Subscription hint',
  'admin.groups.subscription.typeNotEditable': 'Subscription type cannot be edited',
  'admin.groups.public': 'Public',
  'admin.groups.exclusive': 'Exclusive',
  'admin.groups.allStatus': 'All status',
  'admin.accounts.status.active': 'Active',
  'admin.accounts.status.inactive': 'Inactive',
  'common.create': 'Create',
  'common.save': 'Save',
  'common.cancel': 'Cancel',
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
      getRecoveryProbeBilling,
      updateRecoveryProbeBilling,
      create: createGroup,
      update: updateGroup,
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

vi.mock('@/api/keys', () => ({
  keysAPI: {
    list: listOwnAPIKeys,
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
  smart_scheduler_enabled: false,
  recovery_probe_enabled: false,
  recovery_probe_mode: 'manual',
  recovery_probe_model: '',
  recovery_probe_interval_seconds: 900,
  recovery_probe_attempts_per_round: 1,
  recovery_probe_idle_threshold_seconds: 3600,
  recovery_probe_backoff_cap_seconds: 1800,
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
  probe_bootstrap: false,
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
  recent_provider_failure_count: 0,
  recent_provider_transient_count: 0,
  recent_rate_limit_count: 0,
  recent_uncertain_failure_count: 0,
  immediate_provider_failure_count: 0,
  immediate_provider_transient_count: 0,
  immediate_rate_limit_count: 0,
  immediate_uncertain_failure_count: 0,
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
  recovery_probe: null,
  ...overrides,
})

const preview: SmartSchedulerPreview = {
  group: { id: group.id, name: group.name },
  platform: 'openai',
  requested_model: 'gpt-5.5',
  endpoint: 'responses',
  algorithm_version: 'preview-v3',
  generated_at: '2026-08-01T02:03:04Z',
  total_accounts: 3,
  primary_count: 1,
  warm_count: 1,
  isolated_count: 1,
  exploration_rate: 0.075,
  production_control_active: false,
  load_snapshot_available: true,
  capacity_limited_count_1h: 2,
  warnings: ['Group has no available capacity for this model in the last hour.'],
  items: [
    schedulerItem({
      account_id: 100,
      account_name: 'Primary Account',
      pool: 'primary',
      reason: 'best score',
      immediate_provider_failure_count: 1,
      immediate_rate_limit_count: 2,
      recovery_probe: {
        group_id: 42,
        account_id: 100,
        model: 'gpt-5.5',
        status: 'probing',
        consecutive_successes: 0,
        consecutive_failures: 2,
        last_probe_at: '2026-08-01T02:00:00Z',
        next_probe_at: '2026-08-01T02:12:00Z',
        last_failure_at: '2026-08-01T02:00:00Z',
        last_error_class: 'transient',
        last_error: 'upstream timeout',
        latency_ms: 2400,
        probe_count: 3,
        updated_at: '2026-08-01T02:00:00Z',
      },
    }),
    schedulerItem({ rank: 2, account_id: 101, account_name: 'Warm Account', pool: 'warm', reason: 'observe before primary', score: 72, confidence_label: 'medium', evidence_scope: 'account', evidence_fallback: true, exploration_candidate: true, probe_bootstrap: true }),
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

async function openEditGroupDialog(wrapper: ReturnType<typeof mount>) {
  const editButton = wrapper
    .findAll('button')
    .find((button) => button.text().includes('common.edit'))
  expect(editButton).toBeTruthy()
  await editButton!.trigger('click')
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
      getRecoveryProbeBilling,
      updateRecoveryProbeBilling,
      listOwnAPIKeys,
      listAccounts,
      createGroup,
      updateGroup,
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
    getRecoveryProbeBilling.mockResolvedValue({
      settings: {
        enabled: false,
        owner_user_id: 0,
        api_key_id: 0,
        daily_budget_usd: 1,
        per_attempt_limit_usd: 0.01,
      },
      global_today: {
        today_settled_cost: 0.012,
        today_budget_cost: 0.012,
        today_attempts: 5,
        today_settled: 3,
        today_unavailable: 1,
        today_failed: 1,
      },
      group_today: {
        today_settled_cost: 0.004,
        today_budget_cost: 0.004,
        today_attempts: 2,
        today_settled: 2,
        today_unavailable: 0,
        today_failed: 0,
      },
      remaining_usd: 0.988,
    })
    updateRecoveryProbeBilling.mockResolvedValue({
      enabled: true,
      owner_user_id: 7,
      api_key_id: 11,
      api_key_name: 'Probe ledger',
      daily_budget_usd: 1,
      per_attempt_limit_usd: 0.01,
    })
    listOwnAPIKeys.mockResolvedValue({
      items: [{ id: 11, name: 'Probe ledger', status: 'inactive' }],
      total: 1,
      page: 1,
      page_size: 100,
      pages: 1,
    })
    createGroup.mockResolvedValue({ ...group, id: 43 })
    updateGroup.mockResolvedValue({ ...group })
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

  it('creates groups with smart scheduler disabled by default', async () => {
    const wrapper = await mountLoadedView()

    await wrapper.get('[data-tour="groups-create-btn"]').trigger('click')
    await flushPromises()
    await wrapper.get('input[placeholder="Enter group name"]').setValue('New group')
    await wrapper.get('form#create-group-form').trigger('submit')
    await flushPromises()

    expect(createGroup).toHaveBeenCalledWith(expect.objectContaining({
      name: 'New group',
      smart_scheduler_enabled: false,
    }))
    wrapper.unmount()
  })

  it('reads and updates smart scheduler state in the edit form', async () => {
    listGroups.mockResolvedValueOnce({
      items: [{ ...group, smart_scheduler_enabled: true }],
      total: 1,
      page: 1,
      page_size: 20,
      pages: 1,
    })
    const wrapper = await mountLoadedView()

    await openEditGroupDialog(wrapper)
    await flushPromises()

    const dialogText = wrapper.get('[role="dialog"]').text()
    expect(dialogText).toContain('Smart scheduler')
    expect(dialogText).toContain('Enabled')
    expect(dialogText).toContain('If scoring is abnormal, routing automatically falls back.')

    const smartSchedulerToggle = wrapper.find('[data-testid="edit-smart-scheduler-toggle"]')
    expect(smartSchedulerToggle.exists()).toBe(true)
    await smartSchedulerToggle.trigger('click')
    await wrapper.get('form#edit-group-form').trigger('submit')
    await flushPromises()

    expect(updateGroup).toHaveBeenCalledWith(42, expect.objectContaining({
      smart_scheduler_enabled: false,
    }))
    wrapper.unmount()
  })

  it('requests the default smart scheduler preview when the modal opens', async () => {
    const wrapper = await mountLoadedView()

    await openSmartSchedulerPreview(wrapper)

    expect(getSmartSchedulerPreview).toHaveBeenCalledTimes(1)
    expect(getSmartSchedulerPreview).toHaveBeenCalledWith(42, {})
    wrapper.unmount()
  })

  it('shows takeover status and toggles from the smart scheduler preview', async () => {
    const wrapper = await mountLoadedView()

    await openSmartSchedulerPreview(wrapper)

    expect(wrapper.get('[role="dialog"]').text()).toContain('This group uses the original scheduler.')
    expect(wrapper.get('[role="dialog"]').text()).toContain('If scoring is abnormal, routing automatically falls back.')

    await wrapper.get('[data-testid="smart-scheduler-preview-toggle"]').trigger('click')
    await flushPromises()

    expect(updateGroup).toHaveBeenCalledWith(42, expect.objectContaining({
      smart_scheduler_enabled: true,
    }))
    expect(showSuccess).toHaveBeenCalledWith('Smart scheduler setting updated')
    wrapper.unmount()
  })

  it('keeps recovery probe disabled by default and saves its settings through group update', async () => {
    const wrapper = await mountLoadedView()

    await openSmartSchedulerPreview(wrapper)

    const dialog = wrapper.get('[role="dialog"]')
    expect(dialog.text()).toContain('Recovery probe')
    expect(dialog.text()).toContain('Probe disabled')
    expect(dialog.text()).toContain('Fixed 1h')
    expect(dialog.text()).toContain('Probe disabled · Fixed interval · model not configured · interval 15m · 1 per round · backoff max 30m')

    await dialog.get('[data-testid="recovery-probe-toggle"]').trigger('click')
    await dialog.get('[data-testid="recovery-probe-mode"]').setValue('smart')
    await dialog.get('[data-testid="recovery-probe-test-model"]').setValue('gpt-5.5-mini')
    await dialog.get('[data-testid="recovery-probe-fixed-interval"]').setValue('1800')
    await dialog.get('[data-testid="recovery-probe-probes-per-round"]').setValue('2')
    await dialog.get('[data-testid="recovery-probe-smart-backoff-max"]').setValue('7200')
    await dialog.get('[data-testid="recovery-probe-save"]').trigger('click')
    await flushPromises()

    expect(updateGroup).toHaveBeenCalledWith(42, {
      recovery_probe_enabled: true,
      recovery_probe_mode: 'smart',
      recovery_probe_model: 'gpt-5.5-mini',
      recovery_probe_interval_seconds: 1800,
      recovery_probe_attempts_per_round: 2,
      recovery_probe_idle_threshold_seconds: 3600,
      recovery_probe_backoff_cap_seconds: 7200,
    })
    expect(showSuccess).toHaveBeenCalledWith('Recovery probe settings saved')
    wrapper.unmount()
  })

  it('records probe spend under an owned key with a daily budget', async () => {
    const wrapper = await mountLoadedView()
    await openSmartSchedulerPreview(wrapper)

    const dialog = wrapper.get('[role="dialog"]')
    expect(dialog.text()).toContain('Probe cost ledger')
    expect(dialog.text()).toContain('$0.012000')
    expect(dialog.text()).toContain('3 settled · 1 unavailable · 1 failed')

    await dialog.get('[data-testid="recovery-probe-billing-toggle"]').trigger('click')
    await dialog.get('[data-testid="recovery-probe-billing-api-key"]').setValue('11')
    await dialog.get('[data-testid="recovery-probe-billing-daily-budget"]').setValue('2')
    await dialog.get('[data-testid="recovery-probe-billing-per-attempt-limit"]').setValue('0.02')
    await dialog.get('[data-testid="recovery-probe-billing-save"]').trigger('click')
    await flushPromises()

    expect(updateRecoveryProbeBilling).toHaveBeenCalledWith({
      enabled: true,
      api_key_id: 11,
      daily_budget_usd: 2,
      per_attempt_limit_usd: 0.02,
    })
    expect(showSuccess).toHaveBeenCalledWith('Probe billing settings saved')
    wrapper.unmount()
  })

  it('saves group error policy overrides and supports explicit status 529', async () => {
    const wrapper = await mountLoadedView()
    await openSmartSchedulerPreview(wrapper)

    const dialog = wrapper.get('[role="dialog"]')
    await dialog.get('[data-testid="group-pool-mode-policy"]').setValue('enabled')
    await dialog.get('[data-testid="group-pool-retry-count"]').setValue('2')
    await dialog.get('[data-testid="group-pool-retry-status-policy"]').setValue('override')
    await dialog.get('[data-testid="group-pool-retry-status-codes"]').setValue('529, 503')
    await dialog.get('[data-testid="group-custom-error-enabled-policy"]').setValue('enabled')
    await dialog.get('[data-testid="group-custom-error-policy"]').setValue('override')
    await dialog.get('[data-testid="group-custom-error-codes"]').setValue('529')
    await dialog.get('[data-testid="group-pool-error-policy-save"]').trigger('click')
    await flushPromises()

    expect(updateGroup).toHaveBeenCalledWith(42, {
      pool_mode_enabled: true,
      pool_mode_retry_count: 2,
      pool_mode_retry_status_codes: [503, 529],
      custom_error_codes_enabled: true,
      custom_error_codes: [529],
    })
    expect(showSuccess).toHaveBeenCalledWith('Group error and retry policy saved')
    wrapper.unmount()
  })

  it('uses the preview response as the authoritative production control state', async () => {
    getSmartSchedulerPreview.mockResolvedValueOnce({
      ...preview,
      production_control_active: true,
    })
    const wrapper = await mountLoadedView()

    await openSmartSchedulerPreview(wrapper)

    expect(wrapper.get('[role="dialog"]').text()).toContain('This group takes over production routing.')
    expect(wrapper.get('[data-testid="smart-scheduler-preview-toggle"]').text()).toContain('Disable smart scheduling')

    await wrapper.get('[data-testid="smart-scheduler-preview-toggle"]').trigger('click')
    await flushPromises()

    expect(updateGroup).toHaveBeenCalledWith(42, expect.objectContaining({
      smart_scheduler_enabled: false,
    }))
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
    expect(dialogText).toContain('Algorithm preview-v3')
    expect(dialogText).toContain('Group has no available capacity for this model in the last hour.')
    expect(dialogText).toContain('No-capacity events in last 1h: 2. Capacity metrics are not included in account quality score.')
    expect(dialogText).toContain('Explore 7.5%')
    expect(dialogText).toContain('Model + endpoint')
    expect(dialogText).toContain('Fallback evidence')
    expect(dialogText).toContain('Explore candidate')
    expect(dialogText).toContain('Probe bootstrap')
    expect(dialogText).toContain('Raw 98')
    expect(dialogText).toContain('TTFT 440ms')
    expect(dialogText).toContain('Generation 40.8 tok/s')
    expect(dialogText).toContain('Last 5m immediate supplier failures: provider 1, transient 0, rate limit 2, uncertain 0')
    expect(dialogText).toContain('Probe status')
    expect(dialogText).toContain('Probing')
    expect(dialogText).toContain('0 consecutive successes / 2 failures, 3 total')
    expect(dialogText).toContain('latency 2.4s')
    expect(dialogText).toContain('transient: upstream timeout')
    wrapper.unmount()
  })
})
