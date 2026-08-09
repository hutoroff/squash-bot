import { useCallback, useEffect, useState } from 'react'
import type { User, UserPreferences } from '../types'
import { DEFAULT_PREFERENCES, fetchMyPreferences, updateDMLanguage, updateResultsOptOut } from '../api/prefs'
import { ApiError } from '../api/http'
import { LANGUAGES } from '../settingsLabels'
import Field from './Field'

interface SettingsPageProps {
  user: User
}

export default function SettingsPage(_props: SettingsPageProps) {
  const [prefs, setPrefs] = useState<UserPreferences>(DEFAULT_PREFERENCES)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    fetchMyPreferences()
      .then(setPrefs)
      .catch(err => {
        if (err instanceof ApiError && err.status === 401) {
          window.location.reload()
          return
        }
        setError(err instanceof Error ? err.message : 'Failed to load settings')
      })
      .finally(() => setLoading(false))
  }, [])

  // Optimistic update: show the new value immediately, roll back if the save fails.
  const save = useCallback(async (patch: Partial<UserPreferences>, persist: () => Promise<void>) => {
    const previous = prefs
    setPrefs({ ...previous, ...patch })
    setError(null)
    try {
      await persist()
    } catch (err) {
      setPrefs(previous)
      setError(err instanceof Error ? err.message : 'Failed to save')
    }
  }, [prefs])

  if (loading) return <p className="groups-page__loading">Loading…</p>

  return (
    <section className="settings-page">
      <h2 className="settings-page__title">My settings</h2>

      {error && <p className="groups-page__error">{error}</p>}

      <div className="settings-card">
        <Field label="Direct message language" help="Language the bot uses when it writes to you privately.">
          <select
            value={prefs.dm_language}
            onChange={e => save({ dm_language: e.target.value }, () => updateDMLanguage(e.target.value))}
          >
            {LANGUAGES.map(l => <option key={l.value} value={l.value}>{l.label}</option>)}
          </select>
        </Field>
        <Field
          inline
          label="Don't ask me about game results"
          help="Stops the bot from sending you result-submission prompts after a game."
        >
          <input
            type="checkbox"
            checked={prefs.results_opt_out}
            onChange={e => save({ results_opt_out: e.target.checked }, () => updateResultsOptOut(e.target.checked))}
          />
        </Field>
      </div>
    </section>
  )
}
