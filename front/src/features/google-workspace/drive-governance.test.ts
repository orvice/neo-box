import { describe, expect, it } from 'vitest'
import {
  DRIVE_AUDIT_SCOPE,
  DRIVE_USAGE_SCOPE,
  driveEventSummary,
  exposureOf,
  formatQuota,
  formatShare,
  formatUsageMB,
  usageShareTone,
  type DriveEventFields,
} from './drive-governance'

const blank: DriveEventFields = {
  name: '',
  visibility: '',
  oldVisibility: '',
  targetUser: '',
  targetDomain: '',
  target: '',
  oldRole: '',
  newRole: '',
  newOwner: '',
  membershipChangeType: '',
  addedRole: '',
  removedRole: '',
}

describe('exposureOf', () => {
  it('flags public and link exposure', () => {
    expect(exposureOf('public_on_the_web').tone).toBe('danger')
    expect(exposureOf('people_with_link').tone).toBe('warn')
    expect(exposureOf('shared_externally').tone).toBe('warn')
    expect(exposureOf('private').tone).toBe('ok')
    expect(exposureOf('people_within_domain_with_link').label).toBe(
      'Inside the domain'
    )
  })
  it('never shows an unknown value as private', () => {
    for (const v of ['', 'unknown', 'new_google_value']) {
      expect(exposureOf(v)).toEqual({ label: 'Unknown', tone: 'neutral' })
    }
  })
})

describe('driveEventSummary', () => {
  it('describes visibility changes', () => {
    expect(
      driveEventSummary({
        ...blank,
        name: 'change_document_visibility',
        oldVisibility: 'private',
        visibility: 'public_on_the_web',
      })
    ).toBe('Visibility private → public on the web')
  })
  it('keeps missing values as not provided', () => {
    expect(
      driveEventSummary({ ...blank, name: 'change_document_visibility' })
    ).toBe('Visibility Not provided → Not provided')
    expect(driveEventSummary({ ...blank, name: 'change_owner' })).toBe(
      'Ownership → Not provided'
    )
  })
  it('describes access and membership changes', () => {
    expect(
      driveEventSummary({
        ...blank,
        name: 'change_user_access',
        targetUser: 'x@outside.test',
        newRole: 'can_edit',
      })
    ).toBe('x@outside.test: no access → can edit')
    expect(
      driveEventSummary({
        ...blank,
        name: 'shared_drive_membership_change',
        membershipChangeType: 'add_user',
        targetUser: 'a@a.example',
        addedRole: 'manager',
      })
    ).toBe('add user · a@a.example · manager')
  })
  it('falls back to the event name', () => {
    expect(driveEventSummary({ ...blank, name: 'download' })).toBe('download')
  })
})

describe('usage formatting', () => {
  it('never turns missing figures into zero', () => {
    expect(formatUsageMB(undefined)).toBe('Unknown')
    expect(formatUsageMB(0n)).toBe('0 MB')
    expect(formatQuota(undefined)).toBe('Unknown')
    expect(formatQuota(0n)).toBe('Unlimited')
    expect(formatShare(undefined)).toBe('—')
  })
  it('scales in 1024 steps', () => {
    expect(formatUsageMB(512n)).toBe('512 MB')
    expect(formatUsageMB(1536n)).toBe('1.50 GB')
    expect(formatUsageMB(15360n)).toBe('15.0 GB')
    expect(formatUsageMB(1048576n)).toBe('1.00 TB')
    expect(formatQuota(30720n)).toBe('30.0 GB')
  })
  it('formats shares and tones', () => {
    expect(formatShare(4.26)).toBe('4.3%')
    expect(formatShare(90)).toBe('90%')
    expect(usageShareTone(96)).toBe('danger')
    expect(usageShareTone(80)).toBe('warn')
    expect(usageShareTone(10)).toBe('ok')
    expect(usageShareTone(undefined)).toBe('neutral')
  })
})

describe('scopes', () => {
  it('are Reports API scopes only', () => {
    for (const s of [DRIVE_AUDIT_SCOPE, DRIVE_USAGE_SCOPE]) {
      expect(s).toContain('/auth/admin.reports.')
      expect(s).not.toContain('/auth/drive')
    }
  })
})
