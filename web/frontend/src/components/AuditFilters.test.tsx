import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import AuditFiltersForm from './AuditFilters'
import type { AuditFilters } from '../types'

function renderFilters(isServerOwner = false, onApply = vi.fn()) {
  render(<AuditFiltersForm isServerOwner={isServerOwner} onApply={onApply} />)
  return onApply
}

describe('AuditFiltersForm', () => {
  it('sends to as exclusive next-day midnight', () => {
    const onApply = vi.fn()
    renderFilters(false, onApply)

    fireEvent.change(screen.getByLabelText(/^To/), { target: { value: '2026-04-01' } })
    fireEvent.click(screen.getByRole('button', { name: /Apply filters/ }))

    const filters: AuditFilters = onApply.mock.calls[0][0]
    // Backend uses occurred_at < to (exclusive), so to must be start of next day.
    expect(filters.to).toBeDefined()
    const sent = new Date(filters.to!)
    expect(sent.toISOString().startsWith('2026-04-02')).toBe(true)
  })

  it('sends from as start of selected day', () => {
    const onApply = vi.fn()
    renderFilters(false, onApply)

    fireEvent.change(screen.getByLabelText(/^From/), { target: { value: '2026-04-01' } })
    fireEvent.click(screen.getByRole('button', { name: /Apply filters/ }))

    const filters: AuditFilters = onApply.mock.calls[0][0]
    expect(filters.from).toBeDefined()
    expect(filters.from!.startsWith('2026-04-01')).toBe(true)
  })

  it('omits to when not set', () => {
    const onApply = vi.fn()
    renderFilters(false, onApply)
    fireEvent.click(screen.getByRole('button', { name: /Apply filters/ }))
    expect(onApply.mock.calls[0][0].to).toBeUndefined()
  })
})
