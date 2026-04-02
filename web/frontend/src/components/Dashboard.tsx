import { useState } from 'react'
import type { User } from '../types'
import GamesList from './GamesList'

interface DashboardProps {
  user: User
}

export default function Dashboard({ user }: DashboardProps) {
  const [logoutError, setLogoutError] = useState(false)

  const handleLogout = async () => {
    setLogoutError(false)
    const res = await fetch('/api/auth/logout', { method: 'POST' })
    if (!res.ok) {
      setLogoutError(true)
      return
    }
    window.location.reload()
  }

  const displayName = [user.first_name, user.last_name].filter(Boolean).join(' ')

  return (
    <div className="dashboard">
      <header className="dashboard-header">
        <span className="dashboard-title">Squash Bot</span>
        <div className="dashboard-header__actions">
          {logoutError && (
            <span className="logout-error">Sign out failed — try again</span>
          )}
          <button onClick={handleLogout} className="logout-button">Sign out</button>
        </div>
      </header>

      <main className="dashboard-main">
        <div className="user-profile">
          {user.photo_url && (
            <img src={user.photo_url} alt={displayName} className="avatar" />
          )}
          <div className="user-profile__info">
            <p className="user-profile__name">{displayName}</p>
            {user.username && <p className="username">@{user.username}</p>}
          </div>
        </div>

        <section className="games-section">
          <h2 className="games-section__title">My Games</h2>
          <GamesList />
        </section>
      </main>
    </div>
  )
}
