import type { UserPreferences } from '../types'
import { handleResponse, expectNoContent } from './http'

/** Initial state shown while the real preferences are loading. */
export const DEFAULT_PREFERENCES: UserPreferences = {
  user_id: 0,
  dm_language: 'en',
  results_opt_out: false,
}

export async function fetchMyPreferences(): Promise<UserPreferences> {
  return handleResponse<UserPreferences>(await fetch('/api/me/preferences'))
}

export async function updateDMLanguage(language: string): Promise<void> {
  return expectNoContent(await fetch('/api/me/dm-language', {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ language }),
  }))
}

export async function updateResultsOptOut(optOut: boolean): Promise<void> {
  return expectNoContent(await fetch('/api/me/results-opt-out', {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ opt_out: optOut }),
  }))
}
