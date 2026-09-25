import { timestampDate, type Timestamp } from '@bufbuild/protobuf/wkt'
import { SnapshotTrigger, type Snapshot } from '@/api/nocodb'

export function formatTime(ts: Timestamp | undefined) {
  if (!ts) return '-'
  return timestampDate(ts).toLocaleString()
}

export function formatBytes(value: bigint | number) {
  let n = Number(value)
  const units = ['B', 'KB', 'MB', 'GB']
  let i = 0
  while (n >= 1024 && i < units.length - 1) {
    n /= 1024
    i++
  }
  return `${n.toFixed(i === 0 ? 0 : 1)} ${units[i]}`
}

export function formatCount(value: bigint | number) {
  return Number(value).toLocaleString()
}

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
