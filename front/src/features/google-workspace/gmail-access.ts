// Logic for Gmail settings access (docs/adr/0008). The backend checks the
// reason and typed target on every request; this module only keeps the
// confirmation for a short while so one page doesn't ask again for each click.
import { useState } from 'react'

export const MAX_REASON = 200
// Client-side convenience only: the backend validates every request itself.
export const CONFIRMATION_TTL_MS = 10 * 60 * 1000

export type GmailConfirmation = {
  reason: string
  targetEmail: string
  confirmedAt: number
}

export function reasonError(reason: string): string {
  const trimmed = reason.trim()
  if (!trimmed) return 'Say why you are accessing this mailbox’s settings'
  if ([...trimmed].length > MAX_REASON)
    return `Keep the reason within ${MAX_REASON} characters`
  // eslint-disable-next-line no-control-regex
  if (/[\u0000-\u001f\u007f]/.test(trimmed)) return 'Use a single line of text'
  return ''
}

export function targetError(typed: string, primaryEmail: string): string {
  if (!typed.trim()) return 'Type the user’s email to confirm'
  if (typed.trim().toLowerCase() !== primaryEmail.toLowerCase())
    return 'This does not match the user’s primary email'
  return ''
}

export function confirmationUsable(
  c: GmailConfirmation | null,
  primaryEmail: string,
  now: number
): c is GmailConfirmation {
  return (
    c !== null &&
    c.targetEmail.trim().toLowerCase() === primaryEmail.toLowerCase() &&
    now - c.confirmedAt >= 0 &&
    now - c.confirmedAt < CONFIRMATION_TTL_MS
  )
}

export function confirmationRemainingMs(
  c: GmailConfirmation | null,
  now: number
): number {
  return c ? Math.max(0, CONFIRMATION_TTL_MS - (now - c.confirmedAt)) : 0
}

export type Exposure = 'external' | 'internal' | 'unknown'

// Unset means the tenant's domains could not be read: never "internal".
export function exposure(external: boolean | undefined): Exposure {
  if (external === undefined) return 'unknown'
  return external ? 'external' : 'internal'
}

export type FilterLike = {
  forwardTo: string
  forwardExternal?: boolean
  deletes: boolean
  skipsInbox: boolean
}

export type FilterRisk = {
  label: string
  tone: 'danger' | 'warning' | 'neutral'
}

// Risks come only from what was read: a filter that forwards to an address
// whose domain is unknown is flagged as such, not as safe.
export function filterRisks(f: FilterLike): FilterRisk[] {
  const out: FilterRisk[] = []
  if (f.forwardTo) {
    const e = exposure(f.forwardExternal)
    out.push(
      e === 'external'
        ? { label: 'Forwards outside the tenant', tone: 'danger' }
        : e === 'unknown'
          ? { label: 'Forwards (destination not checked)', tone: 'warning' }
          : { label: 'Forwards inside the tenant', tone: 'neutral' }
    )
  }
  if (f.deletes) out.push({ label: 'Deletes mail', tone: 'danger' })
  if (f.skipsInbox) out.push({ label: 'Skips the inbox', tone: 'warning' })
  return out
}

export function filterCriteria(f: {
  from: string
  to: string
  subject: string
  query: string
  negatedQuery: string
  hasAttachment: boolean
}): string[] {
  const out: string[] = []
  if (f.from) out.push(`from: ${f.from}`)
  if (f.to) out.push(`to: ${f.to}`)
  if (f.subject) out.push(`subject: ${f.subject}`)
  if (f.query) out.push(`has: ${f.query}`)
  if (f.negatedQuery) out.push(`not: ${f.negatedQuery}`)
  if (f.hasAttachment) out.push('has an attachment')
  return out
}

// <input type="datetime-local"> values are local time without a zone.
export function toLocalInput(date: Date | undefined): string {
  if (!date || !Number.isFinite(date.getTime())) return ''
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`
}

export function fromLocalInput(value: string): Date | undefined {
  if (!value) return undefined
  const d = new Date(value)
  return Number.isFinite(d.getTime()) ? d : undefined
}

export type VacationDraft = {
  enabled: boolean
  subject: string
  body: string
  start: string
  end: string
}

export function vacationError(v: VacationDraft): string {
  if (v.enabled && !v.subject.trim() && !v.body.trim())
    return 'Add a subject or a message to turn the auto-reply on'
  const start = fromLocalInput(v.start)
  const end = fromLocalInput(v.end)
  if (v.start && !start) return 'The start time is not a valid date'
  if (v.end && !end) return 'The end time is not a valid date'
  if (start && end && start.getTime() >= end.getTime())
    return 'The end time must be after the start time'
  return ''
}

export function useGmailConfirmation() {
  const [confirmation, setConfirmation] = useState<GmailConfirmation | null>(
    null
  )
  return { confirmation, setConfirmation }
}
