import { timestampDate, type Timestamp } from '@bufbuild/protobuf/wkt'

export function formatTime(ts: Timestamp | undefined) {
  if (!ts) return '-'
  return timestampDate(ts).toLocaleString()
}

export function formatBytes(value: bigint | number) {
  let n = Number(value)
  const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB']
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
