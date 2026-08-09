import type { BotGroup } from '../types'
import { handleResponse, expectNoContent } from './http'

export async function fetchGroups(): Promise<BotGroup[]> {
  const res = await fetch('/api/groups')
  return handleResponse<BotGroup[]>(res)
}

export async function fetchGroup(chatID: number): Promise<BotGroup> {
  const res = await fetch(`/api/groups/${chatID}`)
  return handleResponse<BotGroup>(res)
}

function patchGroup(chatID: number, path: string, body: unknown): Promise<void> {
  return fetch(`/api/groups/${chatID}/${path}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  }).then(expectNoContent)
}

export function updateGroupLanguage(chatID: number, language: string): Promise<void> {
  return patchGroup(chatID, 'language', { language })
}

export function updateGroupTimezone(chatID: number, timezone: string): Promise<void> {
  return patchGroup(chatID, 'timezone', { timezone })
}

export function updateGroupChangelog(chatID: number, enabled: boolean): Promise<void> {
  return patchGroup(chatID, 'changelog', { changelog_enabled: enabled })
}

export function updateGroupLeaderboardNotifications(chatID: number, enabled: boolean): Promise<void> {
  return patchGroup(chatID, 'leaderboard-notifications', { leaderboard_notifications_enabled: enabled })
}

export function updateGroupAutoBookingAllowed(chatID: number, enabled: boolean): Promise<void> {
  return patchGroup(chatID, 'auto-booking-allowed', { enabled })
}
