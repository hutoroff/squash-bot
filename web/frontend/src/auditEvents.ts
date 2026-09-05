import type { AuditEventType } from './types'

export const EVENT_LABELS: Record<AuditEventType, string> = {
  'game.created': 'Game created',
  'game.courts_reserved': 'Courts reserved',
  'game.published': 'Game published',
  'game.courts_auto_booked': 'Courts auto-booked',
  'game.result_submitted': 'Result submitted',
  'game.result_approved': 'Result approved',
  'game.result_rejected': 'Result rejected',
  'game.result_auto_approved': 'Result auto-approved',
  'game.result_canceled': 'Result canceled',
  'feature_flag.changed': 'Feature flag changed',
  'game.rating_updated': 'Rating updated',
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
  'group.changelog_toggled': 'Changelog toggled',
  'group.leaderboard_notifications_toggled': 'Leaderboard notifications toggled',
  'group.auto_booking_allowed_toggled': 'Auto-booking toggled',
  'court.booked': 'Court booked',
  'court.canceled': 'Court canceled',
  'user.role_changed': 'Server owner role changed',
}

export const EVENT_TYPE_OPTIONS = Object.entries(EVENT_LABELS).map(
  ([value, label]) => ({ value: value as AuditEventType, label }),
)
