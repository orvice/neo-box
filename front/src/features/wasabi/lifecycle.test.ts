import { describe, expect, it } from 'vitest'
import {
  collectAccountLifecycle,
  filterAccountLifecycle,
  formatAbortMultipartAction,
  formatCurrentVersionAction,
  formatNoncurrentVersionAction,
  formatRuleScope,
  isEarlyDeletionRule,
  lifecycleBadge,
  shortestEarlyDeletionDays,
  type BucketForLifecycle,
  type LifecycleRuleView,
} from './lifecycle'

function rule(overrides: Partial<LifecycleRuleView> = {}): LifecycleRuleView {
  return {
    id: 'r1',
    enabled: true,
    prefix: '',
    expirationDays: 0,
    expirationDate: '',
    expiredObjectDeleteMarker: false,
    noncurrentDays: 0,
    newerNoncurrentVersions: 0,
    abortMultipartDays: 0,
    tags: {},
    ...overrides,
  }
}

describe('formatRuleScope', () => {
  it('marks unfiltered rules as all objects', () => {
    expect(formatRuleScope(rule())).toEqual({
      allObjects: true,
      prefix: undefined,
      size: undefined,
      tags: [],
    })
  })

  it('formats prefix, size bounds including 0 bytes, and sorted tags', () => {
    expect(
      formatRuleScope(
        rule({
          prefix: 'logs/',
          objectSizeGreaterThan: 1048576n,
          objectSizeLessThan: 104857600n,
          tags: { z: '1', a: '2' },
        })
      )
    ).toEqual({
      allObjects: false,
      prefix: 'logs/',
      size: '> 1 MB and < 100 MB',
      tags: ['a=2', 'z=1'],
    })

    expect(formatRuleScope(rule({ objectSizeGreaterThan: 0n }))).toMatchObject({
      allObjects: false,
      size: '> 0 B',
    })

    expect(formatRuleScope(rule({ objectSizeLessThan: 512n }))).toMatchObject({
      allObjects: false,
      size: '< 512 B',
    })
  })
})

describe('action formatting', () => {
  it('formats current version expiration by days, date, and delete markers', () => {
    expect(formatCurrentVersionAction(rule())).toBe('-')
    expect(formatCurrentVersionAction(rule({ expirationDays: 1 }))).toBe(
      'After 1 day'
    )
    expect(formatCurrentVersionAction(rule({ expirationDays: 90 }))).toBe(
      'After 90 days'
    )
    expect(
      formatCurrentVersionAction(rule({ expirationDate: '2027-01-15' }))
    ).toBe('On Jan 15, 2027')
    expect(
      formatCurrentVersionAction(rule({ expiredObjectDeleteMarker: true }))
    ).toBe('Remove expired delete markers')
  })

  it('formats noncurrent versions with optional newer version retention', () => {
    expect(formatNoncurrentVersionAction(rule())).toBe('-')
    expect(formatNoncurrentVersionAction(rule({ noncurrentDays: 90 }))).toBe(
      'After 90 days'
    )
    expect(
      formatNoncurrentVersionAction(
        rule({ noncurrentDays: 120, newerNoncurrentVersions: 3 })
      )
    ).toBe('After 120 days (keep newest 3)')
  })

  it('formats abort multipart upload days', () => {
    expect(formatAbortMultipartAction(rule())).toBe('-')
    expect(formatAbortMultipartAction(rule({ abortMultipartDays: 1 }))).toBe(
      'After 1 day'
    )
    expect(formatAbortMultipartAction(rule({ abortMultipartDays: 7 }))).toBe(
      'After 7 days'
    )
  })
})

describe('early deletion detection', () => {
  it('flags enabled rules under 90 days and ignores 90+ or disabled rules', () => {
    expect(isEarlyDeletionRule(rule({ expirationDays: 89 }))).toBe(true)
    expect(
      shortestEarlyDeletionDays(rule({ expirationDays: 30, noncurrentDays: 7 }))
    ).toBe(7)
    expect(
      isEarlyDeletionRule(rule({ expirationDays: 90, noncurrentDays: 90 }))
    ).toBe(false)
    expect(
      isEarlyDeletionRule(rule({ enabled: false, expirationDays: 7 }))
    ).toBe(false)
    expect(isEarlyDeletionRule(rule({ abortMultipartDays: 3 }))).toBe(false)
  })
})

describe('lifecycleBadge', () => {
  it('distinguishes empty, enabled, and all-disabled rule sets', () => {
    expect(lifecycleBadge([])).toBeNull()
    expect(lifecycleBadge([rule({ enabled: false })])).toEqual({
      label: 'Lifecycle (off)',
      active: false,
    })
    expect(
      lifecycleBadge([rule({ enabled: true }), rule({ enabled: false })])
    ).toEqual({
      label: 'Lifecycle (1)',
      active: true,
    })
  })
})

describe('account lifecycle collection and filtering', () => {
  const buckets: BucketForLifecycle[] = [
    {
      name: 'media',
      region: 'us-east-1',
      deleted: false,
      config: {
        errors: {},
        lifecycleRules: [
          rule({ id: 'expire-tmp', prefix: 'tmp/', expirationDays: 30 }),
          rule({ id: 'old-disabled', enabled: false, noncurrentDays: 120 }),
        ],
      },
    },
    {
      name: 'archives',
      region: 'eu-central-1',
      deleted: false,
      config: {
        errors: {},
        lifecycleRules: [
          rule({
            id: 'keep-90d',
            noncurrentDays: 90,
            tags: { team: 'infra' },
          }),
        ],
      },
    },
    {
      name: 'plain',
      region: 'us-east-1',
      deleted: false,
      config: { errors: {}, lifecycleRules: [] },
    },
    {
      name: 'denied',
      region: 'us-east-1',
      deleted: false,
      config: { errors: { lifecycle: 'access_denied' }, lifecycleRules: [] },
    },
    {
      name: 'gone',
      region: 'us-east-1',
      deleted: true,
      config: { errors: {}, lifecycleRules: [rule({ id: 'deleted-bucket' })] },
    },
  ]

  it('aggregates active buckets, skips deleted buckets, and tracks summary counts', () => {
    const { rows, summary } = collectAccountLifecycle(buckets)
    expect(summary).toEqual({
      totalBuckets: 4,
      readableBuckets: 3,
      bucketsWithRules: 2,
      bucketsWithoutRules: 1,
      unreadableBuckets: 1,
      totalRules: 3,
      enabledRules: 2,
      disabledRules: 1,
      earlyDeletionRules: 1,
    })
    expect(rows.map((r) => `${r.bucket}/${r.rule.id}`)).toEqual([
      'archives/keep-90d',
      'media/expire-tmp',
      'media/old-disabled',
    ])
  })

  it('filters by status and search query across bucket, id, prefix, and tags', () => {
    const { rows } = collectAccountLifecycle(buckets)
    expect(filterAccountLifecycle(rows, '', 'enabled')).toHaveLength(2)
    expect(
      filterAccountLifecycle(rows, '', 'disabled').map((r) => r.rule.id)
    ).toEqual(['old-disabled'])
    expect(
      filterAccountLifecycle(rows, '', 'early_deletion').map((r) => r.rule.id)
    ).toEqual(['expire-tmp'])
    expect(
      filterAccountLifecycle(rows, 'tmp/', 'all').map((r) => r.rule.id)
    ).toEqual(['expire-tmp'])
    expect(
      filterAccountLifecycle(rows, 'team=infra', 'all').map((r) => r.bucket)
    ).toEqual(['archives'])
  })
})
