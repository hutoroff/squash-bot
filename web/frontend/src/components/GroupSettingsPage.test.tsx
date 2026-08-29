import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import GroupSettingsPage from './GroupSettingsPage'
import type { BotGroup, User, Venue } from '../types'
import * as groupsApi from '../api/groups'
import * as venuesApi from '../api/venues'

// ── mocks ────────────────────────────────────────────────────────────────────

vi.mock('../api/groups', () => ({
  fetchGroup: vi.fn(),
  updateGroupLanguage: vi.fn(),
  updateGroupTimezone: vi.fn(),
  updateGroupChangelog: vi.fn(),
  updateGroupLeaderboardNotifications: vi.fn(),
  updateGroupAutoBookingAllowed: vi.fn(),
}))

vi.mock('../api/venues', () => ({
  fetchVenues: vi.fn(),
  fetchBookingReadiness: vi.fn(),
  deleteVenue: vi.fn(),
}))

// ── fixtures ─────────────────────────────────────────────────────────────────

function makeUser(overrides: Partial<User> = {}): User {
  return { user_id: 42, first_name: 'Alice', ...overrides }
}

function makeGroup(overrides: Partial<BotGroup> = {}): BotGroup {
  return {
    chat_id: -100123,
    title: 'Tuesday Squash',
    bot_is_admin: true,
    language: 'en',
    timezone: 'UTC',
    changelog_enabled: true,
    leaderboard_notifications_enabled: false,
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
    courts: '1,2,3',
    time_slots: '18:00,19:00',
    game_days: '2',
    grace_period_hours: 24,
    booking_opens_days: 14,
    preventive_cancellation_fraction: '1/2',
    auto_booking_enabled: true,
    preferred_game_times: '19:00',
    auto_booking_courts: '',
    auto_booking_courts_count: 2,
    created_at: '2026-01-15T10:00:00Z',
    ...overrides,
  }
}

function renderPage(user: User) {
  return render(
    <MemoryRouter initialEntries={['/groups/-100123']}>
      <Routes>
        <Route path="/groups/:chatId" element={<GroupSettingsPage user={user} />} />
      </Routes>
    </MemoryRouter>
  )
}

// ── setup ────────────────────────────────────────────────────────────────────

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(groupsApi.fetchGroup).mockResolvedValue(makeGroup())
  vi.mocked(venuesApi.fetchVenues).mockResolvedValue([])
  vi.mocked(venuesApi.fetchBookingReadiness).mockResolvedValue({ ready: true, max_courts: 3, reason: '' })
})

// ── tests ────────────────────────────────────────────────────────────────────

describe('GroupSettingsPage', () => {
  it('renders every settings section', async () => {
    renderPage(makeUser())

    await waitFor(() => expect(screen.getByText('Tuesday Squash')).toBeInTheDocument())
    expect(screen.getByText('General')).toBeInTheDocument()
    expect(screen.getByText('Notifications')).toBeInTheDocument()
    expect(screen.getByText('Auto-booking')).toBeInTheDocument()
    expect(screen.getByText('Venues')).toBeInTheDocument()
  })

  it('shows help text for each field', async () => {
    renderPage(makeUser())

    await waitFor(() => expect(screen.getByText(/Language of every message/)).toBeInTheDocument())
    expect(screen.getByText(/Game times, reminders and daily jobs/)).toBeInTheDocument()
  })

  it('hides the auto-booking master switch from non-owners', async () => {
    renderPage(makeUser({ is_server_owner: false }))

    await waitFor(() => expect(screen.getByText(/Auto-booking is allowed for this group/)).toBeInTheDocument())
    expect(screen.queryByLabelText(/Allow auto-booking/)).not.toBeInTheDocument()
  })

  it('shows the master switch to the server owner', async () => {
    renderPage(makeUser({ is_server_owner: true }))

    await waitFor(() => expect(screen.getByLabelText(/Allow auto-booking/)).toBeInTheDocument())
  })

  it('tells a group admin when the owner blocked auto-booking', async () => {
    vi.mocked(groupsApi.fetchGroup).mockResolvedValue(makeGroup({ auto_booking_allowed: false }))
    renderPage(makeUser({ is_server_owner: false }))

    await waitFor(() => expect(screen.getByText(/blocked for this group by the server owner/)).toBeInTheDocument())
  })

  it('updates a toggle optimistically', async () => {
    vi.mocked(groupsApi.updateGroupLeaderboardNotifications).mockResolvedValue()
    renderPage(makeUser())

    const toggle = await screen.findByLabelText(/Post the leaderboard/)
    await userEvent.click(toggle)

    expect(toggle).toBeChecked()
    await waitFor(() =>
      expect(groupsApi.updateGroupLeaderboardNotifications).toHaveBeenCalledWith(-100123, true))
  })

  it('reverts a toggle when the save fails', async () => {
    vi.mocked(groupsApi.updateGroupChangelog).mockRejectedValue(new Error('upstream unavailable'))
    renderPage(makeUser())

    const toggle = await screen.findByLabelText(/Announce bot updates/)
    expect(toggle).toBeChecked()

    await userEvent.click(toggle)

    await waitFor(() => expect(toggle).toBeChecked())
    expect(screen.getByText('upstream unavailable')).toBeInTheDocument()
  })

  it('renders a venue with its schedule and readiness reason', async () => {
    vi.mocked(venuesApi.fetchVenues).mockResolvedValue([makeVenue()])
    vi.mocked(venuesApi.fetchBookingReadiness).mockResolvedValue({
      ready: false, max_courts: 0, reason: 'no_usable_credentials',
    })
    renderPage(makeUser())

    await waitFor(() => expect(screen.getByText('SquashPoint')).toBeInTheDocument())
    expect(screen.getByText('Tue · 18:00, 19:00')).toBeInTheDocument()
    expect(screen.getByText(/No usable booking credentials/)).toBeInTheDocument()
  })

  it('shows the venue empty state with an add link', async () => {
    renderPage(makeUser())

    await waitFor(() => expect(screen.getByText(/No venues yet/)).toBeInTheDocument())
    expect(screen.getByRole('link', { name: /Add venue/ })).toHaveAttribute(
      'href', '/groups/-100123/venues/new')
  })

  it('surfaces a 409 when a venue cannot be deleted', async () => {
    vi.mocked(venuesApi.fetchVenues).mockResolvedValue([makeVenue()])
    vi.mocked(venuesApi.deleteVenue).mockRejectedValue(
      new Error('venue has active court bookings and cannot be deleted'))
    vi.spyOn(window, 'confirm').mockReturnValue(true)
    renderPage(makeUser())

    await userEvent.click(await screen.findByRole('button', { name: 'Delete' }))

    await waitFor(() => expect(screen.getByText(/active court bookings/)).toBeInTheDocument())
  })
})
