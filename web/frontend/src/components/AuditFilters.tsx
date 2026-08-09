import { useState } from 'react'
import type { AuditEventType, AuditFilters } from '../types'
import { EVENT_TYPE_OPTIONS } from '../auditEvents'

interface AuditFiltersProps {
  isServerOwner: boolean
  onApply: (filters: AuditFilters) => void
}

export default function AuditFiltersForm({ isServerOwner, onApply }: AuditFiltersProps) {
  const [eventType, setEventType] = useState<AuditEventType | ''>('')
  const [from, setFrom] = useState('')
  const [to, setTo] = useState('')
  const [groupId, setGroupId] = useState('')
  const [actorUserId, setActorUserId] = useState('')

  const handleApply = () => {
    const filters: AuditFilters = {}
    if (eventType) filters.event_type = eventType
    if (from) filters.from = new Date(from).toISOString()
    if (to) { const [y, m, d] = to.split('-').map(Number); filters.to = new Date(Date.UTC(y, m - 1, d + 1)).toISOString() }
    if (isServerOwner && groupId) filters.group_id = parseInt(groupId, 10)
    if (isServerOwner && actorUserId) filters.actor_user_id = parseInt(actorUserId, 10)
    onApply(filters)
  }

  return (
    <div className="audit-filters">
      <div className="audit-filters__row">
        <label className="audit-filters__label">
          Event type
          <select
            className="audit-filters__select"
            value={eventType}
            onChange={e => setEventType(e.target.value as AuditEventType | '')}
          >
            <option value="">Any</option>
            {EVENT_TYPE_OPTIONS.map(t => (
              <option key={t.value} value={t.value}>{t.label}</option>
            ))}
          </select>
        </label>

        <label className="audit-filters__label">
          From
          <input
            type="date"
            className="audit-filters__input"
            value={from}
            onChange={e => setFrom(e.target.value)}
          />
        </label>

        <label className="audit-filters__label">
          To
          <input
            type="date"
            className="audit-filters__input"
            value={to}
            onChange={e => setTo(e.target.value)}
          />
        </label>

        {isServerOwner && (
          <>
            <label className="audit-filters__label">
              Group ID
              <input
                type="number"
                className="audit-filters__input audit-filters__input--narrow"
                value={groupId}
                onChange={e => setGroupId(e.target.value)}
                placeholder="any"
              />
            </label>
            <label className="audit-filters__label">
              Actor user ID
              <input
                type="number"
                className="audit-filters__input audit-filters__input--narrow"
                value={actorUserId}
                onChange={e => setActorUserId(e.target.value)}
                placeholder="any"
              />
            </label>
          </>
        )}
      </div>

      <button className="audit-filters__apply" onClick={handleApply}>
        Apply filters
      </button>
    </div>
  )
}
