import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import AuditPage from './AuditPage'
import type { User, AuditEvent } from '../types'
import * as auditApi from '../api/audit'

// ── mocks ─────────────────────────────────────────────────────────────────────

vi.mock('../api/audit', () => ({
  fetchAuditEvents: vi.fn(),
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

const mockFetch = vi.mocked(auditApi.fetchAuditEvents)

// ── fixtures ──────────────────────────────────────────────────────────────────

function makeUser(overrides: Partial<User> = {}): User {
  return { telegram_id: 42, first_name: 'Alice', ...overrides }
}

function makeEvent(id: number, overrides: Partial<AuditEvent> = {}): AuditEvent {
  return {
    id,
    occurred_at: '2026-04-01T10:00:00Z',
    event_type: 'participation.joined',
    visibility: 'player',
    actor_kind: 'user',
    actor_tg_id: 42,
    actor_display: 'Alice',
    subject_type: 'game',
    subject_id: '1',
    description: 'Alice joined the game',
    ...overrides,
  }
}

function renderAuditPage(user: User) {
  return render(
    <MemoryRouter>
      <AuditPage user={user} />
    </MemoryRouter>
  )
}

// ── setup ─────────────────────────────────────────────────────────────────────

beforeEach(() => {
  vi.clearAllMocks()
})

// ── tests ─────────────────────────────────────────────────────────────────────

describe('AuditPage', () => {
  it('loads events on mount', async () => {
    mockFetch.mockResolvedValue([makeEvent(1)])
    renderAuditPage(makeUser())
    await waitFor(() => {
      expect(screen.getByText('Alice joined the game')).toBeInTheDocument()
    })
  })

  it('shows empty state when no events', async () => {
    mockFetch.mockResolvedValue([])
    renderAuditPage(makeUser())
    await waitFor(() => {
      expect(screen.getByText(/No events match/)).toBeInTheDocument()
    })
  })

  it('shows loading indicator initially', () => {
    mockFetch.mockReturnValue(new Promise(() => {}))
    renderAuditPage(makeUser())
    expect(screen.getByText(/Loading/)).toBeInTheDocument()
  })

  it('shows error message on fetch failure', async () => {
    mockFetch.mockRejectedValue(new Error('Network error'))
    renderAuditPage(makeUser())
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
    renderAuditPage(makeUser())
    await waitFor(() => {
      expect(reloadMock).toHaveBeenCalled()
    })
  })

  it('hides group_id and actor_tg_id filters for regular users', () => {
    mockFetch.mockReturnValue(new Promise(() => {}))
    renderAuditPage(makeUser())
    expect(screen.queryByLabelText(/Group ID/)).not.toBeInTheDocument()
    expect(screen.queryByLabelText(/Actor TG ID/)).not.toBeInTheDocument()
  })

  it('shows group_id and actor_tg_id filters for server owners', () => {
    mockFetch.mockReturnValue(new Promise(() => {}))
    renderAuditPage(makeUser({ is_server_owner: true }))
    expect(screen.getByLabelText(/Group ID/)).toBeInTheDocument()
    expect(screen.getByLabelText(/Actor TG ID/)).toBeInTheDocument()
  })

  it('shows Load more button when exactly 50 events returned', async () => {
    const events = Array.from({ length: 50 }, (_, i) => makeEvent(i + 1))
    mockFetch.mockResolvedValue(events)
    renderAuditPage(makeUser())
    await waitFor(() => {
      expect(screen.getByRole('button', { name: /Load more/ })).toBeInTheDocument()
    })
  })

  it('does not show Load more when fewer than 50 events returned', async () => {
    mockFetch.mockResolvedValue([makeEvent(1), makeEvent(2)])
    renderAuditPage(makeUser())
    await waitFor(() => {
      expect(screen.queryByRole('button', { name: /Load more/ })).not.toBeInTheDocument()
    })
  })

  it('Load more passes before_id of last visible event', async () => {
    const events = Array.from({ length: 50 }, (_, i) => makeEvent(i + 1))
    mockFetch.mockResolvedValueOnce(events).mockResolvedValueOnce([makeEvent(51)])
    renderAuditPage(makeUser())

    const loadMore = await screen.findByRole('button', { name: /Load more/ })
    fireEvent.click(loadMore)

    await waitFor(() => {
      expect(mockFetch).toHaveBeenCalledTimes(2)
      const secondCall = mockFetch.mock.calls[1][0]
      expect(secondCall?.before_id).toBe(50) // last event id from first page
    })
  })

  it('applies event_type filter on apply', async () => {
    mockFetch.mockResolvedValue([])
    renderAuditPage(makeUser())
    await waitFor(() => expect(mockFetch).toHaveBeenCalledTimes(1))

    const select = screen.getByRole('combobox')
    fireEvent.change(select, { target: { value: 'game.created' } })
    fireEvent.click(screen.getByRole('button', { name: /Apply filters/ }))

    await waitFor(() => {
      expect(mockFetch).toHaveBeenCalledTimes(2)
      expect(mockFetch.mock.calls[1][0]).toMatchObject({ event_type: 'game.created' })
    })
  })
})
