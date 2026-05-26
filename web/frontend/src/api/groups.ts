import type { BotGroup } from '../types'
import { handleResponse } from './http'

export async function fetchGroups(): Promise<BotGroup[]> {
  const res = await fetch('/api/groups')
  return handleResponse<BotGroup[]>(res)
}
