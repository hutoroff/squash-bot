import type { BotGroup } from '../types'
import { handleResponse } from './http'

export async function fetchGroups(): Promise<BotGroup[]> {
  const res = await fetch('/api/groups')
  return handleResponse<BotGroup[]>(res)
}

export async function updateGroupAutoBookingAllowed(chatID: number, enabled: boolean): Promise<void> {
  const res = await fetch(`/api/groups/${chatID}/auto-booking-allowed`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ enabled }),
  })
  if (!res.ok) {
    const body = await res.json().catch(() => ({}))
    throw new Error(body.error || `HTTP ${res.status}`)
  }
}
