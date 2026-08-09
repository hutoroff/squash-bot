import { useCallback, useEffect, useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import type { BookingReadiness, BotGroup, User, Venue } from '../types'
import {
  fetchGroup,
  updateGroupAutoBookingAllowed,
  updateGroupChangelog,
  updateGroupLanguage,
  updateGroupLeaderboardNotifications,
  updateGroupTimezone,
} from '../api/groups'
import { deleteVenue, fetchBookingReadiness, fetchVenues } from '../api/venues'
import { ApiError } from '../api/http'
import { LANGUAGES, READINESS_TEXT, scheduleSummary } from '../settingsLabels'
import Badge from './Badge'
import Field from './Field'

interface GroupSettingsPageProps {
  user: User
}

/** Native IANA zone list; falls back to the group's own zone on old engines. */
function timezoneOptions(current: string): string[] {
  const supported = (Intl as unknown as { supportedValuesOf?: (k: string) => string[] }).supportedValuesOf
  const zones = supported ? supported('timeZone') : []
  return zones.includes(current) || !current ? zones : [current, ...zones]
}

function message(err: unknown, fallback: string): string {
  return err instanceof Error ? err.message : fallback
}

export default function GroupSettingsPage({ user }: GroupSettingsPageProps) {
  const { chatId } = useParams()
  const chatID = Number(chatId)

  const [group, setGroup] = useState<BotGroup | null>(null)
  const [venues, setVenues] = useState<Venue[]>([])
  const [readiness, setReadiness] = useState<Record<number, BookingReadiness>>({})
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  const loadVenues = useCallback(async () => {
    const list = await fetchVenues(chatID)
    setVenues(list)
    const entries = await Promise.all(list.map(async v => {
      try {
        return [v.id, await fetchBookingReadiness(chatID, v.id)] as const
      } catch {
        return null
      }
    }))
    setReadiness(Object.fromEntries(entries.filter(Boolean) as [number, BookingReadiness][]))
  }, [chatID])

  useEffect(() => {
    Promise.all([fetchGroup(chatID).then(setGroup), loadVenues()])
      .catch(err => {
        if (err instanceof ApiError && err.status === 401) {
          window.location.reload()
          return
        }
        setError(message(err, 'Failed to load group settings'))
      })
      .finally(() => setLoading(false))
  }, [chatID, loadVenues])

  // Optimistic update: show the new value immediately, roll back if the save fails.
  const save = useCallback(async (patch: Partial<BotGroup>, persist: () => Promise<void>) => {
    const previous = group
    if (!previous) return
    setGroup({ ...previous, ...patch })
    setError(null)
    try {
      await persist()
    } catch (err) {
      setGroup(previous)
      setError(message(err, 'Failed to save'))
    }
  }, [group])

  const handleAutoBookingAllowed = useCallback(async (enabled: boolean) => {
    if (!enabled && !window.confirm(
      'Disable auto-booking for this group? Auto-booking is switched off on every venue here, ' +
      'and turning the group switch back on does not restore them.'
    )) {
      return
    }
    await save({ auto_booking_allowed: enabled }, () => updateGroupAutoBookingAllowed(chatID, enabled))
    await loadVenues().catch(() => undefined)
  }, [chatID, save, loadVenues])

  const handleDeleteVenue = useCallback(async (venue: Venue) => {
    if (!window.confirm(`Delete venue "${venue.name}"? Games already scheduled there keep their date but lose the venue.`)) {
      return
    }
    setError(null)
    try {
      await deleteVenue(chatID, venue.id)
      await loadVenues()
    } catch (err) {
      setError(message(err, 'Failed to delete venue'))
    }
  }, [chatID, loadVenues])

  if (loading) return <p className="groups-page__loading">Loading…</p>
  if (!group) return <p className="groups-page__error">{error ?? 'Group not found.'}</p>

  return (
    <section className="settings-page">
      <h2 className="settings-page__title">{group.title}</h2>
      <Link className="settings-page__back" to="/groups">← All groups</Link>

      {error && <p className="groups-page__error">{error}</p>}

      <div className="settings-card">
        <h3 className="settings-card__title">General</h3>
        <Field label="Language" help="Language of every message the bot posts in this group.">
          <select
            value={group.language}
            onChange={e => save({ language: e.target.value }, () => updateGroupLanguage(chatID, e.target.value))}
          >
            {LANGUAGES.map(l => <option key={l.value} value={l.value}>{l.label}</option>)}
          </select>
        </Field>
        <Field label="Timezone" help="Game times, reminders and daily jobs all run in this timezone.">
          <select
            value={group.timezone}
            onChange={e => save({ timezone: e.target.value }, () => updateGroupTimezone(chatID, e.target.value))}
          >
            {timezoneOptions(group.timezone).map(tz => <option key={tz} value={tz}>{tz}</option>)}
          </select>
        </Field>
      </div>

      <div className="settings-card">
        <h3 className="settings-card__title">Notifications</h3>
        <Field inline label="Announce bot updates" help="Posts a short changelog to the group after the bot is upgraded.">
          <input
            type="checkbox"
            checked={group.changelog_enabled}
            onChange={e => save({ changelog_enabled: e.target.checked }, () => updateGroupChangelog(chatID, e.target.checked))}
          />
        </Field>
        <Field inline label="Post the leaderboard" help="Posts the rating table the day after a game, once results are in.">
          <input
            type="checkbox"
            checked={group.leaderboard_notifications_enabled}
            onChange={e => save(
              { leaderboard_notifications_enabled: e.target.checked },
              () => updateGroupLeaderboardNotifications(chatID, e.target.checked),
            )}
          />
        </Field>
      </div>

      <div className="settings-card">
        <h3 className="settings-card__title">Auto-booking</h3>
        {user.is_server_owner ? (
          <Field
            inline
            label="Allow auto-booking for this group"
            help="Master switch. Turning it off disables auto-booking on every venue in this group."
          >
            <input
              type="checkbox"
              checked={group.auto_booking_allowed}
              onChange={e => handleAutoBookingAllowed(e.target.checked)}
            />
          </Field>
        ) : (
          <p className="settings-card__note">
            {group.auto_booking_allowed
              ? 'Auto-booking is allowed for this group. Enable it per venue below.'
              : 'Auto-booking is blocked for this group by the server owner.'}
          </p>
        )}
      </div>

      <div className="settings-card">
        <h3 className="settings-card__title">Venues</h3>
        <p className="field__help">A venue defines where and when your group plays, and how courts get booked.</p>

        {venues.length === 0 ? (
          <p className="settings-card__note">No venues yet — add one to schedule games and court bookings.</p>
        ) : (
          <ul className="venue-list">
            {venues.map(v => {
              const r = readiness[v.id]
              return (
                <li key={v.id} className="venue-card">
                  <div className="venue-card__main">
                    <span className="venue-card__name">{v.name}</span>
                    <span className="venue-card__schedule">{scheduleSummary(v.game_days, v.time_slots)}</span>
                    {r && (
                      <Badge variant={r.ready ? 'success' : 'muted'}>
                        {r.ready ? `Auto-booking ready · up to ${r.max_courts} courts` : READINESS_TEXT[r.reason]}
                      </Badge>
                    )}
                  </div>
                  <div className="venue-card__actions">
                    <Link className="settings-button" to={`/groups/${chatID}/venues/${v.id}`}>Edit</Link>
                    <button className="settings-button settings-button--danger" onClick={() => handleDeleteVenue(v)}>
                      Delete
                    </button>
                  </div>
                </li>
              )
            })}
          </ul>
        )}

        <Link className="settings-button settings-button--primary" to={`/groups/${chatID}/venues/new`}>
          + Add venue
        </Link>
      </div>
    </section>
  )
}
