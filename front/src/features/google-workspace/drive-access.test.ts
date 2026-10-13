import { describe, expect, it } from 'vitest'
import {
  DRIVE_METADATA_SCOPE,
  DRIVE_READONLY_SCOPE,
  exposureText,
  exposureTone,
  fileRisk,
  mimeLabel,
  orgUnitIdError,
  parseMaxMembers,
  reasonUsable,
  restrictionLabels,
  safeLink,
} from './drive-access'
import { CONFIRMATION_TTL_MS, reasonError } from './gmail-access'

const none = {
  domainUsersOnly: false,
  driveMembersOnly: false,
  adminManagedRestrictions: false,
  copyRequiresWriterPermission: false,
  sharingFoldersRequiresOrganizerPermission: false,
}

describe('reasonError subject', () => {
  it('keeps the Gmail wording by default and names another subject', () => {
    expect(reasonError('')).toContain('mailbox')
    expect(reasonError('', 'this user’s files')).toContain('this user’s files')
    expect(reasonError('ok', 'x')).toBe('')
  })
})

describe('reasonUsable', () => {
  const c = { reason: 'r', targetEmail: '', confirmedAt: 1000 }
  it('expires after the TTL and rejects a future timestamp', () => {
    expect(reasonUsable(null, 1000)).toBe(false)
    expect(reasonUsable(c, 1000 + CONFIRMATION_TTL_MS - 1)).toBe(true)
    expect(reasonUsable(c, 1000 + CONFIRMATION_TTL_MS)).toBe(false)
    expect(reasonUsable(c, 999)).toBe(false)
  })
})

describe('restrictionLabels', () => {
  it('lists only restrictions Google reported as set', () => {
    expect(restrictionLabels(undefined)).toEqual([])
    expect(restrictionLabels(none)).toEqual([])
    expect(
      restrictionLabels({
        ...none,
        domainUsersOnly: true,
        driveMembersOnly: true,
      })
    ).toEqual(['Domain users only', 'Members only'])
  })
})

describe('exposures', () => {
  it('flags public and other-domain exposure, and unchecked domains', () => {
    expect(
      exposureTone({ kind: 'anyone_with_link', role: 'reader', domain: '' })
    ).toBe('danger')
    expect(
      exposureTone({
        kind: 'domain_with_link',
        role: 'reader',
        domain: 'a.example',
        external: false,
      })
    ).toBe('neutral')
    expect(
      exposureTone({
        kind: 'domain_can_find',
        role: 'reader',
        domain: 'x.test',
        external: true,
      })
    ).toBe('danger')
    expect(
      exposureTone({
        kind: 'domain_with_link',
        role: 'reader',
        domain: 'x.test',
      })
    ).toBe('warning')
  })
  it('words the exposure without guessing', () => {
    expect(
      exposureText({ kind: 'anyone_can_find', role: 'reader', domain: '' })
    ).toBe('Anyone can find · reader')
    expect(
      exposureText({
        kind: 'domain_can_find',
        role: 'writer',
        domain: 'x.test',
        external: true,
      })
    ).toBe('Domain can find · x.test (outside the tenant) · writer')
    expect(
      exposureText({ kind: 'domain_with_link', role: '', domain: 'x.test' })
    ).toBe('Domain with the link · x.test (not checked)')
    expect(exposureText({ kind: 'surprise', role: '', domain: '' })).toBe(
      'Unknown'
    )
  })
  it('takes a file’s worst exposure and treats none as no claim', () => {
    expect(fileRisk({ exposures: [] })).toBe('neutral')
    expect(
      fileRisk({
        exposures: [
          { kind: 'domain_with_link', role: '', domain: 'a', external: false },
        ],
      })
    ).toBe('neutral')
    expect(
      fileRisk({
        exposures: [{ kind: 'domain_with_link', role: '', domain: 'a' }],
      })
    ).toBe('warning')
    expect(
      fileRisk({
        exposures: [
          { kind: 'domain_with_link', role: '', domain: 'a' },
          { kind: 'anyone_with_link', role: '', domain: '' },
        ],
      })
    ).toBe('danger')
  })
})

describe('helpers', () => {
  it('shortens Google file types', () => {
    expect(mimeLabel('application/vnd.google-apps.spreadsheet')).toBe(
      'spreadsheet'
    )
    expect(mimeLabel('application/pdf')).toBe('pdf')
    expect(mimeLabel('')).toBe('Not provided')
    expect(mimeLabel('image/png')).toBe('image/png')
  })
  it('opens only https links', () => {
    expect(safeLink('https://docs.google.com/x')).toBe(
      'https://docs.google.com/x'
    )
    expect(safeLink('javascript:alert(1)')).toBe('')
    expect(safeLink('http://x')).toBe('')
  })
  it('validates the member limit and org unit ID', () => {
    expect(parseMaxMembers('')).toEqual({ error: '' })
    expect(parseMaxMembers(' 3 ')).toEqual({ value: 3, error: '' })
    expect(parseMaxMembers('0')).toEqual({ value: 0, error: '' })
    expect(parseMaxMembers('-1').error).not.toBe('')
    expect(parseMaxMembers('1.5').error).not.toBe('')
    expect(parseMaxMembers('100001').error).not.toBe('')
    expect(orgUnitIdError('')).toBe('')
    expect(orgUnitIdError('id:03ph8a2z')).toBe('')
    expect(orgUnitIdError("x' or 1=1")).not.toBe('')
  })
  it('names the two read-only scopes and never the full Drive scope', () => {
    expect(DRIVE_READONLY_SCOPE).toMatch(/drive\.readonly$/)
    expect(DRIVE_METADATA_SCOPE).toMatch(/drive\.metadata\.readonly$/)
  })
})
