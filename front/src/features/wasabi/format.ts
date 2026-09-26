import { type Connection } from '@/api/connections'
import { formatBytes } from '@/lib/format'

/** The Wasabi settings of a connection, if it is one. */
export function wasabiConfig(connection: Connection | undefined) {
  return connection?.config.case === 'wasabi'
    ? connection.config.value
    : undefined
}

const usd = new Intl.NumberFormat('en-US', {
  style: 'currency',
  currency: 'USD',
  maximumFractionDigits: 2,
})

export function formatUSD(value: number) {
  return usd.format(value)
}

const compact = new Intl.NumberFormat('en-US', {
  notation: 'compact',
  maximumFractionDigits: 1,
})

/** 1,284 / 12.9K / 4.2M */
export function formatCompact(value: bigint | number) {
  return compact.format(Number(value))
}

/** A UTC day (YYYY-MM-DD) shifted by `days`. */
export function addDays(day: string, days: number) {
  const d = new Date(`${day}T00:00:00Z`)
  d.setUTCDate(d.getUTCDate() + days)
  return d.toISOString().slice(0, 10)
}

/** Today in UTC, the day Wasabi's figures are keyed by. */
export function todayUTC() {
  return new Date().toISOString().slice(0, 10)
}

/** "Sep 24" for chart ticks; days are UTC. */
export function formatDayShort(day: string) {
  return new Date(`${day}T00:00:00Z`).toLocaleDateString('en-US', {
    month: 'short',
    day: 'numeric',
    timeZone: 'UTC',
  })
}

/** "Sep 24, 2026" */
export function formatDayLong(day: string) {
  if (!day) return '-'
  return new Date(`${day}T00:00:00Z`).toLocaleDateString('en-US', {
    month: 'short',
    day: 'numeric',
    year: 'numeric',
    timeZone: 'UTC',
  })
}

/**
 * Axis ticks for a byte scale at round multiples of a 1024-power unit, so
 * they read 0 / 2 TB / 4 TB rather than 1.8 TB / 3.6 TB (a base-10 step
 * shown in base-2 units).
 */
export function byteTicks(max: number, count = 4): number[] {
  if (!(max > 0)) return [0]
  let unit = 1
  while (max / unit >= 1024) unit *= 1024
  const raw = max / unit / count
  const magnitude = 10 ** Math.floor(Math.log10(raw))
  const step = Math.max(
    ([1, 2, 5, 10].find((m) => m * magnitude >= raw) ?? 10) * magnitude * unit,
    1
  )
  const ticks = [0]
  while (ticks[ticks.length - 1] < max) ticks.push(ticks.length * step)
  return ticks
}

/** A tick label: "2 TB", not "2.0 TB". */
export function formatBytesTick(value: number) {
  return formatBytes(value).replace(/\.0 /, ' ')
}
