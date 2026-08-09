import { useState, useEffect } from 'react'
import { Link } from 'react-router-dom'
import type { User, BotGroup } from '../types'
import { fetchGroups } from '../api/groups'
import { ApiError } from '../api/http'
import Badge from './Badge'

interface GroupsPageProps {
  user: User
}

export default function GroupsPage(_props: GroupsPageProps) {
  const [groups, setGroups] = useState<BotGroup[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    fetchGroups()
      .then(setGroups)
      .catch(err => {
        if (err instanceof ApiError && err.status === 401) {
          window.location.reload()
          return
        }
        setError(err instanceof Error ? err.message : 'Failed to load groups')
      })
      .finally(() => setLoading(false))
  }, [])

  return (
    <section className="groups-page">
      <h2 className="groups-page__title">Groups</h2>

      {error && <p className="groups-page__error">{error}</p>}

      {loading ? (
        <p className="groups-page__loading">Loading…</p>
      ) : groups.length === 0 ? (
        <p className="groups-page__empty">
          You don't administer any groups the bot is in. Ask a group admin to add you,
          or add the bot to a group where you are an admin.
        </p>
      ) : (
        <div className="groups-table-wrapper">
          <table className="groups-table">
            <thead>
              <tr>
                <th>Title</th>
                <th>Bot role</th>
                <th>Language</th>
                <th>Timezone</th>
                <th>Added</th>
              </tr>
            </thead>
            <tbody>
              {groups.map(g => (
                <tr key={g.chat_id}>
                  <td>
                    <Link className="groups-table__link" to={`/groups/${g.chat_id}`}>{g.title}</Link>
                  </td>
                  <td>
                    {g.bot_is_admin
                      ? <Badge variant="success">Admin</Badge>
                      : <Badge variant="muted">Member</Badge>}
                  </td>
                  <td>{g.language}</td>
                  <td>{g.timezone}</td>
                  <td className="groups-table__cell--when">
                    {new Date(g.added_at).toLocaleString()}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </section>
  )
}
