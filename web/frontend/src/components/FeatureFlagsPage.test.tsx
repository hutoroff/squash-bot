import { beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import FeatureFlagsPage from './FeatureFlagsPage'
import * as flagsApi from '../api/featureFlags'
import { fetchGroups } from '../api/groups'
import type { FeatureFlag } from '../api/featureFlags'

vi.mock('../api/featureFlags', () => ({ fetchFeatureFlags: vi.fn(), setFeatureFlag: vi.fn() }))
vi.mock('../api/groups', () => ({ fetchGroups: vi.fn() }))
const base: FeatureFlag = { key: 'rating.score_aware', description: 'Experimental score ratings', service: 'management', default: false, group_scoped: true, global: null, override: null, enabled: false, source: 'default' }

beforeEach(() => {
  vi.resetAllMocks()
  vi.mocked(fetchGroups).mockResolvedValue([{ chat_id: -1, title: 'Squash group' }] as Awaited<ReturnType<typeof fetchGroups>>)
  vi.mocked(flagsApi.fetchFeatureFlags).mockResolvedValue([{ ...base }])
  vi.mocked(flagsApi.setFeatureFlag).mockResolvedValue(undefined)
})

describe('FeatureFlagsPage', () => {
  it('shows disabled defaults without saving anything', async () => {
    render(<FeatureFlagsPage />)
    expect(await screen.findByLabelText('rating.score_aware setting')).toHaveValue('inherit')
    expect(screen.getByText('Disabled', { selector: 'strong' })).toBeInTheDocument()
    expect(flagsApi.setFeatureFlag).not.toHaveBeenCalled()
  })

  it('saves global enable and reloads the effective value', async () => {
    render(<FeatureFlagsPage />)
    const select = await screen.findByLabelText('rating.score_aware setting')
    vi.mocked(flagsApi.fetchFeatureFlags).mockResolvedValue([{ ...base, global: true, enabled: true, source: 'global' }])
    fireEvent.change(select, { target: { value: 'enabled' } })
    await waitFor(() => expect(flagsApi.setFeatureFlag).toHaveBeenCalledWith('rating.score_aware', true, undefined))
    await waitFor(() => expect(select).toHaveValue('enabled'))
    expect(screen.getByText('Enabled', { selector: 'strong' })).toBeInTheDocument()
  })

  it('supports a group disable and reset to inheritance', async () => {
    vi.mocked(flagsApi.fetchFeatureFlags).mockImplementation(async group => [{ ...base, global: true, override: group === undefined ? null : false, enabled: group === undefined, source: group === undefined ? 'global' : 'group' }])
    render(<FeatureFlagsPage />)
    await screen.findByLabelText('rating.score_aware setting')
    fireEvent.change(screen.getByLabelText('Scope'), { target: { value: '-1' } })
    const select = await screen.findByLabelText('rating.score_aware setting')
    expect(select).toHaveValue('disabled')
    vi.mocked(flagsApi.fetchFeatureFlags).mockResolvedValue([{ ...base, global: true, enabled: true, source: 'global' }])
    fireEvent.change(select, { target: { value: 'inherit' } })
    await waitFor(() => expect(flagsApi.setFeatureFlag).toHaveBeenCalledWith('rating.score_aware', null, -1))
    await waitFor(() => expect(select).toHaveValue('inherit'))
    fireEvent.change(select, { target: { value: 'disabled' } })
    await waitFor(() => expect(flagsApi.setFeatureFlag).toHaveBeenCalledWith('rating.score_aware', false, -1))
  })

  it('does not invent disabled state on a failed read', async () => {
    vi.mocked(flagsApi.fetchFeatureFlags).mockRejectedValue(new Error('Forbidden'))
    render(<FeatureFlagsPage />)
    expect(await screen.findByRole('alert')).toHaveTextContent('Forbidden')
    expect(screen.queryByLabelText('rating.score_aware setting')).not.toBeInTheDocument()
    vi.mocked(flagsApi.fetchFeatureFlags).mockResolvedValue([{ ...base }])
    fireEvent.click(screen.getByRole('button', { name: 'Reload' }))
    expect(await screen.findByLabelText('rating.score_aware setting')).toHaveValue('inherit')
  })

  it('blocks scope changes while saving and reloads after failure', async () => {
    let rejectSave!: (reason: Error) => void
    vi.mocked(flagsApi.setFeatureFlag).mockImplementation(() => new Promise((_, reject) => { rejectSave = reject }))
    render(<FeatureFlagsPage />)
    fireEvent.change(await screen.findByLabelText('rating.score_aware setting'), { target: { value: 'enabled' } })
    expect(screen.getByLabelText('Scope')).toBeDisabled()
    rejectSave(new Error('Save failed'))
    expect(await screen.findByRole('alert')).toHaveTextContent('Save failed')
    expect(screen.queryByLabelText('rating.score_aware setting')).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Reload' }))
    expect(await screen.findByLabelText('rating.score_aware setting')).toHaveValue('inherit')
  })
})
