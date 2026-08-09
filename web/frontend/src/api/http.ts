export class ApiError extends Error {
  constructor(public status: number, message: string) {
    super(message)
    this.name = 'ApiError'
  }
}

/** Reads the {"error": …} field the Go services return, falling back to the status. */
async function errorFrom(res: Response): Promise<ApiError> {
  if (res.status === 401) return new ApiError(401, 'Not authenticated')
  const body = await res.json().catch(() => null)
  const msg = body && typeof body.error === 'string' ? body.error : `Request failed (${res.status})`
  return new ApiError(res.status, msg)
}

export async function handleResponse<T>(res: Response): Promise<T> {
  if (!res.ok) throw await errorFrom(res)
  return res.json() as Promise<T>
}

/** For endpoints that answer 204 — throws an ApiError carrying the server message. */
export async function expectNoContent(res: Response): Promise<void> {
  if (!res.ok) throw await errorFrom(res)
}
