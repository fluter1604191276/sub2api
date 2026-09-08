import { apiClient } from '../client'

export type PublicCatalogMediaVisibility = 'hidden' | 'visible'

export interface PublicCatalogModelCandidate {
  key: string
  platform: string
  model: string
  billing_mode: string
  is_media: boolean
  default_visible: boolean
  visible: boolean
}

export interface PublicCatalogVisibilityConfig {
  default_media_visibility: PublicCatalogMediaVisibility
  models: Record<string, boolean>
}

export interface PublicCatalogVisibilityView extends PublicCatalogVisibilityConfig {
  candidates: PublicCatalogModelCandidate[]
}

export async function getVisibility(): Promise<PublicCatalogVisibilityView> {
  const { data } = await apiClient.get<PublicCatalogVisibilityView>('/admin/public-catalog/visibility')
  return data
}

export async function updateVisibility(
  config: PublicCatalogVisibilityConfig,
): Promise<PublicCatalogVisibilityView> {
  const { data } = await apiClient.put<PublicCatalogVisibilityView>(
    '/admin/public-catalog/visibility',
    config,
  )
  return data
}

export const publicCatalogAPI = {
  getVisibility,
  updateVisibility,
}

export default publicCatalogAPI
