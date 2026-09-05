import { expectNoContent, handleResponse } from './http'

export interface FeatureFlag {
  key: string
  description: string
  service: string
  default: boolean
  group_scoped: boolean
  global: boolean | null
  override: boolean | null
  enabled: boolean
  source: 'default' | 'global' | 'group'
}

function scope(groupID?: number): string {
  return groupID === undefined ? '' : `?group_id=${groupID}`
}
export async function fetchFeatureFlags(groupID?: number): Promise<FeatureFlag[]> {
  return handleResponse<FeatureFlag[]>(await fetch(`/api/feature-flags${scope(groupID)}`))
}
export async function setFeatureFlag(key: string, enabled: boolean | null, groupID?: number): Promise<void> {
  await expectNoContent(await fetch(`/api/feature-flags/${encodeURIComponent(key)}${scope(groupID)}`, {
    method: 'PATCH', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ enabled }),
  }))
}
