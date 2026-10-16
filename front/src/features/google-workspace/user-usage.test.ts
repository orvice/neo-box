import { describe, expect, it } from 'vitest'
import {
  activitySummary,
  formatMb,
  gmailGrowth,
  mailboxStats,
  quotaPercent,
  storageSummary,
  usageRows,
  type LabelCount,
  type UsageDay,
} from './user-usage'

const day = (date: string, rest: Partial<UsageDay> = {}): UsageDay => ({
  date,
  ...rest,
})

describe('formatMb', () => {
  it('scales and keeps unknown apart from zero', () => {
    expect(formatMb(0n)).toBe('0 MB')
    expect(formatMb(512)).toBe('512 MB')
    expect(formatMb(1536)).toBe('1.50 GB')
    expect(formatMb(20480)).toBe('20.0 GB')
    expect(formatMb(2 * 1024 * 1024)).toBe('2.00 TB')
    expect(formatMb(undefined)).toBe('—')
  })
})

describe('quotaPercent', () => {
  it('is null when it cannot be known', () => {
    expect(quotaPercent(50, 200)).toBe(25)
    expect(quotaPercent(undefined, 200)).toBeNull()
    expect(quotaPercent(50, undefined)).toBeNull()
    expect(quotaPercent(50, 0)).toBeNull()
    expect(quotaPercent(300, 200)).toBe(100)
    expect(quotaPercent(0, 200)).toBe(0)
  })
})

describe('storageSummary', () => {
  it('uses the newest day with a figure and splits the remainder', () => {
    const s = storageSummary([
      day('2026-10-10', { usedMb: 100n }),
      day('2026-10-11', {
        gmailUsedMb: 300n,
        driveUsedMb: 100n,
        usedMb: 500n,
        totalMb: 1000n,
      }),
      day('2026-10-12', { emailsSent: 3n }),
    ])
    expect(s?.date).toBe('2026-10-11')
    expect(s?.percent).toBe(50)
    expect(s?.split.map((p) => [p.key, p.mb])).toEqual([
      ['gmail', 300],
      ['drive', 100],
      ['other', 100],
    ])
  })

  it('does not invent a total or a percentage', () => {
    const s = storageSummary([day('2026-10-11', { gmailUsedMb: 10n })])
    expect(s?.usedMb).toBe(10)
    expect(s?.totalMb).toBeUndefined()
    expect(s?.percent).toBeNull()
    expect(s?.unlimited).toBe(false)
  })

  it('treats a zero quota as unlimited, not full', () => {
    const s = storageSummary([day('d', { usedMb: 5n, totalMb: 0n })])
    expect(s?.unlimited).toBe(true)
    expect(s?.percent).toBeNull()
  })

  it('is null without any storage figure', () => {
    expect(storageSummary([])).toBeNull()
    expect(storageSummary([day('d', { emailsSent: 1n })])).toBeNull()
  })
})

describe('activitySummary', () => {
  it('averages over reported days only and keeps a reported zero', () => {
    const a = activitySummary([
      day('2026-10-10', { emailsReceived: 10n, emailsSent: 0n }),
      day('2026-10-11', { gmailUsedMb: 5n }),
      day('2026-10-12', {
        emailsReceived: 4n,
        emailsSent: 6n,
        spamReceived: 2n,
      }),
    ])
    expect(a?.reportedDays).toBe(2)
    expect(a?.received).toBe(14)
    expect(a?.sent).toBe(6)
    expect(a?.spam).toBe(2)
    expect(a?.receivedPerDay).toBe(7)
    expect(a?.busiest).toEqual({ date: '2026-10-10', total: 10 })
  })

  it('is null when no day reported counts', () => {
    expect(activitySummary([day('d', { usedMb: 1n })])).toBeNull()
  })
})

describe('gmailGrowth', () => {
  it('measures change per day across reported days', () => {
    const g = gmailGrowth([
      day('2026-10-01', { gmailUsedMb: 100n }),
      day('2026-10-03', { usedMb: 9n }),
      day('2026-10-05', { gmailUsedMb: 140n }),
    ])
    expect(g).toEqual({
      fromDate: '2026-10-01',
      toDate: '2026-10-05',
      deltaMb: 40,
      perDayMb: 10,
    })
  })

  it('needs two reported days', () => {
    expect(gmailGrowth([day('d', { gmailUsedMb: 1n })])).toBeNull()
  })
})

describe('usageRows', () => {
  it('converts storage to bytes and leaves unknowns unset', () => {
    const [r] = usageRows([day('d', { gmailUsedMb: 2n, emailsSent: 0n })])
    expect(r.gmail).toBe(2 * 1024 * 1024)
    expect(r.drive).toBeUndefined()
    expect(r.sent).toBe(0)
    expect(r.received).toBeUndefined()
  })
})

describe('mailboxStats', () => {
  const l = (
    id: string,
    type: string,
    total: number,
    known = true
  ): LabelCount => ({
    id,
    name: id,
    type,
    countsKnown: known,
    messagesTotal: total,
    messagesUnread: 0,
  })

  it('picks system figures and ranks user labels', () => {
    const s = mailboxStats([
      l('INBOX', 'system', 120),
      l('UNREAD', 'system', 7),
      l('SPAM', 'system', 0),
      l('Label_1', 'user', 5),
      l('Label_2', 'user', 50),
      l('Label_3', 'user', 0, false),
    ])
    expect(s.inbox?.messagesTotal).toBe(120)
    expect(s.unread).toBe(7)
    expect(s.spam?.messagesTotal).toBe(0)
    expect(s.sent).toBeUndefined()
    expect(s.userLabels.map((x) => x.id)).toEqual(['Label_2', 'Label_1'])
    expect(s.unknownUserLabels).toBe(1)
  })

  it('does not show unreadable counts as zero', () => {
    expect(mailboxStats([l('INBOX', 'system', 0, false)]).inbox).toBeUndefined()
  })
})
