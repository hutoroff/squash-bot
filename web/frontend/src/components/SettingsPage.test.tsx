import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import SettingsPage from './SettingsPage'
import type { User } from '../types'
import * as prefsApi from '../api/prefs'

vi.mock('../api/prefs', async () => {
  const actual = await vi.importActual<typeof prefsApi>('../api/prefs')
  return {
    DEFAULT_PREFERENCES: actual.DEFAULT_PREFERENCES,
    fetchMyPreferences: vi.fn(),
    updateDMLanguage: vi.fn(),
    updateResultsOptOut: vi.fn(),
  }
})

const user: User = { telegram_id: 42, first_name: 'Alice' }

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(prefsApi.fetchMyPreferences).mockResolvedValue({
    telegram_id: 42, dm_language: 'de', results_opt_out: false,
  })
})

describe('SettingsPage', () => {
  it('renders the stored preferences', async () => {
    render(<SettingsPage user={user} />)

    await waitFor(() => expect(screen.getByLabelText(/Direct message language/)).toHaveValue('de'))
    expect(screen.getByLabelText(/Don't ask me about game results/)).not.toBeChecked()
  })

  it('falls back to defaults when the user has none yet', async () => {
    vi.mocked(prefsApi.fetchMyPreferences).mockResolvedValue(prefsApi.DEFAULT_PREFERENCES)
    render(<SettingsPage user={user} />)

    await waitFor(() => expect(screen.getByLabelText(/Direct message language/)).toHaveValue('en'))
  })

  it('saves the opt-out optimistically', async () => {
    vi.mocked(prefsApi.updateResultsOptOut).mockResolvedValue()
    render(<SettingsPage user={user} />)

    const toggle = await screen.findByLabelText(/Don't ask me about game results/)
    await userEvent.click(toggle)

    expect(toggle).toBeChecked()
    await waitFor(() => expect(prefsApi.updateResultsOptOut).toHaveBeenCalledWith(true))
  })

  it('reverts and reports when a save fails', async () => {
    vi.mocked(prefsApi.updateDMLanguage).mockRejectedValue(new Error('upstream unavailable'))
    render(<SettingsPage user={user} />)

    const select = await screen.findByLabelText(/Direct message language/)
    await userEvent.selectOptions(select, 'ru')

    await waitFor(() => expect(screen.getByText('upstream unavailable')).toBeInTheDocument())
    expect(select).toHaveValue('de')
  })
})
