interface User {
  telegram_id: number
  player_id?: number
  first_name: string
  last_name?: string
  username?: string
  photo_url?: string
}

interface DashboardProps {
  user: User
}

export default function Dashboard({ user }: DashboardProps) {
  const handleLogout = async () => {
    await fetch('/api/auth/logout', { method: 'POST' })
    window.location.reload()
  }

  const displayName = [user.first_name, user.last_name].filter(Boolean).join(' ')

  return (
    <div className="dashboard">
      <header className="dashboard-header">
        <span className="dashboard-title">Squash Bot</span>
        <button onClick={handleLogout} className="logout-button">Sign out</button>
      </header>
      <main className="dashboard-main">
        {user.photo_url && (
          <img src={user.photo_url} alt={displayName} className="avatar" />
        )}
        <h2>Welcome, {displayName}!</h2>
        {user.username && <p className="username">@{user.username}</p>}
      </main>
    </div>
  )
}
