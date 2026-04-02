import type { Game } from '../types'
import Badge from './Badge'

interface GameCardProps {
  game: Game
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

export default function GameCard({ game }: GameCardProps) {
  const { weekday, date, time } = formatGameDate(game.game_date, game.timezone)
  const capacity = game.courts_count * 2
  const courts = game.courts.split(',').map(c => c.trim()).filter(Boolean)
  const fillPct = Math.min((game.registered_count / capacity) * 100, 100)

  const participationBadge = () => {
    if (game.participation_status === 'registered') {
      return <Badge variant="success">{game.completed ? 'Attended' : 'Going'}</Badge>
    }
    if (game.participation_status === 'skipped') {
      return <Badge variant="muted">{game.completed ? 'Skipped' : 'Not going'}</Badge>
    }
    if (!game.completed) {
      return <Badge variant="outline">No response</Badge>
    }
    return null
  }

  return (
    <div className={`game-card${game.completed ? ' game-card--completed' : ''}`}>
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

      {game.venue_name && (
        <div className="game-card__venue">
          <span className="game-card__venue-name">{game.venue_name}</span>
          {game.venue_address && (
            <span className="game-card__venue-address">{game.venue_address}</span>
          )}
        </div>
      )}

      <div className="game-card__courts">
        {courts.join(' · ')}
      </div>

      <div className="game-card__footer">
        <div className="game-card__capacity">
          <span className="game-card__capacity-label">{game.registered_count} / {capacity} players</span>
          <div className="game-card__capacity-bar" role="progressbar"
            aria-valuenow={game.registered_count} aria-valuemin={0} aria-valuemax={capacity}>
            <div className="game-card__capacity-fill" style={{ width: `${fillPct}%` }} />
          </div>
        </div>
        <div className="game-card__participation">
          {participationBadge()}
        </div>
      </div>
    </div>
  )
}
