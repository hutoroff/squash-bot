import type { Game } from '../types'

export class ApiError extends Error {
  constructor(public status: number, message: string) {
    super(message)
    this.name = 'ApiError'
  }
}

export async function fetchMyGames(): Promise<Game[]> {
  const res = await fetch('/api/games')
  if (res.status === 401) {
    throw new ApiError(401, 'Not authenticated')
  }
  if (!res.ok) {
    throw new ApiError(res.status, `Failed to load games (${res.status})`)
  }
  return res.json() as Promise<Game[]>
}
