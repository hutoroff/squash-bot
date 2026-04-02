import { useState, useEffect } from 'react'
import Login from './components/Login'
import Dashboard from './components/Dashboard'
import type { User } from './types'

function App() {
  // undefined = loading, null = unauthenticated, User = authenticated
  const [user, setUser] = useState<User | null | undefined>(undefined)
  const [botName, setBotName] = useState('')

  useEffect(() => {
    Promise.all([
      fetch('/api/auth/me').then(r => r.ok ? r.json() as Promise<User> : null).catch(() => null),
      fetch('/api/config').then(r => r.json()).catch(() => ({})),
    ]).then(([userData, config]) => {
      setUser(userData)
      setBotName((config as { bot_name?: string }).bot_name ?? '')
    })
  }, [])

  if (user === undefined) {
    return <div className="loading">Loading…</div>
  }

  if (user === null) {
    return <Login botName={botName} />
  }

  return <Dashboard user={user} />
}

export default App
