// Wording and levels for the tenant Storage tab. Google reports each user's
// quota; Neo Box only compares usage with it. A figure Google did not report is
// unknown, never zero, and a user with no quota has no level.

export type StorageLevel = 'over' | 'near' | 'ok' | 'none'

/** Where a usage percentage stands against a threshold. */
export function storageLevel(
  percent: number | undefined,
  threshold: number
): StorageLevel {
  if (percent === undefined) return 'none'
  if (percent >= 100) return 'over'
  if (percent >= threshold) return 'near'
  return 'ok'
}

export function formatPercent(percent: number | undefined): string {
  if (percent === undefined) return '—'
  return `${percent.toFixed(percent < 10 ? 1 : 0)}%`
}

/** A unit path for display; users the Directory did not list have none. */
export function orgUnitLabel(path: string): string {
  if (path === '') return 'Unknown unit'
  if (path === '/') return '/ (top level)'
  return path
}

/** An unlimited or unreported quota is not a number of megabytes. */
export function quotaText(
  quotaMb: bigint | undefined,
  mixed: boolean,
  format: (mb: bigint) => string
): string {
  if (mixed) return 'Mixed'
  if (quotaMb === undefined) return 'Not reported'
  return format(quotaMb)
}

export const THRESHOLDS = [70, 80, 90, 95] as const

/** Where users change limits: Google has no API for it. */
export const ADMIN_STORAGE_URL = 'https://admin.google.com/ac/storage'
