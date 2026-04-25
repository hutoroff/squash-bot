import type { AuditEvent, AuditEventType } from '../types'

const EVENT_LABELS: Record<AuditEventType, string> = {
  'game.created': 'Game created',
  'game.courts_reserved': 'Courts reserved',
  'participation.joined': 'Player joined',
  'participation.skipped': 'Player skipped',
  'participation.guest_added': 'Guest added',
  'participation.guest_removed': 'Guest removed',
  'participation.player_kicked': 'Player kicked',
  'participation.guest_kicked': 'Guest kicked',
  'credential.added': 'Credential added',
  'credential.removed': 'Credential removed',
  'venue.created': 'Venue created',
  'venue.updated': 'Venue updated',
  'venue.deleted': 'Venue deleted',
  'group.bot_added': 'Bot added',
  'group.bot_removed': 'Bot removed',
  'group.settings_changed': 'Settings changed',
  'court.booked': 'Court booked',
  'court.canceled': 'Court canceled',
}

const dtFmt = new Intl.DateTimeFormat(undefined, {
  dateStyle: 'short',
  timeStyle: 'short',
})

function formatActor(evt: AuditEvent): string {
  if (evt.actor_kind === 'system') return 'system'
  if (evt.actor_display) return evt.actor_display
  if (evt.actor_tg_id) return `Telegram #${evt.actor_tg_id}`
  return '—'
}

interface AuditTableProps {
  events: AuditEvent[]
}

export default function AuditTable({ events }: AuditTableProps) {
  if (events.length === 0) {
    return <p className="audit-table__empty">No events match these filters.</p>
  }

  return (
    <div className="audit-table-wrapper">
      <table className="audit-table">
        <thead>
          <tr>
            <th>When</th>
            <th>Type</th>
            <th>Actor</th>
            <th>Group</th>
            <th>Description</th>
          </tr>
        </thead>
        <tbody>
          {events.map(evt => (
            <tr key={evt.id}>
              <td className="audit-table__cell--when">{dtFmt.format(new Date(evt.occurred_at))}</td>
              <td className="audit-table__cell--type">{EVENT_LABELS[evt.event_type] ?? evt.event_type}</td>
              <td>{formatActor(evt)}</td>
              <td>{evt.group_id ?? '—'}</td>
              <td>{evt.description}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
