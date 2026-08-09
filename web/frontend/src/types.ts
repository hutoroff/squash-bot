export interface User {
  user_id: number
  first_name: string
  last_name?: string
  username?: string
  photo_url?: string
  is_server_owner?: boolean
}

/** Row shape for the owner-only Users admin page (GET /api/users). */
export interface AdminUser {
  user_id: number
  display_name: string
  is_server_owner: boolean
  dm_language: string
  results_opt_out: boolean
  created_at: string
  providers: string[]
}

export type AuditVisibility = 'player' | 'group_admin' | 'server_owner'
export type AuditActorKind = 'user' | 'system'
export type AuditEventType =
  | 'game.created'
  | 'game.courts_reserved'
  | 'game.published'
  | 'game.courts_auto_booked'
  | 'game.result_submitted'
  | 'game.result_approved'
  | 'game.result_rejected'
  | 'game.result_auto_approved'
  | 'game.result_canceled'
  | 'game.rating_updated'
  | 'participation.joined'
  | 'participation.skipped'
  | 'participation.guest_added'
  | 'participation.guest_removed'
  | 'participation.player_kicked'
  | 'participation.guest_kicked'
  | 'credential.added'
  | 'credential.removed'
  | 'venue.created'
  | 'venue.updated'
  | 'venue.deleted'
  | 'group.bot_added'
  | 'group.bot_removed'
  | 'group.settings_changed'
  | 'group.changelog_toggled'
  | 'group.leaderboard_notifications_toggled'
  | 'group.auto_booking_allowed_toggled'
  | 'court.booked'
  | 'court.canceled'
  | 'user.role_changed'

export interface AuditEvent {
  id: number
  occurred_at: string
  event_type: AuditEventType
  visibility: AuditVisibility
  actor_kind: AuditActorKind
  /** Legacy actor key, present only on rows recorded before the user-ID migration. */
  actor_tg_id?: number
  actor_user_id?: number
  actor_display?: string
  group_id?: number
  subject_type: string
  subject_id: string
  description: string
  metadata?: Record<string, unknown>
}

export interface AuditFilters {
  event_type?: AuditEventType
  from?: string
  to?: string
  group_id?: number
  actor_user_id?: number
  before_id?: number
  limit?: number
}

export interface BotGroup {
  chat_id: number
  title: string
  bot_is_admin: boolean
  language: string
  timezone: string
  changelog_enabled: boolean
  leaderboard_notifications_enabled: boolean
  auto_booking_allowed: boolean
  added_at: string
}

export interface Venue {
  id: number
  group_id: number
  name: string
  address?: string
  /** Comma-separated court numbers, e.g. "1,2,3" */
  courts: string
  /** Comma-separated HH:MM times, e.g. "18:00,19:00" */
  time_slots: string
  /** Comma-separated Go time.Weekday ints, e.g. "0,3" = Sunday+Wednesday */
  game_days: string
  grace_period_hours: number
  booking_opens_days: number
  auto_booking_enabled: boolean
  /** Comma-separated HH:MM times; must be a subset of time_slots */
  preferred_game_times: string
  /** Ordered comma-separated court numbers tried first when auto-booking */
  auto_booking_courts: string
  auto_booking_courts_count: number
  created_at: string
}

/** Payload for creating or updating a venue — group_id is forced server-side. */
export type VenueInput = Omit<Venue, 'id' | 'group_id' | 'created_at'>

export interface VenueCredential {
  id: number
  venue_id: number
  login: string
  priority: number
  max_courts: number
  last_error_at?: string
  created_at: string
}

export type BookingReadinessReason =
  | ''
  | 'credentials_not_configured'
  | 'auto_booking_disabled'
  | 'auto_booking_disallowed_by_owner'
  | 'no_usable_credentials'

export interface BookingReadiness {
  ready: boolean
  max_courts: number
  reason: BookingReadinessReason
}

export interface UserPreferences {
  user_id: number
  dm_language: string
  results_opt_out: boolean
}

export type ParticipationStatus = 'registered' | 'skipped'

export interface Game {
  id: number
  /** ISO 8601 datetime string */
  game_date: string
  courts_count: number
  /** Comma-separated court names, e.g. "Court 1,Court 2" */
  courts: string
  completed: boolean
  participation_status: ParticipationStatus | null
  /** Total registered players plus guests — matches actual capacity consumption. */
  participant_count: number
  venue_name?: string
  venue_address?: string
  group_title: string
  /** IANA timezone of the group, e.g. "Europe/Berlin". Used to display game times in venue local time. */
  timezone: string
}

export interface GamePlayer {
  user_id: number
  telegram_id: number
  username?: string
  first_name?: string
  last_name?: string
}

export interface GameParticipation {
  id: number
  player: GamePlayer
  status: ParticipationStatus
}

export interface GuestParticipation {
  id: number
  invited_by: GamePlayer
}

export interface GameParticipants {
  participations: GameParticipation[]
  guests: GuestParticipation[]
}
