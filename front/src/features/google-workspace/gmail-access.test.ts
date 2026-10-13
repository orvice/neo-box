import { describe, expect, it } from 'vitest'
import {
  CONFIRMATION_TTL_MS,
  confirmationUsable,
  exposure,
  filterRisks,
  fromLocalInput,
  reasonError,
  targetError,
  toLocalInput,
  vacationError,
} from './gmail-access'

describe('access confirmation', () => {
  it('requires a single-line reason within the limit', () => {
    expect(reasonError('')).not.toBe('')
    expect(reasonError('   ')).not.toBe('')
    expect(reasonError('a\nb')).not.toBe('')
    expect(reasonError('x'.repeat(201))).not.toBe('')
    expect(reasonError('x'.repeat(200))).toBe('')
    expect(reasonError('offboarding check')).toBe('')
  })
  it('matches the typed email to the primary email without regard to case', () => {
    expect(targetError('', 'ada@a.example')).not.toBe('')
    expect(targetError('eve@a.example', 'ada@a.example')).not.toBe('')
    expect(targetError(' ADA@a.example ', 'ada@a.example')).toBe('')
  })
  it('expires and never carries over to another user', () => {
    const c = {
      reason: 'r',
      targetEmail: 'ada@a.example',
      confirmedAt: 1_000,
    }
    expect(confirmationUsable(c, 'ada@a.example', 1_000)).toBe(true)
    expect(
      confirmationUsable(c, 'ada@a.example', 1_000 + CONFIRMATION_TTL_MS - 1)
    ).toBe(true)
    expect(
      confirmationUsable(c, 'ada@a.example', 1_000 + CONFIRMATION_TTL_MS)
    ).toBe(false)
    expect(confirmationUsable(c, 'bob@a.example', 2_000)).toBe(false)
    expect(confirmationUsable(null, 'ada@a.example', 2_000)).toBe(false)
    expect(confirmationUsable(c, 'ada@a.example', 500)).toBe(false)
  })
})

describe('risk flags', () => {
  it('never reads an unchecked destination as internal', () => {
    expect(exposure(undefined)).toBe('unknown')
    expect(exposure(false)).toBe('internal')
    expect(exposure(true)).toBe('external')
  })
  it('flags outside forwarding, deletion and skipping the inbox', () => {
    const base = { forwardTo: '', deletes: false, skipsInbox: false }
    expect(filterRisks(base)).toEqual([])
    expect(
      filterRisks({ ...base, forwardTo: 'x@y', forwardExternal: true })[0].tone
    ).toBe('danger')
    expect(
      filterRisks({ ...base, forwardTo: 'x@y', forwardExternal: false })[0].tone
    ).toBe('neutral')
    const unknown = filterRisks({ ...base, forwardTo: 'x@y' })[0]
    expect(unknown.tone).toBe('warning')
    expect(unknown.label).toMatch(/not checked/)
    expect(filterRisks({ ...base, deletes: true, skipsInbox: true })).toEqual([
      { label: 'Deletes mail', tone: 'danger' },
      { label: 'Skips the inbox', tone: 'warning' },
    ])
  })
})

describe('vacation form', () => {
  const draft = { enabled: true, subject: '', body: '', start: '', end: '' }
  it('needs text to turn on, but not to turn off', () => {
    expect(vacationError(draft)).not.toBe('')
    expect(vacationError({ ...draft, enabled: false })).toBe('')
    expect(vacationError({ ...draft, subject: 'Away' })).toBe('')
  })
  it('requires the end after the start', () => {
    const v = { ...draft, subject: 'Away' }
    expect(
      vacationError({
        ...v,
        start: '2026-10-20T09:00',
        end: '2026-10-20T09:00',
      })
    ).not.toBe('')
    expect(
      vacationError({
        ...v,
        start: '2026-10-20T09:00',
        end: '2026-10-21T09:00',
      })
    ).toBe('')
    expect(vacationError({ ...v, end: 'not a date' })).not.toBe('')
  })
  it('round-trips local date-time input values', () => {
    expect(toLocalInput(undefined)).toBe('')
    const d = fromLocalInput('2026-10-20T09:05')!
    expect(toLocalInput(d)).toBe('2026-10-20T09:05')
    expect(fromLocalInput('')).toBeUndefined()
  })
})
