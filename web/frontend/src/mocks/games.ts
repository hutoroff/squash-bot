import type { Game } from '../types'

// Sundays around 2026-04-02 (today)
const MOCK_GAMES: Game[] = [
  {
    id: 1,
    game_date: '2026-04-05T19:00:00+02:00', // upcoming Sunday
    courts_count: 2,
    courts: 'Court 1,Court 2',
    completed: false,
    participation_status: 'registered',
    participant_count: 3,
    venue_name: 'Squash House Berlin',
    venue_address: 'Torstr. 231, 10115 Berlin',
    group_title: 'Berlin Squash Friends',
    timezone: 'Europe/Berlin',
  },
  {
    id: 2,
    game_date: '2026-04-12T19:00:00+02:00', // Sunday after next
    courts_count: 2,
    courts: 'Court 3,Court 4',
    completed: false,
    participation_status: null,
    participant_count: 1,
    venue_name: 'Squash House Berlin',
    venue_address: 'Torstr. 231, 10115 Berlin',
    group_title: 'Berlin Squash Friends',
    timezone: 'Europe/Berlin',
  },
  {
    id: 3,
    game_date: '2026-03-29T19:00:00+01:00', // last Sunday
    courts_count: 2,
    courts: 'Court 1,Court 2',
    completed: true,
    participation_status: 'registered',
    participant_count: 4,
    venue_name: 'Squash House Berlin',
    venue_address: 'Torstr. 231, 10115 Berlin',
    group_title: 'Berlin Squash Friends',
    timezone: 'Europe/Berlin',
  },
  {
    id: 4,
    game_date: '2026-03-22T19:00:00+01:00',
    courts_count: 1,
    courts: 'Court 2',
    completed: true,
    participation_status: 'skipped',
    participant_count: 2,
    venue_name: 'Squash House Berlin',
    venue_address: 'Torstr. 231, 10115 Berlin',
    group_title: 'Berlin Squash Friends',
    timezone: 'Europe/Berlin',
  },
]

export async function fetchMyGames(): Promise<Game[]> {
  // TODO: replace with real API call: GET /api/v1/games/my
  return new Promise(resolve => setTimeout(() => resolve(MOCK_GAMES), 400))
}
