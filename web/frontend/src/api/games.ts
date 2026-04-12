import type { Game, GameParticipants } from '../types'

export class ApiError extends Error {
  constructor(public status: number, message: string) {
    super(message)
    this.name = 'ApiError'
  }
}

async function handleResponse<T>(res: Response): Promise<T> {
  if (res.status === 401) throw new ApiError(401, 'Not authenticated')
  if (!res.ok) throw new ApiError(res.status, `Request failed (${res.status})`)
  return res.json() as Promise<T>
}

export async function fetchMyGames(): Promise<Game[]> {
  const res = await fetch('/api/games')
  return handleResponse<Game[]>(res)
}

export async function fetchGameParticipants(gameId: number): Promise<GameParticipants> {
  const res = await fetch(`/api/games/${gameId}/participants`)
  return handleResponse<GameParticipants>(res)
}

export async function joinGame(gameId: number): Promise<GameParticipants> {
  const res = await fetch(`/api/games/${gameId}/join`, { method: 'POST' })
  return handleResponse<GameParticipants>(res)
}

export async function skipGame(gameId: number): Promise<GameParticipants> {
  const res = await fetch(`/api/games/${gameId}/skip`, { method: 'POST' })
  return handleResponse<GameParticipants>(res)
}

export async function addGuest(gameId: number): Promise<GameParticipants> {
  const res = await fetch(`/api/games/${gameId}/guest`, { method: 'POST' })
  return handleResponse<GameParticipants>(res)
}

export async function removeGuest(gameId: number): Promise<GameParticipants> {
  const res = await fetch(`/api/games/${gameId}/guest`, { method: 'DELETE' })
  return handleResponse<GameParticipants>(res)
}
