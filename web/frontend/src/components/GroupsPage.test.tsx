import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import GroupsPage from './GroupsPage'
import type { User, BotGroup } from '../types'
import * as groupsApi from '../api/groups'

// ── mocks ────────────────────────────────────────────────────────────────────

vi.mock('../api/groups', () => ({
  fetchGroups: vi.fn(),
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

const mockFetch = vi.mocked(groupsApi.fetchGroups)

// ── fixtures ─────────────────────────────────────────────────────────────────

function makeUser(overrides: Partial<User> = {}): User {
  return { telegram_id: 42, first_name: 'Alice', is_server_owner: true, ...overrides }
}

function makeGroup(overrides: Partial<BotGroup> = {}): BotGroup {
  return {
    chat_id: -100123,
    title: 'Test Group',
    bot_is_admin: true,
    language: 'en',
    timezone: 'UTC',
    changelog_enabled: true,
    leaderboard_notifications_enabled: true,
    auto_booking_allowed: true,
    added_at: '2026-01-15T10:00:00Z',
    ...overrides,
  }
}

function renderGroupsPage(user: User) {
  return render(
    <MemoryRouter>
      <GroupsPage user={user} />
    </MemoryRouter>
  )
}

// ── setup ────────────────────────────────────────────────────────────────────

beforeEach(() => {
  vi.clearAllMocks()
})

// ── tests ────────────────────────────────────────────────────────────────────

describe('GroupsPage', () => {
  it('renders loading then groups', async () => {
    mockFetch.mockResolvedValue([makeGroup()])
    renderGroupsPage(makeUser())

    expect(screen.getByText(/Loading/)).toBeInTheDocument()

    await waitFor(() => {
      expect(screen.getByText('Test Group')).toBeInTheDocument()
    })
    expect(screen.getByText('Admin')).toBeInTheDocument()
  })

  it('renders member badge when bot is not admin', async () => {
    mockFetch.mockResolvedValue([makeGroup({ bot_is_admin: false })])
    renderGroupsPage(makeUser())

    await waitFor(() => {
      expect(screen.getByText('Member')).toBeInTheDocument()
    })
  })

  it('renders empty state when no groups', async () => {
    mockFetch.mockResolvedValue([])
    renderGroupsPage(makeUser())

    await waitFor(() => {
      expect(screen.getByText(/don't administer any groups/)).toBeInTheDocument()
    })
  })

  it('links each group to its settings page', async () => {
    mockFetch.mockResolvedValue([makeGroup()])
    renderGroupsPage(makeUser({ is_server_owner: false }))

    await waitFor(() => {
      expect(screen.getByRole('link', { name: 'Test Group' })).toHaveAttribute('href', '/groups/-100123')
    })
  })

  it('renders error on fetch failure', async () => {
    mockFetch.mockRejectedValue(new Error('Network error'))
    renderGroupsPage(makeUser())

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
    renderGroupsPage(makeUser())

    await waitFor(() => {
      expect(reloadMock).toHaveBeenCalled()
    })
  })
})
