import { useState } from 'react'
import type { AuditEventType, AuditFilters } from '../types'

const EVENT_TYPES: { value: AuditEventType; label: string }[] = [
  { value: 'game.created', label: 'Game created' },
  { value: 'game.courts_reserved', label: 'Courts reserved' },
  { value: 'participation.joined', label: 'Player joined' },
  { value: 'participation.skipped', label: 'Player skipped' },
  { value: 'participation.guest_added', label: 'Guest added' },
  { value: 'participation.guest_removed', label: 'Guest removed' },
  { value: 'participation.player_kicked', label: 'Player kicked' },
  { value: 'participation.guest_kicked', label: 'Guest kicked' },
  { value: 'credential.added', label: 'Credential added' },
  { value: 'credential.removed', label: 'Credential removed' },
  { value: 'venue.created', label: 'Venue created' },
  { value: 'venue.updated', label: 'Venue updated' },
  { value: 'venue.deleted', label: 'Venue deleted' },
  { value: 'group.bot_added', label: 'Bot added to group' },
  { value: 'group.bot_removed', label: 'Bot removed from group' },
  { value: 'group.settings_changed', label: 'Group settings changed' },
  { value: 'court.booked', label: 'Court booked' },
  { value: 'court.canceled', label: 'Court canceled' },
]

interface AuditFiltersProps {
  isServerOwner: boolean
  onApply: (filters: AuditFilters) => void
}

export default function AuditFiltersForm({ isServerOwner, onApply }: AuditFiltersProps) {
  const [eventType, setEventType] = useState<AuditEventType | ''>('')
  const [from, setFrom] = useState('')
  const [to, setTo] = useState('')
  const [groupId, setGroupId] = useState('')
  const [actorTgId, setActorTgId] = useState('')

  const handleApply = () => {
    const filters: AuditFilters = {}
    if (eventType) filters.event_type = eventType
    if (from) filters.from = new Date(from).toISOString()
    if (to) { const d = new Date(to); d.setDate(d.getDate() + 1); filters.to = d.toISOString() }
    if (isServerOwner && groupId) filters.group_id = parseInt(groupId, 10)
    if (isServerOwner && actorTgId) filters.actor_tg_id = parseInt(actorTgId, 10)
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
            {EVENT_TYPES.map(t => (
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
              Actor TG ID
              <input
                type="number"
                className="audit-filters__input audit-filters__input--narrow"
                value={actorTgId}
                onChange={e => setActorTgId(e.target.value)}
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
