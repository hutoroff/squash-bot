import { useState, useEffect, useCallback } from 'react'
import type { User, AuditEvent, AuditFilters } from '../types'
import { fetchAuditEvents } from '../api/audit'
import { ApiError } from '../api/http'
import AuditFiltersForm from './AuditFilters'
import AuditTable from './AuditTable'

interface AuditPageProps {
  user: User
}

export default function AuditPage({ user }: AuditPageProps) {
  const [events, setEvents] = useState<AuditEvent[]>([])
  const [filters, setFilters] = useState<AuditFilters>({})
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [hasMore, setHasMore] = useState(false)

  const load = useCallback(async (f: AuditFilters, append: boolean) => {
    setLoading(true)
    setError(null)
    try {
      const data = await fetchAuditEvents({ ...f, limit: 50 })
      setEvents(prev => append ? [...prev, ...data] : data)
      setHasMore(data.length === 50)
    } catch (err) {
      if (err instanceof ApiError && err.status === 401) {
        window.location.reload()
        return
      }
      setError(err instanceof Error ? err.message : 'Failed to load events')
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    load(filters, false)
  }, []) // eslint-disable-line react-hooks/exhaustive-deps

  const handleApply = (f: AuditFilters) => {
    setFilters(f)
    setEvents([])
    load(f, false)
  }

  const handleLoadMore = () => {
    if (events.length === 0) return
    const lastId = events[events.length - 1].id
    load({ ...filters, before_id: lastId }, true)
  }

  return (
    <section className="audit-page">
      <h2 className="audit-page__title">Audit log</h2>

      <AuditFiltersForm
        isServerOwner={user.is_server_owner === true}
        onApply={handleApply}
      />

      {error && <p className="audit-page__error">{error}</p>}

      {loading && events.length === 0 ? (
        <p className="audit-page__loading">Loading…</p>
      ) : (
        <>
          <AuditTable events={events} />
          {hasMore && (
            <button
              className="audit-page__load-more"
              onClick={handleLoadMore}
              disabled={loading}
            >
              {loading ? 'Loading…' : 'Load more'}
            </button>
          )}
        </>
      )}
    </section>
  )
}
