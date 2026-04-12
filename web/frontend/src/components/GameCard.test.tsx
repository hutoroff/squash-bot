import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import GameCard from './GameCard'
import type { Game, User, GameParticipants } from '../types'
import * as api from '../api/games'

// ── mocks ─────────────────────────────────────────────────────────────────────

vi.mock('../api/games', () => ({
  fetchGameParticipants: vi.fn(),
  joinGame: vi.fn(),
  skipGame: vi.fn(),
  addGuest: vi.fn(),
  removeGuest: vi.fn(),
  ApiError: class ApiError extends Error {
    status: number
    constructor(status: number, message: string) {
      super(message)
      this.name = 'ApiError'
      this.status = status
    }
  },
}))

const mockFetch = vi.mocked(api.fetchGameParticipants)
const mockJoin = vi.mocked(api.joinGame)
const mockSkip = vi.mocked(api.skipGame)
const mockAddGuest = vi.mocked(api.addGuest)
const mockRemoveGuest = vi.mocked(api.removeGuest)

// ── fixtures ──────────────────────────────────────────────────────────────────

function makeGame(overrides: Partial<Game> = {}): Game {
  return {
    id: 1,
    game_date: '2026-04-20T10:00:00Z',
    courts_count: 2,
    courts: 'Court 1,Court 2',
    completed: false,
    participation_status: null,
    participant_count: 0,
    group_title: 'Squash Club',
    timezone: 'UTC',
    ...overrides,
  }
}

function makeUser(overrides: Partial<User> = {}): User {
  return { telegram_id: 42, first_name: 'Alice', ...overrides }
}

function makeParticipants(overrides: Partial<GameParticipants> = {}): GameParticipants {
  return { participations: [], guests: [], ...overrides }
}

// ── setup ─────────────────────────────────────────────────────────────────────

beforeEach(() => {
  vi.clearAllMocks()
  mockFetch.mockResolvedValue(makeParticipants())
})

// ── tests ─────────────────────────────────────────────────────────────────────

describe('GameCard', () => {
  describe('player list', () => {
    it('shows loading while participants are fetching', () => {
      mockFetch.mockReturnValue(new Promise(() => {}))
      render(<GameCard game={makeGame()} user={makeUser()} />)
      expect(screen.getByText(/Loading/)).toBeInTheDocument()
    })

    it('renders player names after fetch resolves', async () => {
      mockFetch.mockResolvedValue(makeParticipants({
        participations: [
          { id: 1, player: { telegram_id: 10, username: 'alice' }, status: 'registered' },
          { id: 2, player: { telegram_id: 11, first_name: 'Bob' }, status: 'registered' },
        ],
      }))
      render(<GameCard game={makeGame()} user={makeUser()} />)
      expect(await screen.findByText('@alice')).toBeInTheDocument()
      expect(screen.getByText('Bob')).toBeInTheDocument()
    })

    it('shows empty state when nobody has joined', async () => {
      mockFetch.mockResolvedValue(makeParticipants())
      render(<GameCard game={makeGame()} user={makeUser()} />)
      expect(await screen.findByText('No players yet')).toBeInTheDocument()
    })

    it('shows guests with inviter name', async () => {
      mockFetch.mockResolvedValue(makeParticipants({
        guests: [{ id: 5, invited_by: { telegram_id: 99, username: 'bob' } }],
      }))
      render(<GameCard game={makeGame()} user={makeUser()} />)
      expect(await screen.findByText(/by @bob/)).toBeInTheDocument()
    })

    it('shows game participant_count before fetch resolves', () => {
      mockFetch.mockReturnValue(new Promise(() => {}))
      render(<GameCard game={makeGame({ participant_count: 3, courts_count: 2 })} user={makeUser()} />)
      expect(screen.getByText('Players (3 / 4)')).toBeInTheDocument()
    })

    it('updates count from fetch result', async () => {
      mockFetch.mockResolvedValue(makeParticipants({
        participations: [
          { id: 1, player: { telegram_id: 1 }, status: 'registered' },
          { id: 2, player: { telegram_id: 2 }, status: 'registered' },
        ],
        guests: [{ id: 1, invited_by: { telegram_id: 1 } }],
      }))
      // participant_count=0 in game data; fetch gives 2 registered + 1 guest = 3
      render(<GameCard game={makeGame({ participant_count: 0, courts_count: 2 })} user={makeUser()} />)
      expect(await screen.findByText('Players (3 / 4)')).toBeInTheDocument()
    })

    it('skipped participations are excluded from the player list', async () => {
      mockFetch.mockResolvedValue(makeParticipants({
        participations: [
          { id: 1, player: { telegram_id: 10, username: 'alice' }, status: 'registered' },
          { id: 2, player: { telegram_id: 11, username: 'bob' }, status: 'skipped' },
        ],
      }))
      render(<GameCard game={makeGame()} user={makeUser()} />)
      expect(await screen.findByText('@alice')).toBeInTheDocument()
      expect(screen.queryByText('@bob')).not.toBeInTheDocument()
    })
  })

  describe('participation badge', () => {
    // Badges are driven by participationStatus, which is initialized from
    // game.participation_status — no async fetch needed to check them.

    it('shows "Going" when registered on an upcoming game', () => {
      render(<GameCard game={makeGame({ participation_status: 'registered' })} user={makeUser()} />)
      expect(screen.getByText('Going')).toBeInTheDocument()
    })

    it('shows "Attended" when registered on a completed game', () => {
      render(<GameCard game={makeGame({ participation_status: 'registered', completed: true })} user={makeUser()} />)
      expect(screen.getByText('Attended')).toBeInTheDocument()
    })

    it('shows "Not going" when skipped on an upcoming game', () => {
      render(<GameCard game={makeGame({ participation_status: 'skipped' })} user={makeUser()} />)
      expect(screen.getByText('Not going')).toBeInTheDocument()
    })

    it('shows "Skipped" when skipped on a completed game', () => {
      render(<GameCard game={makeGame({ participation_status: 'skipped', completed: true })} user={makeUser()} />)
      expect(screen.getByText('Skipped')).toBeInTheDocument()
    })

    it('shows "No response" when status is null on an upcoming game', () => {
      render(<GameCard game={makeGame({ participation_status: null })} user={makeUser()} />)
      expect(screen.getByText('No response')).toBeInTheDocument()
    })

    it('shows no participation badge on a completed game with null status', () => {
      render(<GameCard game={makeGame({ participation_status: null, completed: true })} user={makeUser()} />)
      expect(screen.queryByText('No response')).not.toBeInTheDocument()
    })

    it('updates badge after Join action', async () => {
      const ue = userEvent.setup()
      mockJoin.mockResolvedValue(makeParticipants({
        participations: [{ id: 1, player: { telegram_id: 42 }, status: 'registered' }],
      }))
      render(<GameCard game={makeGame({ participation_status: null })} user={makeUser()} />)
      await ue.click(screen.getByRole('button', { name: 'Join' }))
      expect(await screen.findByText('Going')).toBeInTheDocument()
    })

    it('updates badge after Skip action', async () => {
      const ue = userEvent.setup()
      mockSkip.mockResolvedValue(makeParticipants({
        participations: [{ id: 1, player: { telegram_id: 42 }, status: 'skipped' }],
      }))
      render(<GameCard game={makeGame({ participation_status: 'registered' })} user={makeUser()} />)
      await ue.click(screen.getByRole('button', { name: 'Skip' }))
      expect(await screen.findByText('Not going')).toBeInTheDocument()
    })
  })

  describe('action buttons — visibility', () => {
    // Join/Skip visibility depends on participationStatus (sync initial state).
    // +1/-1 visibility depends on whether user has a guest; initially guests=[]
    // so +1 always shows before the fetch. -1 requires fetch to complete.

    it('shows Join when not registered', () => {
      render(<GameCard game={makeGame({ participation_status: null })} user={makeUser()} />)
      expect(screen.getByRole('button', { name: 'Join' })).toBeInTheDocument()
    })

    it('shows Skip when registered', () => {
      render(<GameCard game={makeGame({ participation_status: 'registered' })} user={makeUser()} />)
      expect(screen.getByRole('button', { name: 'Skip' })).toBeInTheDocument()
    })

    it('shows +1 when user has no guest (before fetch completes)', () => {
      mockFetch.mockReturnValue(new Promise(() => {}))
      render(<GameCard game={makeGame()} user={makeUser()} />)
      expect(screen.getByRole('button', { name: '+1' })).toBeInTheDocument()
    })

    it('shows −1 when user has a guest', async () => {
      mockFetch.mockResolvedValue(makeParticipants({
        guests: [{ id: 10, invited_by: { telegram_id: 42 } }],
      }))
      render(<GameCard game={makeGame()} user={makeUser({ telegram_id: 42 })} />)
      expect(await screen.findByRole('button', { name: '−1' })).toBeInTheDocument()
    })

    it('hides all action buttons on completed games', () => {
      render(<GameCard game={makeGame({ completed: true })} user={makeUser()} />)
      expect(screen.queryByRole('button', { name: 'Join' })).not.toBeInTheDocument()
      expect(screen.queryByRole('button', { name: 'Skip' })).not.toBeInTheDocument()
      expect(screen.queryByRole('button', { name: '+1' })).not.toBeInTheDocument()
    })
  })

  describe('action buttons — interactions', () => {
    it('Join calls joinGame and updates state', async () => {
      const ue = userEvent.setup()
      mockJoin.mockResolvedValue(makeParticipants({
        participations: [{ id: 1, player: { telegram_id: 42 }, status: 'registered' }],
      }))
      render(<GameCard game={makeGame({ participation_status: null })} user={makeUser()} />)
      await ue.click(screen.getByRole('button', { name: 'Join' }))
      expect(mockJoin).toHaveBeenCalledWith(1)
      // After join, Skip button replaces Join
      expect(await screen.findByRole('button', { name: 'Skip' })).toBeInTheDocument()
    })

    it('Skip calls skipGame and updates state', async () => {
      const ue = userEvent.setup()
      mockSkip.mockResolvedValue(makeParticipants({
        participations: [{ id: 1, player: { telegram_id: 42 }, status: 'skipped' }],
      }))
      render(<GameCard game={makeGame({ participation_status: 'registered' })} user={makeUser()} />)
      await ue.click(screen.getByRole('button', { name: 'Skip' }))
      expect(mockSkip).toHaveBeenCalledWith(1)
      expect(await screen.findByRole('button', { name: 'Join' })).toBeInTheDocument()
    })

    it('+1 calls addGuest and switches to −1', async () => {
      const ue = userEvent.setup()
      mockAddGuest.mockResolvedValue(makeParticipants({
        guests: [{ id: 10, invited_by: { telegram_id: 42 } }],
      }))
      render(<GameCard game={makeGame()} user={makeUser()} />)
      await ue.click(screen.getByRole('button', { name: '+1' }))
      expect(mockAddGuest).toHaveBeenCalledWith(1)
      expect(await screen.findByRole('button', { name: '−1' })).toBeInTheDocument()
    })

    it('−1 calls removeGuest and switches to +1', async () => {
      const ue = userEvent.setup()
      mockFetch.mockResolvedValue(makeParticipants({
        guests: [{ id: 10, invited_by: { telegram_id: 42 } }],
      }))
      mockRemoveGuest.mockResolvedValue(makeParticipants())
      render(<GameCard game={makeGame()} user={makeUser({ telegram_id: 42 })} />)
      await ue.click(await screen.findByRole('button', { name: '−1' }))
      expect(mockRemoveGuest).toHaveBeenCalledWith(1)
      expect(await screen.findByRole('button', { name: '+1' })).toBeInTheDocument()
    })

    it('disables both buttons while an action is in flight', async () => {
      const ue = userEvent.setup()
      let resolve!: (v: GameParticipants) => void
      mockJoin.mockReturnValue(new Promise(r => { resolve = r }))

      render(<GameCard game={makeGame({ participation_status: null })} user={makeUser()} />)
      await ue.click(screen.getByRole('button', { name: 'Join' }))

      expect(screen.getByRole('button', { name: 'Join' })).toBeDisabled()
      expect(screen.getByRole('button', { name: '+1' })).toBeDisabled()

      resolve(makeParticipants())
      await waitFor(() => expect(screen.getByRole('button', { name: 'Join' })).not.toBeDisabled())
    })
  })

  describe('game metadata', () => {
    it('shows venue name when present', () => {
      render(<GameCard game={makeGame({ venue_name: 'Squash Palace' })} user={makeUser()} />)
      expect(screen.getByText('Squash Palace')).toBeInTheDocument()
    })

    it('shows venue address when present', () => {
      render(<GameCard game={makeGame({ venue_name: 'Squash Palace', venue_address: 'Main St 1' })} user={makeUser()} />)
      expect(screen.getByText('Main St 1')).toBeInTheDocument()
    })

    it('hides venue block when no venue', () => {
      const { container } = render(<GameCard game={makeGame()} user={makeUser()} />)
      expect(container.querySelector('.game-card__venue')).not.toBeInTheDocument()
    })

    it('shows Completed badge on completed games', () => {
      render(<GameCard game={makeGame({ completed: true })} user={makeUser()} />)
      expect(screen.getByText('Completed')).toBeInTheDocument()
    })

    it('shows Upcoming badge on upcoming games', () => {
      render(<GameCard game={makeGame({ completed: false })} user={makeUser()} />)
      expect(screen.getByText('Upcoming')).toBeInTheDocument()
    })

    it('shows courts', () => {
      render(<GameCard game={makeGame({ courts: 'A1,A2' })} user={makeUser()} />)
      expect(screen.getByText(/A1.*A2/)).toBeInTheDocument()
    })

    it('shows group title', () => {
      render(<GameCard game={makeGame({ group_title: 'Monday Squad' })} user={makeUser()} />)
      expect(screen.getByText('Monday Squad')).toBeInTheDocument()
    })
  })
})
