import { timestampDate, type Timestamp } from '@bufbuild/protobuf/wkt'
import { type Connection } from '@/api/connections'
import { SnapshotTrigger, type Snapshot } from '@/api/nocodb'

/** How long a snapshot or restore ran. */
export function formatDuration(s: {
  startedAt?: Timestamp
  finishedAt?: Timestamp
}) {
  if (!s.startedAt || !s.finishedAt) return '-'
  const ms =
    timestampDate(s.finishedAt).getTime() - timestampDate(s.startedAt).getTime()
  if (ms < 1000) return `${ms} ms`
  const secs = Math.round(ms / 1000)
  if (secs < 60) return `${secs}s`
  return `${Math.floor(secs / 60)}m ${secs % 60}s`
}

export function triggerLabel(t: SnapshotTrigger) {
  return t === SnapshotTrigger.SCHEDULED ? 'Scheduled' : 'Manual'
}

/** The instance URL of a NocoDB connection. */
export function nocodbBaseUrl(connection: Connection | undefined) {
  return connection?.config.case === 'nocodb'
    ? connection.config.value.baseUrl
    : ''
}

/** A Base's page in the NocoDB (OSS) dashboard. */
export function nocodbBaseLink(instanceUrl: string, baseId: string) {
  return instanceUrl && baseId
    ? `${instanceUrl.replace(/\/+$/, '')}/dashboard/#/nc/${baseId}`
    : ''
}

// NocoDB's rule for Base names, as the server checks it.
export const BASE_TITLE_MAX = 150
const baseTitleChar = /[\p{L}\p{N}\s\-_.()&,']/u

export function isValidBaseTitle(title: string) {
  return (
    title.trim() !== '' &&
    [...title].length <= BASE_TITLE_MAX &&
    [...title].every((c) => baseTitleChar.test(c))
  )
}

/** The server's default title for a restore of s. */
export function defaultRestoreTitle(s: Snapshot) {
  const date = s.createdAt
    ? timestampDate(s.createdAt).toISOString().slice(0, 10)
    : ''
  const suffix = ` (restored from ${date})`
  const cleaned = [...(s.baseTitle ?? '')]
    .map((c) => (baseTitleChar.test(c) && !/\s/.test(c) ? c : ' '))
    .join('')
    .split(/\s+/)
    .filter(Boolean)
    .join(' ')
  const title = [...cleaned]
    .slice(0, BASE_TITLE_MAX - suffix.length)
    .join('')
    .trim()
  return (title || s.baseId) + suffix
}
