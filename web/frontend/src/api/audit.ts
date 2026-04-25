import type { AuditEvent, AuditFilters } from '../types'
import { handleResponse } from './http'

export async function fetchAuditEvents(filters: AuditFilters = {}): Promise<AuditEvent[]> {
  const params = new URLSearchParams()
  if (filters.event_type !== undefined) params.set('event_type', filters.event_type)
  if (filters.from !== undefined) params.set('from', filters.from)
  if (filters.to !== undefined) params.set('to', filters.to)
  if (filters.group_id !== undefined) params.set('group_id', String(filters.group_id))
  if (filters.actor_tg_id !== undefined) params.set('actor_tg_id', String(filters.actor_tg_id))
  if (filters.before_id !== undefined) params.set('before_id', String(filters.before_id))
  if (filters.limit !== undefined) params.set('limit', String(filters.limit))

  const qs = params.toString()
  const res = await fetch('/api/audit' + (qs ? '?' + qs : ''))
  return handleResponse<AuditEvent[]>(res)
}
