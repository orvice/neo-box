// Helpers that translate proto-generated types (camelCase + Timestamp/Duration
// wrappers) into the plain shapes the hand-written API interfaces expose.
import {
  timestampDate,
  durationMs,
  type Timestamp,
  type Duration,
} from '@bufbuild/protobuf/wkt'

export function tsToISO(ts: Timestamp | undefined): string | undefined {
  if (!ts) return undefined
  return timestampDate(ts).toISOString()
}

export function durationToString(d: Duration | undefined): string | undefined {
  if (!d) return undefined
  const ms = durationMs(d)
  // Match Go protojson's Duration encoding ("3.5s") for display compatibility
  // with the existing dashboard parsers.
  const secs = ms / 1000
  return `${secs}s`
}

// bigintToNumber narrows protobuf int64 (bigint in protobuf-es) to number.
// All of our int64 fields fit comfortably under Number.MAX_SAFE_INTEGER —
// they're sizes, counts, latencies — so the conversion is lossless in
// practice.
export function bigintToNumber(v: bigint | undefined): number | undefined {
  if (v === undefined) return undefined
  return Number(v)
}
