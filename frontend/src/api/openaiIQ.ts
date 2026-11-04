import { apiClient } from './client'

export type IQResultStatus = 'correct' | 'incorrect' | 'error' | 'skipped' | 'pending'

export interface IQResult {
  group_id: number
  group_name: string
  rate_multiplier: number
  model: string
  response_model?: string
  expected_answer: string
  model_answer: string
  output: string
  status: IQResultStatus
  checked_at: string | null
  error?: string
}

export interface IQSummary {
  total: number
  correct: number
  incorrect: number
  errors: number
  pending: number
}

export interface OpenAIIQView {
  enabled: boolean
  model: string
  question: string
  expected_answer: string
  interval_seconds: number
  round_started_at: string | null
  results: IQResult[]
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
  remaining_usd: number
}

export interface OpenAIIQSettingsView {
  settings: IQSettings
  groups: IQGroupOption[]
  test_user_id: number | null
  test_user_balance: number
  budget: IQBudget
}

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
