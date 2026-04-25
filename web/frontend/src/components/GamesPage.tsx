import type { User } from '../types'
import GamesList from './GamesList'

interface GamesPageProps {
  user: User
}

export default function GamesPage({ user }: GamesPageProps) {
  return (
    <div className="games-page">
      <div className="user-profile">
        {user.photo_url && (
          <img src={user.photo_url} alt={[user.first_name, user.last_name].filter(Boolean).join(' ')} className="avatar" />
        )}
        <div className="user-profile__info">
          <p className="user-profile__name">{[user.first_name, user.last_name].filter(Boolean).join(' ')}</p>
          {user.username && <p className="username">@{user.username}</p>}
        </div>
      </div>

      <section className="games-section">
        <h2 className="games-section__title">My Games</h2>
        <GamesList user={user} />
      </section>
    </div>
  )
}
