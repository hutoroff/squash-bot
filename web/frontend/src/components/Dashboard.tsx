import { useState } from 'react'
import { NavLink, Outlet } from 'react-router-dom'
import type { User } from '../types'

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

  return (
    <div className="dashboard">
      <header className="dashboard-header">
        <span className="dashboard-title">Squash Bot</span>
        <nav className="dashboard-nav">
          <NavLink to="/" end className={({ isActive }) => 'dashboard-nav__link' + (isActive ? ' dashboard-nav__link--active' : '')}>
            My games
          </NavLink>
          <NavLink to="/audit" className={({ isActive }) => 'dashboard-nav__link' + (isActive ? ' dashboard-nav__link--active' : '')}>
            Audit log
          </NavLink>
        </nav>
        <div className="dashboard-header__actions">
          {logoutError && (
            <span className="logout-error">Sign out failed — try again</span>
          )}
          <button onClick={handleLogout} className="logout-button">Sign out</button>
        </div>
      </header>

      <main className="dashboard-main">
        <Outlet context={{ user }} />
      </main>
    </div>
  )
}
