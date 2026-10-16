// Figures for a user's storage and Gmail activity. Google's usage reports lag
// by days and leave out what they do not know, so every helper keeps "not
// reported" apart from zero: an unknown figure is undefined or null, never 0.

export type UsageDay = {
  date: string
  gmailUsedMb?: bigint
  driveUsedMb?: bigint
  photosUsedMb?: bigint
  usedMb?: bigint
  totalMb?: bigint
  emailsReceived?: bigint
  emailsSent?: bigint
  spamReceived?: bigint
}

const MB = 1024 * 1024

const num = (v: bigint | undefined) => (v === undefined ? undefined : Number(v))

/** Megabytes as a size: 512 MB, 1.5 GB, 2.00 TB. */
export function formatMb(mb: number | bigint | undefined): string {
  if (mb === undefined) return '—'
  const n = Number(mb)
  if (!Number.isFinite(n) || n < 0) return '—'
  if (n < 1024) return `${Math.round(n)} MB`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(n < 10240 ? 2 : 1)} GB`
  return `${(n / 1024 / 1024).toFixed(2)} TB`
}

/** Share of a quota, 0-100. Null when either figure is unknown or unlimited. */
export function quotaPercent(
  used: number | undefined,
  total: number | undefined
): number | null {
  if (used === undefined || total === undefined || total <= 0) return null
  return Math.min(100, (used / total) * 100)
}

export type StorageSplit = {
  key: 'gmail' | 'drive' | 'photos' | 'other'
  label: string
  mb: number
}

export type StorageSummary = {
  date: string
  usedMb?: number
  totalMb?: number
  /** Null when the quota is unknown or unlimited. */
  percent: number | null
  /** True when the total quota is reported as zero or less: no cap. */
  unlimited: boolean
  /** Parts Google reported, largest first; "other" holds the remainder. */
  split: StorageSplit[]
}

/** The newest day with any storage figure, or null when there is none. */
export function storageSummary(days: UsageDay[]): StorageSummary | null {
  for (let i = days.length - 1; i >= 0; i--) {
    const d = days[i]
    const parts = [d.gmailUsedMb, d.driveUsedMb, d.photosUsedMb, d.usedMb]
    if (parts.every((p) => p === undefined) && d.totalMb === undefined) continue
    const gmail = num(d.gmailUsedMb)
    const drive = num(d.driveUsedMb)
    const photos = num(d.photosUsedMb)
    const known = (gmail ?? 0) + (drive ?? 0) + (photos ?? 0)
    // Google's own total when it gave one; otherwise the sum of known parts.
    const used = num(d.usedMb) ?? (known > 0 ? known : undefined)
    const total = num(d.totalMb)
    const split: StorageSplit[] = []
    if (gmail !== undefined)
      split.push({ key: 'gmail', label: 'Gmail', mb: gmail })
    if (drive !== undefined)
      split.push({ key: 'drive', label: 'Drive', mb: drive })
    if (photos !== undefined)
      split.push({ key: 'photos', label: 'Photos', mb: photos })
    if (used !== undefined && used > known)
      split.push({ key: 'other', label: 'Other', mb: used - known })
    split.sort((a, b) => b.mb - a.mb)
    return {
      date: d.date,
      usedMb: used,
      totalMb: total,
      percent: quotaPercent(used, total),
      unlimited: total !== undefined && total <= 0,
      split,
    }
  }
  return null
}

export type ActivitySummary = {
  /** Days that reported message counts. */
  reportedDays: number
  received: number
  sent: number
  spam: number
  /** Average per reported day, rounded. */
  receivedPerDay: number
  sentPerDay: number
  busiest?: { date: string; total: number }
}

/** Totals over the days that reported counts; null when none did. */
export function activitySummary(days: UsageDay[]): ActivitySummary | null {
  let reported = 0
  let received = 0
  let sent = 0
  let spam = 0
  let busiest: ActivitySummary['busiest']
  for (const d of days) {
    if (d.emailsReceived === undefined && d.emailsSent === undefined) continue
    reported++
    const r = Number(d.emailsReceived ?? 0n)
    const s = Number(d.emailsSent ?? 0n)
    received += r
    sent += s
    spam += Number(d.spamReceived ?? 0n)
    if (!busiest || r + s > busiest.total)
      busiest = { date: d.date, total: r + s }
  }
  if (reported === 0) return null
  return {
    reportedDays: reported,
    received,
    sent,
    spam,
    receivedPerDay: Math.round(received / reported),
    sentPerDay: Math.round(sent / reported),
    busiest,
  }
}

export type GmailGrowth = {
  fromDate: string
  toDate: string
  /** Change in Gmail storage across the shown days, in MB (may be negative). */
  deltaMb: number
  /** Average change per day between the first and last reported days. */
  perDayMb: number
}

/** How Gmail storage moved across the days shown; null with fewer than two. */
export function gmailGrowth(days: UsageDay[]): GmailGrowth | null {
  const withGmail = days.filter((d) => d.gmailUsedMb !== undefined)
  if (withGmail.length < 2) return null
  const first = withGmail[0]
  const last = withGmail[withGmail.length - 1]
  const delta = Number(last.gmailUsedMb) - Number(first.gmailUsedMb)
  const span = Math.max(
    1,
    Math.round(
      (Date.parse(`${last.date}T00:00:00Z`) -
        Date.parse(`${first.date}T00:00:00Z`)) /
        86_400_000
    )
  )
  return {
    fromDate: first.date,
    toDate: last.date,
    deltaMb: delta,
    perDayMb: delta / span,
  }
}

/** Gmail's chart row: bytes for the byte axis, counts as plain numbers. */
export type UsageRow = {
  day: string
  gmail?: number
  drive?: number
  photos?: number
  received?: number
  sent?: number
}

export function usageRows(days: UsageDay[]): UsageRow[] {
  return days.map((d) => ({
    day: d.date,
    gmail: d.gmailUsedMb === undefined ? undefined : Number(d.gmailUsedMb) * MB,
    drive: d.driveUsedMb === undefined ? undefined : Number(d.driveUsedMb) * MB,
    photos:
      d.photosUsedMb === undefined ? undefined : Number(d.photosUsedMb) * MB,
    received: num(d.emailsReceived),
    sent: num(d.emailsSent),
  }))
}

// --- mailbox label figures (read after the access confirmation) ---

export type LabelCount = {
  id: string
  name: string
  type: string
  countsKnown: boolean
  messagesTotal: number
  messagesUnread: number
}

export type MailboxStats = {
  inbox?: LabelCount
  unread?: number
  sent?: LabelCount
  drafts?: LabelCount
  spam?: LabelCount
  trash?: LabelCount
  starred?: LabelCount
  /** User labels with known counts, largest first. */
  userLabels: LabelCount[]
  /** User labels whose counts Gmail would not give. */
  unknownUserLabels: number
}

/**
 * Picks the figures worth showing from a label list. Counts Gmail would not
 * give are left out rather than shown as zero.
 */
export function mailboxStats(labels: LabelCount[]): MailboxStats {
  const byId = new Map(labels.map((l) => [l.id, l]))
  const known = (id: string) => {
    const l = byId.get(id)
    return l && l.countsKnown ? l : undefined
  }
  const user = labels.filter((l) => l.type === 'user')
  return {
    inbox: known('INBOX'),
    unread: known('UNREAD')?.messagesTotal,
    sent: known('SENT'),
    drafts: known('DRAFT'),
    spam: known('SPAM'),
    trash: known('TRASH'),
    starred: known('STARRED'),
    userLabels: user
      .filter((l) => l.countsKnown)
      .sort((a, b) => b.messagesTotal - a.messagesTotal),
    unknownUserLabels: user.filter((l) => !l.countsKnown).length,
  }
}
