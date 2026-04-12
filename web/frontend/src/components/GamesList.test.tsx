import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import GamesList from './GamesList'
import type { Game, User } from '../types'
import * as api from '../api/games'

// ── mocks ─────────────────────────────────────────────────────────────────────

vi.mock('../api/games', () => ({
  fetchMyGames: vi.fn(),
  // fetchGameParticipants is called by each GameCard that mounts
  fetchGameParticipants: vi.fn(),
  ApiError: class ApiError extends Error {
    status: number
    constructor(status: number, message: string) {
      super(message)
      this.name = 'ApiError'
      this.status = status
    }
  },
}))

const mockFetchMyGames = vi.mocked(api.fetchMyGames)
const mockFetchGameParticipants = vi.mocked(api.fetchGameParticipants)

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

const testUser: User = { telegram_id: 42, first_name: 'Alice' }

// ── setup ─────────────────────────────────────────────────────────────────────

beforeEach(() => {
  vi.clearAllMocks()
  mockFetchGameParticipants.mockResolvedValue({ participations: [], guests: [] })
})

// ── tests ─────────────────────────────────────────────────────────────────────

describe('GamesList', () => {
  describe('loading and error states', () => {
    it('shows loading while fetchMyGames is pending', () => {
      mockFetchMyGames.mockReturnValue(new Promise(() => {}))
      render(<GamesList user={testUser} />)
      expect(screen.getByText(/Loading games/)).toBeInTheDocument()
    })

    it('shows error message when fetchMyGames rejects', async () => {
      mockFetchMyGames.mockRejectedValue(new Error('network'))
      render(<GamesList user={testUser} />)
      expect(await screen.findByText(/Failed to load games/)).toBeInTheDocument()
    })

    it('shows empty message when there are no games', async () => {
      mockFetchMyGames.mockResolvedValue([])
      render(<GamesList user={testUser} />)
      expect(await screen.findByText(/No games yet/)).toBeInTheDocument()
    })
  })

  describe('upcoming section', () => {
    it('renders the Upcoming section header', async () => {
      mockFetchMyGames.mockResolvedValue([makeGame({ completed: false })])
      render(<GamesList user={testUser} />)
      expect(await screen.findByRole('heading', { name: 'Upcoming' })).toBeInTheDocument()
    })

    it('renders GameCard for each upcoming game', async () => {
      mockFetchMyGames.mockResolvedValue([
        makeGame({ id: 1, completed: false, group_title: 'Club A' }),
        makeGame({ id: 2, completed: false, group_title: 'Club B' }),
      ])
      render(<GamesList user={testUser} />)
      expect(await screen.findByText('Club A')).toBeInTheDocument()
      expect(await screen.findByText('Club B')).toBeInTheDocument()
    })

    it('does not show Upcoming section when there are no upcoming games', async () => {
      mockFetchMyGames.mockResolvedValue([makeGame({ completed: true })])
      render(<GamesList user={testUser} />)
      // Wait for the component to finish loading
      await screen.findByRole('button', { name: /Past/ })
      expect(screen.queryByText('Upcoming')).not.toBeInTheDocument()
    })
  })

  describe('past section — collapsed by default', () => {
    it('shows a toggle button with the game count', async () => {
      mockFetchMyGames.mockResolvedValue([
        makeGame({ id: 1, completed: true }),
        makeGame({ id: 2, completed: true }),
      ])
      render(<GamesList user={testUser} />)
      expect(await screen.findByRole('button', { name: /Past \(2\)/ })).toBeInTheDocument()
    })

    it('does not mount GameCards when collapsed (no participant fetches)', async () => {
      mockFetchMyGames.mockResolvedValue([makeGame({ completed: true })])
      render(<GamesList user={testUser} />)
      await screen.findByRole('button', { name: /Past/ })
      expect(mockFetchGameParticipants).not.toHaveBeenCalled()
    })

    it('does not show past game content when collapsed', async () => {
      mockFetchMyGames.mockResolvedValue([
        makeGame({ id: 1, completed: true, group_title: 'Past Club' }),
      ])
      render(<GamesList user={testUser} />)
      await screen.findByRole('button', { name: /Past/ })
      expect(screen.queryByText('Past Club')).not.toBeInTheDocument()
    })
  })

  describe('past section — expanded', () => {
    it('reveals game cards after clicking the toggle', async () => {
      const ue = userEvent.setup()
      mockFetchMyGames.mockResolvedValue([
        makeGame({ id: 1, completed: true, group_title: 'Past Club' }),
      ])
      render(<GamesList user={testUser} />)
      await ue.click(await screen.findByRole('button', { name: /Past/ }))
      expect(await screen.findByText('Past Club')).toBeInTheDocument()
    })

    it('mounts GameCards on expand — participant fetches fire', async () => {
      const ue = userEvent.setup()
      mockFetchMyGames.mockResolvedValue([makeGame({ id: 7, completed: true })])
      render(<GamesList user={testUser} />)
      await ue.click(await screen.findByRole('button', { name: /Past/ }))
      await screen.findByText('Squash Club')
      expect(mockFetchGameParticipants).toHaveBeenCalledWith(7)
    })

    it('collapses again on a second click', async () => {
      const ue = userEvent.setup()
      mockFetchMyGames.mockResolvedValue([
        makeGame({ id: 1, completed: true, group_title: 'Past Club' }),
      ])
      render(<GamesList user={testUser} />)
      const toggle = await screen.findByRole('button', { name: /Past/ })
      await ue.click(toggle)
      expect(await screen.findByText('Past Club')).toBeInTheDocument()
      await ue.click(toggle)
      expect(screen.queryByText('Past Club')).not.toBeInTheDocument()
    })
  })

  describe('mixed upcoming and past', () => {
    it('renders both sections when both kinds of game exist', async () => {
      mockFetchMyGames.mockResolvedValue([
        makeGame({ id: 1, completed: false }),
        makeGame({ id: 2, completed: true }),
      ])
      render(<GamesList user={testUser} />)
      expect(await screen.findByRole('heading', { name: 'Upcoming' })).toBeInTheDocument()
      expect(await screen.findByRole('button', { name: /Past \(1\)/ })).toBeInTheDocument()
    })
  })
})
