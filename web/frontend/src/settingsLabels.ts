import type { BookingReadinessReason, Sport } from './types'

export const SPORTS: Record<Sport, { label: string; emoji: string; unit: string; defaultPlayers: number; maxPlayers: number }> = {
  squash: { label: 'Squash', emoji: '🏸', unit: 'court', defaultPlayers: 2, maxPlayers: 4 },
  badminton: { label: 'Badminton', emoji: '🏸', unit: 'court', defaultPlayers: 2, maxPlayers: 4 },
  table_tennis: { label: 'Table tennis', emoji: '🏓', unit: 'table', defaultPlayers: 2, maxPlayers: 4 },
  tennis: { label: 'Tennis', emoji: '🎾', unit: 'court', defaultPlayers: 2, maxPlayers: 4 },
  padel: { label: 'Padel', emoji: '🎾', unit: 'court', defaultPlayers: 4, maxPlayers: 4 },
  bowling: { label: 'Bowling', emoji: '🎳', unit: 'lane', defaultPlayers: 6, maxPlayers: 6 },
}

/** The three languages the bot speaks (i18n.Normalize maps everything to these). */
export const LANGUAGES = [
  { value: 'en', label: 'English' },
  { value: 'de', label: 'Deutsch' },
  { value: 'ru', label: 'Русский' },
]

/** Indexes match Go's time.Weekday, which the venue game_days column stores. */
export const WEEKDAYS = [
  { value: 0, label: 'Sun' },
  { value: 1, label: 'Mon' },
  { value: 2, label: 'Tue' },
  { value: 3, label: 'Wed' },
  { value: 4, label: 'Thu' },
  { value: 5, label: 'Fri' },
  { value: 6, label: 'Sat' },
]

/** Why auto-booking will or won't run, in words an admin can act on. */
export const READINESS_TEXT: Record<BookingReadinessReason, string> = {
  '': 'Ready to book',
  credentials_not_configured: 'Credential storage is not configured on the server',
  auto_booking_disabled: 'Auto-booking is off for this venue',
  auto_booking_disallowed_by_owner: 'Auto-booking is blocked for this group by the server owner',
  no_usable_credentials: 'No usable booking credentials — add one, or wait out the error cooldown',
}

/** Comma-separated storage format ⇄ list of trimmed, non-empty values. */
export function splitList(value: string): string[] {
  return value.split(',').map(v => v.trim()).filter(Boolean)
}

export function joinList(values: (string | number)[]): string {
  return values.join(',')
}

/** "Sun, Wed · 18:00, 19:00" — the one-line venue schedule summary. */
export function scheduleSummary(gameDays: string, timeSlots: string): string {
  const days = splitList(gameDays)
    .map(d => WEEKDAYS[Number(d)]?.label)
    .filter(Boolean)
  const parts = []
  if (days.length) parts.push(days.join(', '))
  if (timeSlots) parts.push(splitList(timeSlots).join(', '))
  return parts.length ? parts.join(' · ') : 'No schedule set'
}
