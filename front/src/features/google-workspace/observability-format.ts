import type { Timestamp } from '@bufbuild/protobuf/wkt'

export function formatWorkspaceTime(value: Timestamp | undefined) {
  if (!value) return '—'
  return new Date(
    Number(value.seconds) * 1000 + value.nanos / 1000000
  ).toLocaleString()
}
export function triState(value: boolean | undefined) {
  return value === undefined ? 'Unknown' : value ? 'Yes' : 'No'
}
export function lastLoginText(value: string) {
  if (!value) return 'Unknown'
  const time = new Date(value)
  if (!Number.isFinite(time.getTime())) return 'Unknown'
  if (time.getTime() <= 0) return 'Never logged in'
  return time.toLocaleString()
}
