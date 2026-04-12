import { useState, useEffect } from 'react'
import type { Game, User } from '../types'
import GameCard from './GameCard'
import { fetchMyGames, ApiError } from '../api/games'

interface GamesListProps {
  user: User
}

export default function GamesList({ user }: GamesListProps) {
  const [games, setGames] = useState<Game[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [pastExpanded, setPastExpanded] = useState(false)

  useEffect(() => {
    fetchMyGames()
      .then(setGames)
      .catch((err: unknown) => {
        if (err instanceof ApiError && err.status === 401) {
          // Session expired between page load and games fetch — reload to re-auth.
          window.location.reload()
          return
        }
        setError('Failed to load games. Please try again later.')
      })
  }, [])

  if (error) {
    return <p className="games-list__message games-list__message--error">{error}</p>
  }

  if (games === null) {
    return <p className="games-list__message">Loading games…</p>
  }

  const upcoming = games
    .filter(g => !g.completed)
    .sort((a, b) => new Date(a.game_date).getTime() - new Date(b.game_date).getTime())

  const past = games
    .filter(g => g.completed)
    .sort((a, b) => new Date(b.game_date).getTime() - new Date(a.game_date).getTime())

  if (upcoming.length === 0 && past.length === 0) {
    return (
      <p className="games-list__message">
        No games yet — join a squash group in Telegram to get started.
      </p>
    )
  }

  return (
    <div className="games-list">
      {upcoming.length > 0 && (
        <section className="games-list__section">
          <h3 className="games-list__section-title">Upcoming</h3>
          {upcoming.map(game => <GameCard key={game.id} game={game} user={user} />)}
        </section>
      )}
      {past.length > 0 && (
        <section className="games-list__section">
          <button
            className="games-list__section-toggle"
            onClick={() => setPastExpanded(v => !v)}
            aria-expanded={pastExpanded}
            aria-controls="past-games-section"
          >
            <span className="games-list__section-title">Past ({past.length})</span>
            <span className="games-list__section-chevron" aria-hidden="true">
              {pastExpanded ? '▾' : '▸'}
            </span>
          </button>
          {pastExpanded && (
            <div id="past-games-section">
              {past.map(game => <GameCard key={game.id} game={game} user={user} />)}
            </div>
          )}
        </section>
      )}
    </div>
  )
}
