import type { BillingMode } from '@/api/admin/channels'
import type { PricingFormEntry } from './types'
import { findModelConflict } from './types'

export const accountStatsImageOperationOptions = [
  { value: '', labelKey: 'admin.channels.form.imageOperationAny' },
  { value: 'generation', labelKey: 'admin.channels.form.imageOperationGeneration' },
  { value: 'responses', labelKey: 'admin.channels.form.imageOperationResponses' },
  { value: 'edit', labelKey: 'admin.channels.form.imageOperationEdit' },
] as const

const accountStatsImageTiers = new Set(['1K', '2K', '4K'])

export function isAccountStatsImageTierLabel(label: string): boolean {
  return accountStatsImageTiers.has(label.trim().toUpperCase())
}

function conflictScope(
  entry: Pick<PricingFormEntry, 'platform' | 'billing_mode' | 'image_operation'>,
  defaultPlatform: string,
): string {
  const platform = entry.platform ?? defaultPlatform
  if ((entry.billing_mode as BillingMode) === 'image') {
    return `${platform}\x00image:${entry.image_operation ?? ''}`
  }
  return `${platform}\x00non-image`
}

export function findAccountStatsPricingConflict(
  entries: PricingFormEntry[],
  defaultPlatform = '',
): [string, string] | null {
  const scopedModels = new Map<string, string[]>()
  for (const entry of entries) {
    const scope = conflictScope(entry, defaultPlatform)
    const models = scopedModels.get(scope) ?? []
    models.push(...entry.models)
    scopedModels.set(scope, models)
  }

  for (const models of scopedModels.values()) {
    const conflict = findModelConflict(models)
    if (conflict) return conflict
  }
  return null
}
