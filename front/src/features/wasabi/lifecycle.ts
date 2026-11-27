import { type WasabiBucket, type WasabiLifecycleRule } from '@/api/wasabi'
import { formatBytes } from '@/lib/format'
import { formatDayLong } from './format'

/** Wasabi Pay-Go minimum storage duration; earlier deletes stay billed. */
export const WASABI_MIN_STORAGE_DAYS = 90

/** Wasabi clears incomplete multipart uploads after ~31 days by default. */
export const WASABI_DEFAULT_ABORT_MULTIPART_DAYS = 31

export type LifecycleRuleView = Pick<
  WasabiLifecycleRule,
  | 'id'
  | 'enabled'
  | 'prefix'
  | 'expirationDays'
  | 'expirationDate'
  | 'expiredObjectDeleteMarker'
  | 'noncurrentDays'
  | 'newerNoncurrentVersions'
  | 'abortMultipartDays'
  | 'objectSizeGreaterThan'
  | 'objectSizeLessThan'
  | 'tags'
>

export type LifecycleScope = {
  allObjects: boolean
  prefix?: string
  size?: string
  tags: string[]
}

export type LifecycleFilterStatus =
  'all' | 'enabled' | 'disabled' | 'early_deletion'

export type AccountLifecycleRow = {
  bucket: string
  region: string
  rule: LifecycleRuleView
  ruleIndex: number
  earlyDeletion: boolean
}

export type AccountLifecycleSummary = {
  totalBuckets: number
  readableBuckets: number
  bucketsWithRules: number
  bucketsWithoutRules: number
  unreadableBuckets: number
  totalRules: number
  enabledRules: number
  disabledRules: number
  earlyDeletionRules: number
}

function pluralDays(n: number) {
  return n === 1 ? '1 day' : `${n} days`
}

function formatSizeBound(bytes: bigint) {
  return formatBytes(bytes).replace(/\.0 /, ' ')
}

/** Shortest enabled expiration/noncurrent day count under 90 days, if any. */
export function shortestEarlyDeletionDays(
  rule: Pick<LifecycleRuleView, 'enabled' | 'expirationDays' | 'noncurrentDays'>
): number | undefined {
  if (!rule.enabled) return undefined
  let min: number | undefined
  if (
    rule.expirationDays > 0 &&
    rule.expirationDays < WASABI_MIN_STORAGE_DAYS
  ) {
    min = rule.expirationDays
  }
  if (
    rule.noncurrentDays > 0 &&
    rule.noncurrentDays < WASABI_MIN_STORAGE_DAYS
  ) {
    min =
      min === undefined
        ? rule.noncurrentDays
        : Math.min(min, rule.noncurrentDays)
  }
  return min
}

export function isEarlyDeletionRule(
  rule: Pick<LifecycleRuleView, 'enabled' | 'expirationDays' | 'noncurrentDays'>
): boolean {
  return shortestEarlyDeletionDays(rule) !== undefined
}

export function formatRuleScope(
  rule: Pick<
    LifecycleRuleView,
    'prefix' | 'objectSizeGreaterThan' | 'objectSizeLessThan' | 'tags'
  >
): LifecycleScope {
  const prefix = rule.prefix || undefined
  const gt = rule.objectSizeGreaterThan
  const lt = rule.objectSizeLessThan
  let size: string | undefined
  if (gt !== undefined && lt !== undefined) {
    size = `> ${formatSizeBound(gt)} and < ${formatSizeBound(lt)}`
  } else if (gt !== undefined) {
    size = `> ${formatSizeBound(gt)}`
  } else if (lt !== undefined) {
    size = `< ${formatSizeBound(lt)}`
  }
  const tags = Object.entries(rule.tags ?? {})
    .toSorted(([a], [b]) => a.localeCompare(b))
    .map(([k, v]) => `${k}=${v}`)
  const allObjects = !prefix && !size && tags.length === 0
  return { allObjects, prefix, size, tags }
}

export function formatCurrentVersionAction(
  rule: Pick<
    LifecycleRuleView,
    'expirationDays' | 'expirationDate' | 'expiredObjectDeleteMarker'
  >
): string {
  const parts: string[] = []
  if (rule.expirationDays > 0) {
    parts.push(`After ${pluralDays(rule.expirationDays)}`)
  }
  if (rule.expirationDate) {
    parts.push(`On ${formatDayLong(rule.expirationDate)}`)
  }
  if (rule.expiredObjectDeleteMarker) {
    parts.push('Remove expired delete markers')
  }
  return parts.length > 0 ? parts.join(' · ') : '-'
}

export function formatNoncurrentVersionAction(
  rule: Pick<LifecycleRuleView, 'noncurrentDays' | 'newerNoncurrentVersions'>
): string {
  if (rule.noncurrentDays > 0) {
    const base = `After ${pluralDays(rule.noncurrentDays)}`
    return rule.newerNoncurrentVersions > 0
      ? `${base} (keep newest ${rule.newerNoncurrentVersions})`
      : base
  }
  if (rule.newerNoncurrentVersions > 0) {
    return `Keep newest ${rule.newerNoncurrentVersions}`
  }
  return '-'
}

export function formatAbortMultipartAction(
  rule: Pick<LifecycleRuleView, 'abortMultipartDays'>
): string {
  return rule.abortMultipartDays > 0
    ? `After ${pluralDays(rule.abortMultipartDays)}`
    : '-'
}

export function lifecycleBadge(
  rules: Pick<LifecycleRuleView, 'enabled'>[]
): { label: string; active: boolean } | null {
  if (rules.length === 0) return null
  const enabled = rules.filter((r) => r.enabled).length
  if (enabled > 0) {
    return { label: `Lifecycle (${enabled})`, active: true }
  }
  return { label: 'Lifecycle (off)', active: false }
}

export type BucketForLifecycle = Pick<
  WasabiBucket,
  'name' | 'region' | 'deleted'
> & {
  config?: {
    lifecycleRules: LifecycleRuleView[]
    errors: { [key: string]: string }
  }
}

export function collectAccountLifecycle(buckets: BucketForLifecycle[]): {
  rows: AccountLifecycleRow[]
  summary: AccountLifecycleSummary
} {
  const active = buckets
    .filter((b) => !b.deleted)
    .toSorted((a, b) => a.name.localeCompare(b.name))

  const rows: AccountLifecycleRow[] = []
  let readableBuckets = 0
  let bucketsWithRules = 0
  let bucketsWithoutRules = 0
  let unreadableBuckets = 0
  let enabledRules = 0
  let disabledRules = 0
  let earlyDeletionRules = 0

  for (const b of active) {
    if (!b.config || b.config.errors.lifecycle) {
      unreadableBuckets++
      continue
    }
    readableBuckets++
    const rules = b.config.lifecycleRules
    if (rules.length === 0) {
      bucketsWithoutRules++
      continue
    }
    bucketsWithRules++
    rules.forEach((rule, ruleIndex) => {
      const earlyDeletion = isEarlyDeletionRule(rule)
      if (rule.enabled) enabledRules++
      else disabledRules++
      if (earlyDeletion) earlyDeletionRules++
      rows.push({
        bucket: b.name,
        region: b.region,
        rule,
        ruleIndex,
        earlyDeletion,
      })
    })
  }

  return {
    rows,
    summary: {
      totalBuckets: active.length,
      readableBuckets,
      bucketsWithRules,
      bucketsWithoutRules,
      unreadableBuckets,
      totalRules: rows.length,
      enabledRules,
      disabledRules,
      earlyDeletionRules,
    },
  }
}

export function filterAccountLifecycle(
  rows: AccountLifecycleRow[],
  query: string,
  status: LifecycleFilterStatus
): AccountLifecycleRow[] {
  const q = query.trim().toLowerCase()
  return rows.filter((row) => {
    if (status === 'enabled' && !row.rule.enabled) return false
    if (status === 'disabled' && row.rule.enabled) return false
    if (status === 'early_deletion' && !row.earlyDeletion) return false
    if (!q) return true
    if (row.bucket.toLowerCase().includes(q)) return true
    if (row.rule.id.toLowerCase().includes(q)) return true
    if (row.rule.prefix.toLowerCase().includes(q)) return true
    for (const [k, v] of Object.entries(row.rule.tags ?? {})) {
      if (`${k}=${v}`.toLowerCase().includes(q)) return true
    }
    return false
  })
}
