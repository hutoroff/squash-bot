export class ApiError extends Error {
  constructor(public status: number, message: string) {
    super(message)
    this.name = 'ApiError'
  }
}

export async function handleResponse<T>(res: Response): Promise<T> {
  if (res.status === 401) throw new ApiError(401, 'Not authenticated')
  if (!res.ok) throw new ApiError(res.status, `Request failed (${res.status})`)
  return res.json() as Promise<T>
}
