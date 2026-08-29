import { useCallback, useEffect, useState, type FormEvent } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import type { BookingReadiness, User, VenueCredential, VenueInput } from '../types'
import { fetchGroup } from '../api/groups'
import {
  addCredential,
  createVenue,
  deleteCredential,
  fetchBookingReadiness,
  fetchCredentialPriorities,
  fetchCredentials,
  fetchVenue,
  updateVenue,
} from '../api/venues'
import { ApiError } from '../api/http'
import { READINESS_TEXT, WEEKDAYS, joinList, splitList } from '../settingsLabels'
import Field from './Field'

interface VenueFormPageProps {
  user: User
}

const EMPTY_VENUE: VenueInput = {
  name: '',
  address: '',
  courts: '',
  time_slots: '',
  game_days: '',
  grace_period_hours: 24,
  booking_opens_days: 14,
  preventive_cancellation_fraction: '1/2',
  auto_booking_enabled: false,
  preferred_game_times: '',
  auto_booking_courts: '',
  auto_booking_courts_count: 3,
}

function message(err: unknown, fallback: string): string {
  return err instanceof Error ? err.message : fallback
}

export default function VenueFormPage(_props: VenueFormPageProps) {
  const { chatId, venueId } = useParams()
  const chatID = Number(chatId)
  const venueID = venueId ? Number(venueId) : null
  const isEdit = venueID !== null
  const navigate = useNavigate()

  const [form, setForm] = useState<VenueInput>(EMPTY_VENUE)
  const [autoBookingAllowed, setAutoBookingAllowed] = useState(true)
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const [credentials, setCredentials] = useState<VenueCredential[]>([])
  const [credentialsDisabled, setCredentialsDisabled] = useState(false)
  const [credError, setCredError] = useState<string | null>(null)
  const [readiness, setReadiness] = useState<BookingReadiness | null>(null)
  const [newCred, setNewCred] = useState({ login: '', password: '', priority: 1, max_courts: 3 })

  const set = <K extends keyof VenueInput>(key: K, value: VenueInput[K]) =>
    setForm(prev => ({ ...prev, [key]: value }))

  // Readiness depends on the credential list, so the two always reload together.
  const loadCredentialState = useCallback(async () => {
    if (venueID === null) return
    await fetchBookingReadiness(chatID, venueID).then(setReadiness).catch(() => undefined)
    try {
      const [list, priorities] = await Promise.all([
        fetchCredentials(chatID, venueID),
        fetchCredentialPriorities(chatID, venueID),
      ])
      setCredentials(list)
      setNewCred(prev => ({ ...prev, priority: (priorities.length ? Math.max(...priorities) : 0) + 1 }))
      setCredentialsDisabled(false)
    } catch (err) {
      if (err instanceof ApiError && err.status === 503) {
        setCredentialsDisabled(true)
        return
      }
      setCredError(message(err, 'Failed to load credentials'))
    }
  }, [chatID, venueID])

  useEffect(() => {
    const tasks: Promise<unknown>[] = [
      fetchGroup(chatID).then(g => setAutoBookingAllowed(g.auto_booking_allowed)),
    ]
    if (venueID !== null) {
      tasks.push(fetchVenue(chatID, venueID).then(v => setForm({
        name: v.name,
        address: v.address ?? '',
        courts: v.courts,
        time_slots: v.time_slots,
        game_days: v.game_days,
        grace_period_hours: v.grace_period_hours,
        booking_opens_days: v.booking_opens_days,
        preventive_cancellation_fraction: v.preventive_cancellation_fraction,
        auto_booking_enabled: v.auto_booking_enabled,
        preferred_game_times: v.preferred_game_times,
        auto_booking_courts: v.auto_booking_courts,
        auto_booking_courts_count: v.auto_booking_courts_count || 3,
      })))
      tasks.push(loadCredentialState())
    }
    Promise.all(tasks)
      .catch(err => setError(message(err, 'Failed to load venue')))
      .finally(() => setLoading(false))
  }, [chatID, venueID, loadCredentialState])

  // ── structured editors ─────────────────────────────────────────────────────

  const courts = splitList(form.courts)
  const timeSlots = splitList(form.time_slots)
  const preferred = splitList(form.preferred_game_times)
  const priority = splitList(form.auto_booking_courts)
  const gameDays = splitList(form.game_days).map(Number)

  const toggleDay = (day: number) => {
    const next = gameDays.includes(day) ? gameDays.filter(d => d !== day) : [...gameDays, day].sort()
    set('game_days', joinList(next))
  }

  const togglePreferred = (slot: string) => {
    const next = preferred.includes(slot) ? preferred.filter(s => s !== slot) : [...preferred, slot].sort()
    set('preferred_game_times', joinList(next))
  }

  // Removing a time slot must also drop it from preferred times (server rejects ⊄).
  const setTimeSlots = (slots: string[]) => {
    setForm(prev => ({
      ...prev,
      time_slots: joinList(slots),
      preferred_game_times: joinList(splitList(prev.preferred_game_times).filter(s => slots.includes(s))),
    }))
  }

  // Removing a court must also drop it from the auto-booking priority list.
  const setCourts = (list: string[]) => {
    setForm(prev => ({
      ...prev,
      courts: joinList(list),
      auto_booking_courts: joinList(splitList(prev.auto_booking_courts).filter(c => list.includes(c))),
    }))
  }

  const moveCourt = (index: number, delta: number) => {
    const next = [...priority]
    const target = index + delta
    if (target < 0 || target >= next.length) return
    ;[next[index], next[target]] = [next[target], next[index]]
    set('auto_booking_courts', joinList(next))
  }

  // ── submit ─────────────────────────────────────────────────────────────────

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault()
    if (!form.name.trim() || courts.length === 0) {
      setError('Name and at least one court are required.')
      return
    }
    if (form.auto_booking_enabled && form.auto_booking_courts_count < 1) {
      setError('Courts per game must be at least 1 while auto-booking is on — 0 would silently book nothing.')
      return
    }
    setSaving(true)
    setError(null)
    try {
      if (venueID === null) {
        await createVenue(chatID, form)
      } else {
        await updateVenue(chatID, venueID, form)
      }
      navigate(`/groups/${chatID}`)
    } catch (err) {
      setError(err instanceof ApiError && err.message === 'auto_booking_disallowed_by_owner'
        ? 'The server owner has blocked auto-booking for this group.'
        : message(err, 'Failed to save venue'))
    } finally {
      setSaving(false)
    }
  }

  // ── credentials ────────────────────────────────────────────────────────────

  const handleAddCredential = async (e: FormEvent) => {
    e.preventDefault()
    if (venueID === null) return
    setCredError(null)
    try {
      await addCredential(chatID, venueID, newCred)
      setNewCred(prev => ({ ...prev, login: '', password: '' }))
      await loadCredentialState()
    } catch (err) {
      setCredError(err instanceof ApiError && err.status === 409
        ? 'A credential with this login already exists for this venue.'
        : message(err, 'Failed to add credential'))
    }
  }

  const handleDeleteCredential = async (cred: VenueCredential) => {
    if (venueID === null) return
    if (!window.confirm(`Remove the booking login "${cred.login}"?`)) return
    setCredError(null)
    try {
      await deleteCredential(chatID, venueID, cred.id)
      await loadCredentialState()
    } catch (err) {
      setCredError(err instanceof ApiError && err.status === 409
        ? 'This login has active court bookings and cannot be removed yet.'
        : message(err, 'Failed to remove credential'))
    }
  }

  if (loading) return <p className="groups-page__loading">Loading…</p>

  return (
    <section className="settings-page">
      <h2 className="settings-page__title">{isEdit ? form.name || 'Venue' : 'New venue'}</h2>
      <Link className="settings-page__back" to={`/groups/${chatID}`}>← Back to group</Link>

      {error && <p className="groups-page__error">{error}</p>}

      <form onSubmit={handleSubmit}>
        <div className="settings-card">
          <h3 className="settings-card__title">Basics</h3>
          <Field label="Name" help="Shown in every game announcement for this venue.">
            <input value={form.name} onChange={e => set('name', e.target.value)} required />
          </Field>
          <Field label="Address" help="Optional. Added to announcements so players can find the place.">
            <input value={form.address ?? ''} onChange={e => set('address', e.target.value)} />
          </Field>

          <div className="field">
            <span className="field__label">Courts</span>
            <ChipList
              values={courts}
              onRemove={c => setCourts(courts.filter(x => x !== c))}
              addPlaceholder="Court number"
              addType="number"
              onAdd={value => !courts.includes(value) && setCourts([...courts, value])}
            />
            <p className="field__help">
              The court numbers this venue has. Auto-booking only ever books courts from this list.
            </p>
          </div>

          <div className="field">
            <span className="field__label">Time slots</span>
            <ChipList
              values={timeSlots}
              onRemove={s => setTimeSlots(timeSlots.filter(x => x !== s))}
              addType="time"
              onAdd={value => !timeSlots.includes(value) && setTimeSlots([...timeSlots, value].sort())}
            />
            <p className="field__help">
              The times games can start. Preferred auto-booking times are chosen from this list.
            </p>
          </div>
        </div>

        <div className="settings-card">
          <h3 className="settings-card__title">Game schedule</h3>
          <div className="field">
            <span className="field__label">Game days</span>
            <div className="chip-row">
              {WEEKDAYS.map(d => (
                <button
                  key={d.value}
                  type="button"
                  aria-pressed={gameDays.includes(d.value)}
                  className={'chip' + (gameDays.includes(d.value) ? ' chip--on' : '')}
                  onClick={() => toggleDay(d.value)}
                >
                  {d.label}
                </button>
              ))}
            </div>
            <p className="field__help">Weekdays this group plays. Reminders and auto-booking only run for these days.</p>
          </div>

          <Field
            label="Grace period (hours)"
            help="How long before a game the line-up is final — the cancellation reminder fires 6 hours before that."
          >
            <input
              type="number" min={1}
              value={form.grace_period_hours}
              onChange={e => set('grace_period_hours', Number(e.target.value))}
            />
          </Field>
          <Field
            label="Booking opens (days ahead)"
            help="How many days in advance the venue releases courts — used for the manual booking reminder."
          >
            <input
              type="number" min={1}
              value={form.booking_opens_days}
              onChange={e => set('booking_opens_days', Number(e.target.value))}
            />
          </Field>
        </div>

        <div className="settings-card">
          <h3 className="settings-card__title">Auto-booking</h3>
          {!autoBookingAllowed && (
            <p className="settings-card__note">
              The server owner has blocked auto-booking for this group, so it cannot be enabled here.
            </p>
          )}
          <Field
            inline
            label="Book courts automatically"
            help="At midnight on booking day the bot books the preferred times and creates the game."
          >
            <input
              type="checkbox"
              disabled={!autoBookingAllowed}
              checked={form.auto_booking_enabled}
              onChange={e => set('auto_booking_enabled', e.target.checked)}
            />
          </Field>

          {form.auto_booking_enabled && (
            <Field
              label="Preventive cancellation"
              help="Release unused courts at this point between booking opening and the grace-period deadline."
            >
              <select
                value={form.preventive_cancellation_fraction}
                onChange={e => set('preventive_cancellation_fraction', e.target.value as VenueInput['preventive_cancellation_fraction'])}
              >
                <option value="1/3">One third</option>
                <option value="1/2">One half</option>
                <option value="2/3">Two thirds</option>
              </select>
            </Field>
          )}

          <div className="field">
            <span className="field__label">Preferred game times</span>
            {timeSlots.length === 0 ? (
              <p className="settings-card__note">Add time slots above first.</p>
            ) : (
              <div className="chip-row">
                {timeSlots.map(slot => (
                  <button
                    key={slot}
                    type="button"
                    aria-pressed={preferred.includes(slot)}
                    className={'chip' + (preferred.includes(slot) ? ' chip--on' : '')}
                    onClick={() => togglePreferred(slot)}
                  >
                    {slot}
                  </button>
                ))}
              </div>
            )}
            <p className="field__help">Which of the time slots auto-booking should try, one game per selected time.</p>
          </div>

          <div className="field">
            <span className="field__label">Court priority</span>
            {priority.length > 0 && (
              <ol className="priority-list">
                {priority.map((court, i) => (
                  <li key={court} className="priority-list__item">
                    <span>Court {court}</span>
                    <button type="button" aria-label={`Move court ${court} up`} onClick={() => moveCourt(i, -1)}>↑</button>
                    <button type="button" aria-label={`Move court ${court} down`} onClick={() => moveCourt(i, 1)}>↓</button>
                    <button
                      type="button"
                      aria-label={`Remove court ${court} from priority`}
                      onClick={() => set('auto_booking_courts', joinList(priority.filter(c => c !== court)))}
                    >×</button>
                  </li>
                ))}
              </ol>
            )}
            <div className="chip-row">
              {courts.filter(c => !priority.includes(c)).map(c => (
                <button
                  key={c}
                  type="button"
                  className="chip"
                  onClick={() => set('auto_booking_courts', joinList([...priority, c]))}
                >
                  + Court {c}
                </button>
              ))}
            </div>
            <p className="field__help">
              Courts are booked in this order. Leave empty to accept any free court from the list above.
            </p>
          </div>

          <Field
            label="Courts per game"
            help="How many courts to book for each game. Two players fit on one court."
          >
            <input
              type="number" min={1}
              value={form.auto_booking_courts_count}
              onChange={e => set('auto_booking_courts_count', Number(e.target.value))}
            />
          </Field>
        </div>

        <div className="settings-page__actions">
          <button type="submit" className="settings-button settings-button--primary" disabled={saving}>
            {saving ? 'Saving…' : 'Save venue'}
          </button>
          <Link className="settings-button" to={`/groups/${chatID}`}>Cancel</Link>
        </div>
      </form>

      {isEdit && (
        <div className="settings-card">
          <h3 className="settings-card__title">Booking credentials</h3>
          {readiness && (
            <p className="settings-card__note">
              {readiness.ready
                ? `Auto-booking is ready — up to ${readiness.max_courts} courts per run.`
                : READINESS_TEXT[readiness.reason]}
            </p>
          )}

          {credentialsDisabled ? (
            <p className="settings-card__note">
              Credential storage is not configured on this server, so booking logins cannot be saved.
            </p>
          ) : (
            <>
              {credError && <p className="groups-page__error">{credError}</p>}

              {credentials.length === 0 ? (
                <p className="settings-card__note">No booking logins yet — auto-booking needs at least one.</p>
              ) : (
                <ul className="venue-list">
                  {credentials.map(c => (
                    <li key={c.id} className="venue-card">
                      <div className="venue-card__main">
                        <span className="venue-card__name">{c.login}</span>
                        <span className="venue-card__schedule">
                          Priority {c.priority} · up to {c.max_courts} courts
                          {c.last_error_at && ` · last failed ${new Date(c.last_error_at).toLocaleString()}`}
                        </span>
                      </div>
                      <div className="venue-card__actions">
                        <button
                          className="settings-button settings-button--danger"
                          onClick={() => handleDeleteCredential(c)}
                        >
                          Remove
                        </button>
                      </div>
                    </li>
                  ))}
                </ul>
              )}

              <form className="cred-form" onSubmit={handleAddCredential}>
                <Field label="Login" help="The Eversports account email used to book courts.">
                  <input
                    value={newCred.login}
                    onChange={e => setNewCred({ ...newCred, login: e.target.value })}
                    required
                  />
                </Field>
                <Field label="Password" help="Stored encrypted and never shown again — re-enter it to change it.">
                  <input
                    type="password"
                    value={newCred.password}
                    onChange={e => setNewCred({ ...newCred, password: e.target.value })}
                    required
                  />
                </Field>
                <Field label="Priority" help="Lower numbers are used first; the next one is filled in for you.">
                  <input
                    type="number" min={0}
                    value={newCred.priority}
                    onChange={e => setNewCred({ ...newCred, priority: Number(e.target.value) })}
                  />
                </Field>
                <Field label="Max courts" help="How many courts this account may book before the next one is used.">
                  <input
                    type="number" min={1}
                    value={newCred.max_courts}
                    onChange={e => setNewCred({ ...newCred, max_courts: Number(e.target.value) })}
                  />
                </Field>
                <button type="submit" className="settings-button settings-button--primary">Add credential</button>
              </form>
            </>
          )}
        </div>
      )}
    </section>
  )
}

interface ChipListProps {
  values: string[]
  onRemove: (value: string) => void
  onAdd: (value: string) => void
  addType: 'time' | 'number'
  addPlaceholder?: string
}

/** Chips for one comma-separated column, with a native input to add entries. */
function ChipList({ values, onRemove, onAdd, addType, addPlaceholder }: ChipListProps) {
  const [draft, setDraft] = useState('')

  const add = () => {
    const value = draft.trim()
    if (!value) return
    onAdd(value)
    setDraft('')
  }

  return (
    <div className="chip-row">
      {values.map(v => (
        <span key={v} className="chip chip--on">
          {v}
          <button type="button" aria-label={`Remove ${v}`} onClick={() => onRemove(v)}>×</button>
        </span>
      ))}
      <input
        type={addType}
        min={addType === 'number' ? 1 : undefined}
        placeholder={addPlaceholder}
        value={draft}
        onChange={e => setDraft(e.target.value)}
        onKeyDown={e => { if (e.key === 'Enter') { e.preventDefault(); add() } }}
      />
      <button type="button" className="chip" onClick={add}>Add</button>
    </div>
  )
}
