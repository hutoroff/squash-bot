import { useEffect, useState } from 'react'
import type { AdminUser, User } from '../types'
import { fetchUsers, setServerOwner } from '../api/users'
import { ApiError } from '../api/http'
import Badge from './Badge'

interface UsersPageProps {
  user: User
}

function message(err: unknown, fallback: string): string {
  return err instanceof Error ? err.message : fallback
}

export default function UsersPage(_props: UsersPageProps) {
  const [users, setUsers] = useState<AdminUser[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [savingIDs, setSavingIDs] = useState<Set<number>>(new Set())

  useEffect(() => {
    fetchUsers()
      .then(setUsers)
      .catch(err => {
        if (err instanceof ApiError && err.status === 401) {
          window.location.reload()
          return
        }
        setError(message(err, 'Failed to load users'))
      })
      .finally(() => setLoading(false))
  }, [])

  const handleToggleOwner = async (target: AdminUser) => {
    if (savingIDs.has(target.user_id)) return // already in flight — ignore duplicate clicks
    const enabled = !target.is_server_owner
    setError(null)
    setSavingIDs(prev => new Set(prev).add(target.user_id))
    setUsers(prev => prev.map(u => u.user_id === target.user_id ? { ...u, is_server_owner: enabled } : u))
    try {
      await setServerOwner(target.user_id, enabled)
    } catch (err) {
      setUsers(prev => prev.map(u => u.user_id === target.user_id ? { ...u, is_server_owner: target.is_server_owner } : u))
      if (err instanceof ApiError && err.status === 409) {
        setError('Cannot remove the last server owner.')
      } else {
        setError(message(err, 'Failed to update server owner'))
      }
    } finally {
      setSavingIDs(prev => {
        const next = new Set(prev)
        next.delete(target.user_id)
        return next
      })
    }
  }

  return (
    <section className="groups-page">
      <h2 className="groups-page__title">Users</h2>

      {error && <p className="groups-page__error">{error}</p>}

      {loading ? (
        <p className="groups-page__loading">Loading…</p>
      ) : users.length === 0 ? (
        <p className="groups-page__empty">No users yet.</p>
      ) : (
        <div className="groups-table-wrapper">
          <table className="groups-table">
            <thead>
              <tr>
                <th>Name</th>
                <th>Providers</th>
                <th>Created</th>
                <th>Server owner</th>
              </tr>
            </thead>
            <tbody>
              {users.map(u => (
                <tr key={u.user_id}>
                  <td>{u.display_name || `user#${u.user_id}`}</td>
                  <td>
                    {u.providers.length === 0
                      ? <Badge variant="muted">none</Badge>
                      : u.providers.map(p => <Badge key={p} variant="info">{p}</Badge>)}
                  </td>
                  <td className="groups-table__cell--when">
                    {new Date(u.created_at).toLocaleString()}
                  </td>
                  <td>
                    <input
                      type="checkbox"
                      checked={u.is_server_owner}
                      disabled={savingIDs.has(u.user_id)}
                      onChange={() => handleToggleOwner(u)}
                    />
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
