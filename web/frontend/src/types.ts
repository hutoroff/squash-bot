export interface User {
  telegram_id: number
  player_id?: number
  first_name: string
  last_name?: string
  username?: string
  photo_url?: string
  is_server_owner?: boolean
}

export type AuditVisibility = 'player' | 'group_admin' | 'server_owner'
export type AuditActorKind = 'user' | 'system'
export type AuditEventType =
  | 'game.created'
  | 'game.courts_reserved'
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
  | 'court.booked'
  | 'court.canceled'

export interface AuditEvent {
  id: number
  occurred_at: string
  event_type: AuditEventType
  visibility: AuditVisibility
  actor_kind: AuditActorKind
  actor_tg_id?: number
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
  actor_tg_id?: number
  before_id?: number
  limit?: number
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
