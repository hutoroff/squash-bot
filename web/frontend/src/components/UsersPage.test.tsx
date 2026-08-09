import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import UsersPage from './UsersPage'
import type { User, AdminUser } from '../types'
import * as usersApi from '../api/users'

// ── mocks ────────────────────────────────────────────────────────────────────

vi.mock('../api/users', () => ({
  fetchUsers: vi.fn(),
  setServerOwner: vi.fn(),
}))

vi.mock('../api/http', () => ({
  ApiError: class ApiError extends Error {
    status: number
    constructor(status: number, message: string) {
      super(message)
      this.name = 'ApiError'
      this.status = status
    }
  },
  handleResponse: vi.fn(),
}))

const mockFetch = vi.mocked(usersApi.fetchUsers)
const mockSetOwner = vi.mocked(usersApi.setServerOwner)

// ── fixtures ─────────────────────────────────────────────────────────────────

function makeUser(overrides: Partial<User> = {}): User {
  return { user_id: 1, first_name: 'Alice', is_server_owner: true, ...overrides }
}

function makeAdminUser(overrides: Partial<AdminUser> = {}): AdminUser {
  return {
    user_id: 1,
    display_name: '@alice',
    is_server_owner: true,
    dm_language: 'en',
    results_opt_out: false,
    created_at: '2026-01-15T10:00:00Z',
    providers: ['telegram'],
    ...overrides,
  }
}

// ── setup ────────────────────────────────────────────────────────────────────

beforeEach(() => {
  vi.clearAllMocks()
})

// ── tests ────────────────────────────────────────────────────────────────────

describe('UsersPage', () => {
  it('renders loading then users', async () => {
    mockFetch.mockResolvedValue([makeAdminUser()])
    render(<UsersPage user={makeUser()} />)

    expect(screen.getByText(/Loading/)).toBeInTheDocument()

    await waitFor(() => {
      expect(screen.getByText('@alice')).toBeInTheDocument()
    })
    expect(screen.getByText('telegram')).toBeInTheDocument()
  })

  it('renders empty state when no users', async () => {
    mockFetch.mockResolvedValue([])
    render(<UsersPage user={makeUser()} />)

    await waitFor(() => {
      expect(screen.getByText('No users yet.')).toBeInTheDocument()
    })
  })

  it('renders error on fetch failure', async () => {
    mockFetch.mockRejectedValue(new Error('Network error'))
    render(<UsersPage user={makeUser()} />)

    await waitFor(() => {
      expect(screen.getByText(/Network error/)).toBeInTheDocument()
    })
  })

  it('reloads on 401', async () => {
    const { ApiError } = await import('../api/http')
    const reloadMock = vi.fn()
    Object.defineProperty(window, 'location', {
      value: { reload: reloadMock },
      writable: true,
    })
    mockFetch.mockRejectedValue(new ApiError(401, 'Not authenticated'))
    render(<UsersPage user={makeUser()} />)

    await waitFor(() => {
      expect(reloadMock).toHaveBeenCalled()
    })
  })

  it('toggles server owner off and calls setServerOwner', async () => {
    mockFetch.mockResolvedValue([makeAdminUser({ user_id: 2, is_server_owner: true })])
    mockSetOwner.mockResolvedValue(undefined)
    render(<UsersPage user={makeUser()} />)

    const checkbox = await screen.findByRole('checkbox')
    expect(checkbox).toBeChecked()
    checkbox.click()

    await waitFor(() => {
      expect(mockSetOwner).toHaveBeenCalledWith(2, false)
    })
  })

  it('rolls back and shows a specific message on 409 (last owner)', async () => {
    const { ApiError } = await import('../api/http')
    mockFetch.mockResolvedValue([makeAdminUser({ user_id: 2, is_server_owner: true })])
    mockSetOwner.mockRejectedValue(new ApiError(409, 'cannot revoke the last server owner'))
    render(<UsersPage user={makeUser()} />)

    const checkbox = await screen.findByRole('checkbox')
    checkbox.click()

    await waitFor(() => {
      expect(screen.getByText(/Cannot remove the last server owner/)).toBeInTheDocument()
    })
    expect(checkbox).toBeChecked() // rolled back
  })
})
