import { useState, useEffect } from 'react'
import type { Game, User, GameParticipants, GamePlayer, ParticipationStatus } from '../types'
import Badge from './Badge'
import {
  fetchGameParticipants,
  joinGame,
  skipGame,
  addGuest,
  removeGuest,
  ApiError,
} from '../api/games'

interface GameCardProps {
  game: Game
  user: User
}

function formatGameDate(dateStr: string, timezone: string) {
  const opts = { timeZone: timezone } as const
  const date = new Date(dateStr)
  return {
    weekday: date.toLocaleDateString(undefined, { ...opts, weekday: 'short' }),
    date: date.toLocaleDateString(undefined, { ...opts, month: 'short', day: 'numeric' }),
    time: date.toLocaleTimeString(undefined, { ...opts, hour: '2-digit', minute: '2-digit', hour12: false }),
  }
}

function playerDisplayName(player: GamePlayer): string {
  if (player.username) return `@${player.username}`
  const name = [player.first_name, player.last_name].filter(Boolean).join(' ')
  return name || 'Unknown'
}

export default function GameCard({ game, user }: GameCardProps) {
  const [participationStatus, setParticipationStatus] = useState<ParticipationStatus | null>(
    game.participation_status
  )
  const [participants, setParticipants] = useState<GameParticipants | null>(null)
  const [actionLoading, setActionLoading] = useState(false)

  const { weekday, date, time } = formatGameDate(game.game_date, game.timezone)
  const capacity = game.courts_count * 2
  const courts = game.courts.split(',').map(c => c.trim()).filter(Boolean)

  useEffect(() => {
    fetchGameParticipants(game.id)
      .then(setParticipants)
      .catch(() => {
        // Silent fail — fall back to participant_count from game data
      })
  }, [game.id])

  const registered = participants?.participations.filter(p => p.status === 'registered') ?? []
  const guests = participants?.guests ?? []
  const totalCount = participants ? registered.length + guests.length : game.participant_count

  const myGuest = guests.find(g => g.invited_by.telegram_id === user.telegram_id)

  async function handleAction(action: () => Promise<GameParticipants>) {
    setActionLoading(true)
    try {
      const updated = await action()
      setParticipants(updated)
      const myPart = updated.participations.find(p => p.player.telegram_id === user.telegram_id)
      setParticipationStatus(myPart?.status ?? null)
    } catch (err) {
      if (err instanceof ApiError && err.status === 401) {
        window.location.reload()
      }
      // Other errors: silently ignore; UI stays as-is
    } finally {
      setActionLoading(false)
    }
  }

  const isRegistered = participationStatus === 'registered'

  return (
    <div className={`game-card${game.completed ? ' game-card--completed' : ''}`}>
      {/* Header */}
      <div className="game-card__header">
        <div className="game-card__datetime">
          <span className="game-card__weekday">{weekday}</span>
          <span className="game-card__sep">·</span>
          <span className="game-card__date">{date}</span>
          <span className="game-card__sep">·</span>
          <span className="game-card__time">{time}</span>
        </div>
        {game.completed
          ? <Badge variant="muted">Completed</Badge>
          : <Badge variant="info">Upcoming</Badge>
        }
      </div>

      {/* Venue */}
      {game.venue_name && (
        <div className="game-card__venue">
          <span className="game-card__venue-icon">📍</span>
          <div className="game-card__venue-details">
            <span className="game-card__venue-name">{game.venue_name}</span>
            {game.venue_address && (
              <span className="game-card__venue-address">{game.venue_address}</span>
            )}
          </div>
        </div>
      )}

      {/* Courts */}
      <div className="game-card__courts">
        🎾 {courts.join(' · ')}
      </div>

      {/* Group */}
      <div className="game-card__group">{game.group_title}</div>

      {/* Players */}
      <div className="game-card__players">
        <div className="game-card__players-header">
          Players ({totalCount} / {capacity})
        </div>
        {participants === null ? (
          <div className="game-card__players-loading">Loading…</div>
        ) : registered.length === 0 && guests.length === 0 ? (
          <div className="game-card__players-empty">No players yet</div>
        ) : (
          <ol className="game-card__player-list">
            {registered.map(p => (
              <li key={p.id} className="game-card__player-item">
                {playerDisplayName(p.player)}
              </li>
            ))}
            {guests.map(g => (
              <li key={`g-${g.id}`} className="game-card__player-item game-card__player-item--guest">
                +1{' '}
                <span className="game-card__guest-by">
                  (by {playerDisplayName(g.invited_by)})
                </span>
              </li>
            ))}
          </ol>
        )}
      </div>

      {/* Footer: actions + participation badge */}
      <div className="game-card__footer">
        {!game.completed && (
          <div className="game-card__actions">
            {isRegistered ? (
              <button
                className="game-action-btn game-action-btn--secondary"
                onClick={() => handleAction(() => skipGame(game.id))}
                disabled={actionLoading}
              >
                Skip
              </button>
            ) : (
              <button
                className="game-action-btn game-action-btn--primary"
                onClick={() => handleAction(() => joinGame(game.id))}
                disabled={actionLoading}
              >
                Join
              </button>
            )}
            {myGuest ? (
              <button
                className="game-action-btn game-action-btn--secondary"
                onClick={() => handleAction(() => removeGuest(game.id))}
                disabled={actionLoading}
              >
                −1
              </button>
            ) : (
              <button
                className="game-action-btn game-action-btn--ghost"
                onClick={() => handleAction(() => addGuest(game.id))}
                disabled={actionLoading}
              >
                +1
              </button>
            )}
          </div>
        )}
        <div className="game-card__participation">
          {participationStatus === 'registered' ? (
            <Badge variant="success">{game.completed ? 'Attended' : 'Going'}</Badge>
          ) : participationStatus === 'skipped' ? (
            <Badge variant="muted">{game.completed ? 'Skipped' : 'Not going'}</Badge>
          ) : !game.completed ? (
            <Badge variant="outline">No response</Badge>
          ) : null}
        </div>
      </div>
    </div>
  )
}
