import { apiClient } from './client'

export type IQPublicResultStatus = 'correct' | 'incorrect' | 'no_results'
export type IQDiagnosticStatus = 'correct' | 'incorrect' | 'error' | 'skipped' | 'pending'

export interface IQPublicResult {
  group_id: number
  group_name: string
  rate_multiplier: number
  model: string
  question: string
  expected_answer: string
  model_answer: string
  output: string
  status: IQPublicResultStatus
  checked_at: string | null
}

export interface IQSummary {
  total: number
  correct: number
  incorrect: number
  no_results: number
}

export interface OpenAIIQView {
  enabled: boolean
  model: string
  question: string
  expected_answer: string
  interval_seconds: number
  results: IQPublicResult[]
  summary: IQSummary
}

export interface IQSettings {
  enabled: boolean
  question: string
  expected_answer: string
  group_ids: number[]
  model: string
  interval_seconds: number
  daily_budget_usd: number
  per_request_limit_usd: number
  max_output_tokens: number
}

export interface IQGroupOption {
  id: number
  name: string
  rate_multiplier: number
}

export interface IQBudget {
  date: string
  spent_usd: number
  reserved_usd: number
  unconfirmed_usd: number
  unconfirmed_since: string | null
  remaining_usd: number
}

export interface OpenAIIQSettingsView {
  settings: IQSettings
  groups: IQGroupOption[]
  test_user_id: number | null
  test_user_balance: number
  budget: IQBudget
}

export interface IQDiagnosticReservation {
  reserved_usd: number
  actual_cost_usd: number | null
  request_id: string | null
  usage_log_id: number | null
  account_id: number | null
  settled_at: string | null
}
export interface IQDiagnosticResult {
  id: number
  run_id: number
  group_id: number
  group_name: string
  rate_multiplier: number
  model: string
  response_model: string
  expected_answer: string
  model_answer: string
  output: string
  status: IQDiagnosticStatus
  checked_at: string | null
  error: string
  reservation: IQDiagnosticReservation | null
}
export interface IQDiagnosticRun {
  id: number
  trigger?: 'scheduled' | 'manual'
  group_ids?: number[]
  // Older diagnostic responses omit this. Treat an omitted value conservatively
  // as active while a run still reports status=running.
  lease_active?: boolean
  model: string
  question: string
  expected_answer: string
  status: string
  started_at: string
  finished_at: string | null
  results: IQDiagnosticResult[]
}
export interface OpenAIIQDiagnostics { runs: IQDiagnosticRun[]; total: number; page: number; page_size: number }
export interface OpenAIIQManualRecheck { run_id: number }

export async function getOpenAIIQ(): Promise<OpenAIIQView> {
  const { data } = await apiClient.get<OpenAIIQView>('/openai-iq')
  return data
}

export async function getOpenAIIQSettings(): Promise<OpenAIIQSettingsView> {
  const { data } = await apiClient.get<OpenAIIQSettingsView>('/admin/openai-iq/settings')
  return data
}

export async function updateOpenAIIQSettings(settings: IQSettings): Promise<OpenAIIQSettingsView> {
  const { data } = await apiClient.put<OpenAIIQSettingsView>('/admin/openai-iq/settings', settings)
  return data
}

export async function getOpenAIIQDiagnostics(page = 1, pageSize = 20): Promise<OpenAIIQDiagnostics> {
  const { data } = await apiClient.get<OpenAIIQDiagnostics>('/admin/openai-iq/diagnostics', { params: { page, page_size: pageSize } })
  return data
}

export async function recheckOpenAIIQGroup(groupID: number, confirmedLimitUSD: number): Promise<OpenAIIQManualRecheck> {
  const { data } = await apiClient.post<OpenAIIQManualRecheck>(`/admin/openai-iq/groups/${groupID}/recheck`, {
    confirm_cost: true,
    confirmed_limit_usd: confirmedLimitUSD,
  })
  return data
}
