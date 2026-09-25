import { timestampDate } from '@bufbuild/protobuf/wkt'
import { type Connection } from '@/api/connections'
import { SnapshotTrigger, type Snapshot } from '@/api/nocodb'

export function formatDuration(s: Snapshot) {
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
