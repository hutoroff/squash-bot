import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import App from './App'
import type { User } from './types'

// Keep the real auth bootstrap, BrowserRouter, route table and Dashboard. Page
// bodies are isolated here; their API/mutation behavior has component tests.
vi.mock('./components/GamesPage', () => ({ default: () => <h1>Games content</h1> }))
vi.mock('./components/GroupsPage', () => ({ default: () => <h1>Groups content</h1> }))
vi.mock('./components/GroupSettingsPage', () => ({ default: () => <h1>Group settings content</h1> }))
vi.mock('./components/SettingsPage', () => ({ default: () => <h1>Settings content</h1> }))
vi.mock('./components/AuditPage', () => ({ default: () => <h1>Audit content</h1> }))
vi.mock('./components/UsersPage', () => ({ default: () => <h1>Users content</h1> }))
vi.mock('./components/VenueFormPage', async () => {
  const { useParams } = await import('react-router-dom')
  return {
    default: ({ user }: { user: User }) => {
      const { chatId, venueId } = useParams()
      return <h1>Venue {chatId}/{venueId} for user {user.user_id}</h1>
    },
  }
})

const user: User = { user_id: 42, first_name: 'Alice', is_server_owner: false }
const fetchMock = vi.fn()

function session(status: number, currentUser: User = user) {
  fetchMock.mockImplementation(async (url: string) => {
    // No Telegram widget is injected, and all requests remain synthetic/local.
    if (url === '/api/config') return { ok: true, json: async () => ({ bot_name: '' }) }
    if (url === '/api/auth/me') return { ok: status === 200, status, json: async () => currentUser }
    throw new Error(`Unexpected fetch: ${url}`)
  })
}

beforeEach(() => {
  window.history.replaceState(null, '', '/')
  fetchMock.mockReset()
  vi.stubGlobal('fetch', fetchMock)
  session(200)
})

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('App authentication and routing compatibility', () => {
  it('navigates nested dashboard pages and updates active links without a new auth request', async () => {
    const events = userEvent.setup()
    render(<App />)
    await screen.findByRole('heading', { name: 'Games content' })
    expect(screen.queryByRole('link', { name: 'Users' })).not.toBeInTheDocument()

    for (const [link, pathname, heading] of [
      ['Groups', '/groups', 'Groups content'],
      ['My settings', '/settings', 'Settings content'],
      ['Audit log', '/audit', 'Audit content'],
      ['My games', '/', 'Games content'],
    ]) {
      await events.click(screen.getByRole('link', { name: link }))
      await screen.findByRole('heading', { name: heading })
      expect(window.location.pathname).toBe(pathname)
      expect(screen.getByRole('link', { name: link })).toHaveAttribute('aria-current', 'page')
    }
    expect(fetchMock.mock.calls.filter(([url]) => url === '/api/auth/me')).toHaveLength(1)
  })

  it('retains negative group IDs, venue IDs and canonical user identity on a deep link', async () => {
    window.history.replaceState(null, '', '/groups/-100123/venues/7')
    render(<App />)
    await screen.findByRole('heading', { name: 'Venue -100123/7 for user 42' })
    expect(window.location.pathname).toBe('/groups/-100123/venues/7')
  })

  it('mounts the owner-only Users route for an authenticated server owner', async () => {
    session(200, { ...user, is_server_owner: true })
    window.history.replaceState(null, '', '/users')
    render(<App />)
    await screen.findByRole('heading', { name: 'Users content' })
    expect(screen.getByRole('link', { name: 'Users' })).toHaveAttribute('aria-current', 'page')
  })

  it('does not mount authenticated routes when a deep link receives 401', async () => {
    session(401)
    window.history.replaceState(null, '', '/groups/-100123/venues/7')
    render(<App />)
    await screen.findByText(/Sign in with your Telegram account/)
    expect(screen.queryByRole('navigation')).not.toBeInTheDocument()
    expect(screen.queryByText(/Venue -100123/)).not.toBeInTheDocument()
    expect(document.querySelector('script[src*="telegram.org"]')).toBeNull()
  })

  it.each(['upstream', 'network'])('keeps a retry state, not login or protected pages, on %s failure', async failure => {
    session(503)
    if (failure === 'network') {
      fetchMock.mockImplementation(async (url: string) => {
        if (url === '/api/config') return { ok: true, json: async () => ({}) }
        throw new Error('Synthetic network failure')
      })
    }
    render(<App />)
    await waitFor(() => expect(screen.getByRole('button', { name: 'Retry' })).toBeInTheDocument())
    expect(screen.queryByText(/Sign in with your Telegram account/)).not.toBeInTheDocument()
    expect(screen.queryByRole('navigation')).not.toBeInTheDocument()
  })
})
