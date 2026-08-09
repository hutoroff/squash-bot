import type { UserPreferences } from '../types'
import { ApiError, handleResponse, expectNoContent } from './http'

/** Defaults used before the user has ever changed a preference (management 404s). */
export const DEFAULT_PREFERENCES: UserPreferences = {
  telegram_id: 0,
  dm_language: 'en',
  results_opt_out: false,
}

export async function fetchMyPreferences(): Promise<UserPreferences> {
  try {
    return await handleResponse<UserPreferences>(await fetch('/api/me/preferences'))
  } catch (err) {
    if (err instanceof ApiError && err.status === 404) return DEFAULT_PREFERENCES
    throw err
  }
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
