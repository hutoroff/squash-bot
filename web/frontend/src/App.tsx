import { useState, useEffect } from 'react'
import { BrowserRouter, Routes, Route } from 'react-router-dom'
import Login from './components/Login'
import Dashboard from './components/Dashboard'
import GamesPage from './components/GamesPage'
import AuditPage from './components/AuditPage'
import GroupsPage from './components/GroupsPage'
import GroupSettingsPage from './components/GroupSettingsPage'
import VenueFormPage from './components/VenueFormPage'
import SettingsPage from './components/SettingsPage'
import UsersPage from './components/UsersPage'
import type { User } from './types'

function App() {
  // undefined = loading, null = unauthenticated, User = authenticated
  const [user, setUser] = useState<User | null | undefined>(undefined)
  const [botName, setBotName] = useState('')
  const [authError, setAuthError] = useState<string | null>(null)

  useEffect(() => {
    Promise.all([
      fetch('/api/auth/me')
        .then(r => {
          if (r.status === 401) return null // truly unauthenticated — show the login page
          if (!r.ok) throw new Error(`Failed to check session (${r.status})`)
          return r.json() as Promise<User>
        })
        .catch(err => {
          // Transient failure (upstream outage, network error) — do NOT log the
          // user out; show a retry state instead of the login page.
          setAuthError(err instanceof Error ? err.message : 'Failed to check session')
          return undefined
        }),
      fetch('/api/config').then(r => r.json()).catch(() => ({})),
    ]).then(([userData, config]) => {
      if (userData !== undefined) setUser(userData)
      setBotName((config as { bot_name?: string }).bot_name ?? '')
    })
  }, [])

  if (authError) {
    return (
      <div className="loading">
        {authError} — <button onClick={() => window.location.reload()}>Retry</button>
      </div>
    )
  }

  if (user === undefined) {
    return <div className="loading">Loading…</div>
  }

  if (user === null) {
    return <Login botName={botName} />
  }

  return (
    <BrowserRouter>
      <Routes>
        <Route path="/" element={<Dashboard user={user} />}>
          <Route index element={<GamesPage user={user} />} />
          <Route path="audit" element={<AuditPage user={user} />} />
          <Route path="groups" element={<GroupsPage user={user} />} />
          <Route path="groups/:chatId" element={<GroupSettingsPage user={user} />} />
          <Route path="groups/:chatId/venues/new" element={<VenueFormPage user={user} />} />
          <Route path="groups/:chatId/venues/:venueId" element={<VenueFormPage user={user} />} />
          <Route path="settings" element={<SettingsPage user={user} />} />
          {user.is_server_owner && <Route path="users" element={<UsersPage user={user} />} />}
        </Route>
      </Routes>
    </BrowserRouter>
  )
}

export default App
