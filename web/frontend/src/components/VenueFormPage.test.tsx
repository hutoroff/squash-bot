import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import VenueFormPage from './VenueFormPage'
import type { BotGroup, User, Venue } from '../types'
import { ApiError } from '../api/http'
import * as groupsApi from '../api/groups'
import * as venuesApi from '../api/venues'

// ── mocks ────────────────────────────────────────────────────────────────────

vi.mock('../api/groups', () => ({ fetchGroup: vi.fn() }))

vi.mock('../api/venues', () => ({
  fetchVenue: vi.fn(),
  createVenue: vi.fn(),
  updateVenue: vi.fn(),
  fetchBookingReadiness: vi.fn(),
  fetchCredentials: vi.fn(),
  fetchCredentialPriorities: vi.fn(),
  addCredential: vi.fn(),
  deleteCredential: vi.fn(),
}))

// ── fixtures ─────────────────────────────────────────────────────────────────

const user: User = { user_id: 42, first_name: 'Alice' }

function makeGroup(overrides: Partial<BotGroup> = {}): BotGroup {
  return {
    chat_id: -100123,
    title: 'Tuesday Squash',
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

function makeVenue(overrides: Partial<Venue> = {}): Venue {
  return {
    id: 7,
    group_id: -100123,
    name: 'SquashPoint',
    address: 'Main St 1',
    courts: '1,2,3',
    time_slots: '18:00,19:00',
    game_days: '2',
    grace_period_hours: 24,
    booking_opens_days: 14,
    preventive_cancellation_fraction: '1/2',
    auto_booking_enabled: true,
    preferred_game_times: '19:00',
    auto_booking_courts: '2,1',
    auto_booking_courts_count: 2,
    created_at: '2026-01-15T10:00:00Z',
    ...overrides,
  }
}

function renderNew() {
  return render(
    <MemoryRouter initialEntries={['/groups/-100123/venues/new']}>
      <Routes>
        <Route path="/groups/:chatId/venues/new" element={<VenueFormPage user={user} />} />
      </Routes>
    </MemoryRouter>
  )
}

function renderEdit() {
  return render(
    <MemoryRouter initialEntries={['/groups/-100123/venues/7']}>
      <Routes>
        <Route path="/groups/:chatId/venues/:venueId" element={<VenueFormPage user={user} />} />
        <Route path="/groups/:chatId" element={<div>group page</div>} />
      </Routes>
    </MemoryRouter>
  )
}

// ── setup ────────────────────────────────────────────────────────────────────

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(groupsApi.fetchGroup).mockResolvedValue(makeGroup())
  vi.mocked(venuesApi.fetchVenue).mockResolvedValue(makeVenue())
  vi.mocked(venuesApi.fetchCredentials).mockResolvedValue([])
  vi.mocked(venuesApi.fetchCredentialPriorities).mockResolvedValue([1, 2])
  vi.mocked(venuesApi.fetchBookingReadiness).mockResolvedValue({ ready: true, max_courts: 3, reason: '' })
})

// ── tests ────────────────────────────────────────────────────────────────────

describe('VenueFormPage — new venue', () => {
  it('has no credentials section before the venue exists', async () => {
    renderNew()

    await waitFor(() => expect(screen.getByText('New venue')).toBeInTheDocument())
    expect(screen.queryByText('Booking credentials')).not.toBeInTheDocument()
    expect(venuesApi.fetchCredentials).not.toHaveBeenCalled()
  })

  it('offers preferred times only once time slots exist', async () => {
    renderNew()

    await waitFor(() => expect(screen.getByText('Add time slots above first.')).toBeInTheDocument())
  })

  it('refuses to submit auto-booking with zero courts per game', async () => {
    renderNew()

    await waitFor(() => expect(screen.getByLabelText('Name')).toBeInTheDocument())
    await userEvent.type(screen.getByLabelText('Name'), 'SquashPoint')
    await userEvent.type(screen.getByPlaceholderText('Court number'), '1')
    await userEvent.click(screen.getAllByRole('button', { name: 'Add' })[0])
    await userEvent.click(screen.getByLabelText(/Book courts automatically/))

    const countInput = screen.getByLabelText('Courts per game')
    expect(countInput).toHaveAttribute('min', '1')

    await userEvent.clear(countInput)
    await userEvent.type(countInput, '0')
    await userEvent.click(screen.getByRole('button', { name: 'Save venue' }))

    // min=1 blocks the submit; nothing reaches the API that would silently
    // disable booking server-side.
    expect(venuesApi.createVenue).not.toHaveBeenCalled()
  })

  it('requires a name and at least one court', async () => {
    renderNew()

    await waitFor(() => expect(screen.getByLabelText('Name')).toBeInTheDocument())
    // Name is filled so the browser lets the submit through to our own checks.
    await userEvent.type(screen.getByLabelText('Name'), 'SquashPoint')
    await userEvent.click(screen.getByRole('button', { name: 'Save venue' }))

    expect(await screen.findByText(/at least one court are required/)).toBeInTheDocument()
    expect(venuesApi.createVenue).not.toHaveBeenCalled()
  })

  it('disables auto-booking with an explanation when the owner blocked it', async () => {
    vi.mocked(groupsApi.fetchGroup).mockResolvedValue(makeGroup({ auto_booking_allowed: false }))
    renderNew()

    await waitFor(() =>
      expect(screen.getByText(/server owner has blocked auto-booking/)).toBeInTheDocument())
    expect(screen.getByLabelText(/Book courts automatically/)).toBeDisabled()
  })

  it('shows the fraction selector only with auto-booking and submits its value', async () => {
    vi.mocked(venuesApi.createVenue).mockResolvedValue(makeVenue())
    renderNew()

    await userEvent.type(await screen.findByLabelText('Name'), 'SquashPoint')
    expect(screen.queryByLabelText('Preventive cancellation')).not.toBeInTheDocument()
    await userEvent.click(screen.getByLabelText(/Book courts automatically/))
    const fraction = screen.getByLabelText('Preventive cancellation')
    expect(fraction).toHaveValue('1/2')
    expect(screen.getAllByRole('option')).toHaveLength(3)
    await userEvent.selectOptions(fraction, '1/3')
    await userEvent.type(screen.getByPlaceholderText('Court number'), '1')
    await userEvent.click(screen.getAllByRole('button', { name: 'Add' })[0])
    await userEvent.click(screen.getByRole('button', { name: 'Save venue' }))

    await waitFor(() => expect(venuesApi.createVenue).toHaveBeenCalled())
    expect(vi.mocked(venuesApi.createVenue).mock.calls[0][1].preventive_cancellation_fraction).toBe('1/3')
  })
})

describe('VenueFormPage — edit venue', () => {
  it('loads the venue into the structured editors', async () => {
    vi.mocked(venuesApi.fetchVenue).mockResolvedValue(makeVenue({ preventive_cancellation_fraction: '2/3' }))
    renderEdit()

    await waitFor(() => expect(screen.getByLabelText('Name')).toHaveValue('SquashPoint'))
    expect(screen.getByRole('button', { name: 'Tue' })).toHaveAttribute('aria-pressed', 'true')
    expect(screen.getByRole('button', { name: 'Mon' })).toHaveAttribute('aria-pressed', 'false')
    // Court priority keeps the stored order, not the court list order.
    expect(screen.getByLabelText('Move court 2 up')).toBeInTheDocument()
    expect(screen.getByLabelText('Preventive cancellation')).toHaveValue('2/3')
  })

  it('only offers preferred times from the venue time slots', async () => {
    renderEdit()

    await waitFor(() => expect(screen.getByRole('button', { name: '19:00' })).toHaveAttribute('aria-pressed', 'true'))
    expect(screen.getByRole('button', { name: '18:00' })).toHaveAttribute('aria-pressed', 'false')
    expect(screen.queryByRole('button', { name: '20:00' })).not.toBeInTheDocument()
  })

  it('drops a preferred time when its slot is removed', async () => {
    vi.mocked(venuesApi.updateVenue).mockResolvedValue(makeVenue())
    renderEdit()

    await waitFor(() => expect(screen.getByLabelText('Remove 19:00')).toBeInTheDocument())
    await userEvent.click(screen.getByLabelText('Remove 19:00'))
    await userEvent.click(screen.getByRole('button', { name: 'Save venue' }))

    await waitFor(() => expect(venuesApi.updateVenue).toHaveBeenCalled())
    const sent = vi.mocked(venuesApi.updateVenue).mock.calls[0][2]
    expect(sent.time_slots).toBe('18:00')
    expect(sent.preferred_game_times).toBe('')
  })

  it('shows a single banner when credential storage is off', async () => {
    vi.mocked(venuesApi.fetchCredentials).mockRejectedValue(new ApiError(503, 'disabled'))
    renderEdit()

    await waitFor(() =>
      expect(screen.getByText(/Credential storage is not configured/)).toBeInTheDocument())
    expect(screen.queryByLabelText('Login')).not.toBeInTheDocument()
  })

  it('prefills the next free credential priority', async () => {
    renderEdit()

    await waitFor(() => expect(screen.getByLabelText('Priority')).toHaveValue(3))
  })

  it('reports a duplicate credential login', async () => {
    vi.mocked(venuesApi.addCredential).mockRejectedValue(new ApiError(409, 'duplicate'))
    renderEdit()

    await userEvent.type(await screen.findByLabelText('Login'), 'a@b.c')
    await userEvent.type(screen.getByLabelText('Password'), 'secret')
    await userEvent.click(screen.getByRole('button', { name: 'Add credential' }))

    expect(await screen.findByText(/already exists for this venue/)).toBeInTheDocument()
  })

  it('translates the owner-blocked error on save', async () => {
    vi.mocked(venuesApi.updateVenue).mockRejectedValue(
      new ApiError(400, 'auto_booking_disallowed_by_owner'))
    renderEdit()

    await waitFor(() => expect(screen.getByLabelText('Name')).toHaveValue('SquashPoint'))
    await userEvent.click(screen.getByRole('button', { name: 'Save venue' }))

    expect(await screen.findByText(/server owner has blocked auto-booking/)).toBeInTheDocument()
  })
})
