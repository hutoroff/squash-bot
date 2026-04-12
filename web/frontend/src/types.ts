export interface User {
  telegram_id: number
  player_id?: number
  first_name: string
  last_name?: string
  username?: string
  photo_url?: string
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
