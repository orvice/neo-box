import { CloudflareEmailLogSchema } from '@/gen/neobox/v1/cloudflare_pb'
import { create } from '@bufbuild/protobuf'
import { describe, expect, it } from 'vitest'
import {
  LOG_RANGES,
  authFailed,
  authSummary,
  emptyLogsMessage,
  eventTypeLabel,
  filterLogs,
  logStatuses,
  ruleLabel,
  statusClass,
  statusLabel,
} from './email-logs'

const log = (
  init: Parameters<typeof create<typeof CloudflareEmailLogSchema>>[1]
) => create(CloudflareEmailLogSchema, init)

describe('ranges', () => {
  it('stays inside the 31 days Cloudflare keeps', () => {
    const day = 24 * 60 * 60 * 1000
    for (const r of LOG_RANGES) expect(r.ms).toBeLessThan(31 * day)
  })
})

describe('wording', () => {
  it('turns a status into words', () => {
    expect(statusLabel('delivered')).toBe('Delivered')
    expect(statusLabel('deliveryFailed')).toBe('Delivery failed')
    expect(statusLabel('')).toBe('-')
  })

  it('colours a status by what it says', () => {
    expect(statusClass('deliveryFailed')).toMatch(/danger/)
    expect(statusClass('rejected')).toMatch(/danger/)
    expect(statusClass('dropped')).toMatch(/warning/)
    expect(statusClass('delivered')).toMatch(/success/)
    expect(statusClass('forwarded')).toMatch(/success/)
    expect(statusClass('somethingNew')).toMatch(/muted/)
  })

  it('names the event kinds', () => {
    expect(eventTypeLabel('incoming')).toBe('Received')
    expect(eventTypeLabel('forward')).toBe('Forwarded')
    expect(eventTypeLabel('newEmail')).toBe('New email')
    expect(eventTypeLabel('')).toBe('-')
  })

  it('summarizes authentication', () => {
    expect(authSummary({ spf: 'pass', dkim: 'fail', dmarc: '' })).toBe(
      'SPF pass · DKIM fail'
    )
    expect(authSummary({ spf: '', dkim: '', dmarc: '' })).toBe('-')
    expect(authFailed({ spf: 'pass', dkim: 'fail', dmarc: '' })).toBe(true)
    expect(authFailed({ spf: 'pass', dkim: 'pass', dmarc: 'pass' })).toBe(false)
  })

  it('names the matched rule by its address', () => {
    const rules = [{ id: 'r1', address: 'info@example.com' }]
    expect(ruleLabel('r1', rules)).toBe('info@example.com')
    expect(ruleLabel('gone', rules)).toBe('gone')
    expect(ruleLabel('r1', undefined)).toBe('r1')
    expect(ruleLabel('', rules)).toBe('-')
  })
})

describe('filtering', () => {
  const logs = [
    log({
      from: 'bob@x.org',
      to: 'info@a.com',
      subject: 'Hello',
      status: 'delivered',
    }),
    log({
      from: 'spam@y.org',
      to: 'x@a.com',
      subject: 'Buy now',
      status: 'dropped',
    }),
    log({
      from: 'c@z.org',
      to: 'info@a.com',
      status: 'deliveryFailed',
      errorDetail: 'mailbox full',
      messageId: '<abc@z.org>',
    }),
  ]

  it('lists the distinct statuses, sorted', () => {
    expect(logStatuses(logs)).toEqual([
      'delivered',
      'deliveryFailed',
      'dropped',
    ])
  })

  it('filters by status', () => {
    expect(filterLogs(logs, { status: 'dropped', search: '' })).toHaveLength(1)
    expect(filterLogs(logs, { status: '', search: '' })).toHaveLength(3)
  })

  it('searches sender, recipient, subject, message ID and error', () => {
    const n = (search: string) =>
      filterLogs(logs, { status: '', search }).length
    expect(n('BOB@')).toBe(1)
    expect(n('info@a.com')).toBe(2)
    expect(n('buy')).toBe(1)
    expect(n('mailbox')).toBe(1)
    expect(n('<abc@z')).toBe(1)
    expect(n('  ')).toBe(3)
    expect(n('nothing')).toBe(0)
  })

  it('combines status and search', () => {
    expect(
      filterLogs(logs, { status: 'delivered', search: 'info' })
    ).toHaveLength(1)
  })

  it('says why a list is empty', () => {
    expect(emptyLogsMessage(0, { status: '', search: '' })).toMatch(
      /in this range/
    )
    expect(emptyLogsMessage(3, { status: 'x', search: '' })).toMatch(/filter/)
  })
})
