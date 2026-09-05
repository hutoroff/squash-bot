import { useEffect, useState } from 'react'
import { fetchGroups } from '../api/groups'
import { fetchFeatureFlags, setFeatureFlag } from '../api/featureFlags'
import type { FeatureFlag } from '../api/featureFlags'
import type { BotGroup } from '../types'

export default function FeatureFlagsPage() {
  const [groups, setGroups] = useState<BotGroup[]>([])
  const [scope, setScope] = useState('')
  const [flags, setFlags] = useState<FeatureFlag[] | null>(null)
  const [error, setError] = useState('')
  const [saving, setSaving] = useState(false)
  const [reload, setReload] = useState(0)

  useEffect(() => {
    let active = true
    setFlags(null)
    setError('')
    Promise.all([fetchGroups(), fetchFeatureFlags(scope === '' ? undefined : Number(scope))])
      .then(([gs, fs]) => { if (active) { setGroups(gs); setFlags(fs) } })
      .catch(err => { if (active) setError(err instanceof Error ? err.message : 'Unable to load feature flags') })
    return () => { active = false }
  }, [scope, reload])

  async function save(flag: FeatureFlag, value: string) {
    setSaving(true)
    setError('')
    try {
      const groupID = scope === '' ? undefined : Number(scope)
      await setFeatureFlag(flag.key, value === 'inherit' ? null : value === 'enabled', groupID)
      // Reload authoritative effective values instead of assuming the write's outcome.
      setFlags(await fetchFeatureFlags(groupID))
    } catch (err) {
      // A read failure after a successful write is ambiguous: don't show stale state as current.
      setFlags(null)
      setError(err instanceof Error ? err.message : 'Unable to save feature flag')
    } finally { setSaving(false) }
  }

  return <section className="settings-page">
    <h1>Feature toggles</h1>
    <p>All flags default to disabled. A group override takes precedence over the global setting; global Disabled is not a kill switch.</p>
    <label>Scope <select aria-label="Scope" value={scope} disabled={saving} onChange={e => { setFlags(null); setScope(e.target.value) }}>
      <option value="">Global defaults</option>
      {groups.map(g => <option key={g.chat_id} value={g.chat_id}>{g.title}</option>)}
    </select></label>
    {error && <p role="alert">{error} <button disabled={saving} onClick={() => setReload(n => n + 1)}>Reload</button></p>}
    {!flags && !error && <p>Loading…</p>}
    {flags?.map(flag => {
      const override = scope === '' ? flag.global : flag.override
      return <fieldset key={flag.key} disabled={saving || (scope !== '' && !flag.group_scoped)}>
        <legend>{flag.key}</legend>
        <p>{flag.description} ({flag.service})</p>
        <label>Setting <select aria-label={`${flag.key} setting`} value={override === null ? 'inherit' : override ? 'enabled' : 'disabled'} onChange={e => void save(flag, e.target.value)}>
          <option value="inherit">{scope === '' ? 'Default (disabled)' : 'Inherit global'}</option>
          <option value="enabled">Enabled</option>
          <option value="disabled">Disabled</option>
        </select></label>
        <p>Effective: <strong>{flag.enabled ? 'Enabled' : 'Disabled'}</strong> ({flag.source})</p>
      </fieldset>
    })}
    <p>Flags affect behavior only where their owning service evaluates them. The score-aware rating flag is registered, but its rating consumer is not connected yet.</p>
  </section>
}
