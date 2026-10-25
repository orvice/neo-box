import type { CloudflareEmailLog, CloudflareEmailRule } from '@/api/cloudflare'

// Wording and filtering for a Zone's Email Routing logs. The events come
// from Cloudflare's GraphQL Analytics API, which keeps them for 31 days.

const HOUR = 60 * 60 * 1000
const DAY = 24 * HOUR

/** The ranges offered; the longest stays inside Cloudflare's 31 days. */
export const LOG_RANGES = [
  { label: 'Last hour', ms: HOUR },
  { label: 'Last 24 hours', ms: DAY },
  { label: 'Last 7 days', ms: 7 * DAY },
  { label: 'Last 30 days', ms: 30 * DAY },
] as const

export const LOG_LIMITS = [50, 100, 250, 500] as const

export const DEFAULT_LOG_RANGE_MS = DAY
export const DEFAULT_LOG_LIMIT = 100

/** The token permission the logs need. */
export const LOGS_PERMISSION = 'Analytics · Read'

/** Cloudflare's status as words: "deliveryFailed" is "Delivery failed". */
export function statusLabel(status: string): string {
  if (!status) return '-'
  const words = status
    .replace(/([a-z0-9])([A-Z])/g, '$1 $2')
    .replace(/[_-]+/g, ' ')
    .toLowerCase()
  return words.charAt(0).toUpperCase() + words.slice(1)
}

/**
 * The colour of a status badge. Cloudflare's statuses are not a closed set
 * Neo Box knows, so this goes by what the word says and leaves the rest
 * neutral.
 */
export function statusClass(status: string): string {
  const s = status.toLowerCase()
  if (/fail|reject|error|bounce|block/.test(s))
    return 'bg-danger-muted text-danger-foreground'
  if (/drop|spam|pending|defer/.test(s))
    return 'bg-warning-muted text-warning-foreground'
  if (/deliver|forward|handled|sent|accept/.test(s))
    return 'bg-success-muted text-success-foreground'
  return 'bg-muted text-muted-foreground'
}

/** The event kinds in words. */
export function eventTypeLabel(t: string): string {
  switch (t) {
    case 'incoming':
      return 'Received'
    case 'forward':
      return 'Forwarded'
    case 'reply':
      return 'Reply'
    case 'newEmail':
      return 'New email'
  }
  return t ? statusLabel(t) : '-'
}

/** SPF, DKIM and DMARC on one line, leaving out what Cloudflare did not say. */
export function authSummary(
  l: Pick<CloudflareEmailLog, 'spf' | 'dkim' | 'dmarc'>
): string {
  const parts = [
    l.spf && `SPF ${l.spf}`,
    l.dkim && `DKIM ${l.dkim}`,
    l.dmarc && `DMARC ${l.dmarc}`,
  ].filter(Boolean)
  return parts.length > 0 ? parts.join(' · ') : '-'
}

/** True when an authentication check failed. */
export function authFailed(
  l: Pick<CloudflareEmailLog, 'spf' | 'dkim' | 'dmarc'>
): boolean {
  return [l.spf, l.dkim, l.dmarc].some((v) => /fail/i.test(v))
}

/**
 * The rule that matched, as its address; an unknown ID (a deleted rule, or
 * rules not read yet) is shown as the ID, and no match as "-".
 */
export function ruleLabel(
  ruleId: string,
  rules: Pick<CloudflareEmailRule, 'id' | 'address'>[] | undefined
): string {
  if (!ruleId) return '-'
  return rules?.find((r) => r.id === ruleId)?.address || ruleId
}

/** The distinct statuses in the events, sorted. */
export function logStatuses(logs: Pick<CloudflareEmailLog, 'status'>[]) {
  return [...new Set(logs.map((l) => l.status).filter(Boolean))].sort((a, b) =>
    a.localeCompare(b)
  )
}

/**
 * Narrows events by status ('' is any) and by a search matched, ignoring
 * case, against sender, recipient, subject, message ID and error detail.
 */
export function filterLogs(
  logs: CloudflareEmailLog[],
  f: { status: string; search: string }
): CloudflareEmailLog[] {
  const q = f.search.trim().toLowerCase()
  return logs.filter((l) => {
    if (f.status && l.status !== f.status) return false
    if (!q) return true
    return [l.from, l.to, l.subject, l.messageId, l.errorDetail].some((v) =>
      v.toLowerCase().includes(q)
    )
  })
}

/** What an empty list means, given what was asked. */
export function emptyLogsMessage(
  total: number,
  f: { status: string; search: string }
): string {
  if (total > 0 && (f.status || f.search.trim()))
    return 'No events match the filter'
  return 'No Email Routing events in this range'
}
