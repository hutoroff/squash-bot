import type { AdminUser } from '../types'
import { handleResponse, expectNoContent } from './http'

/** Owner-only: management enforces the check against the DB, not this call. */
export async function fetchUsers(): Promise<AdminUser[]> {
  return handleResponse<AdminUser[]>(await fetch('/api/users'))
}

export async function setServerOwner(userID: number, enabled: boolean): Promise<void> {
  return expectNoContent(await fetch(`/api/users/${userID}/server-owner`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ enabled }),
  }))
}
