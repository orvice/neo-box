// Logic for Drive API reads (docs/adr/0009). The backend checks the reason (and,
// for one user's files, the typed primary email) on every request and records
// each read before sending it; this module only holds a confirmation for a
// short while so one page doesn't ask again for each click.
import { CONFIRMATION_TTL_MS, type GmailConfirmation } from './gmail-access'

export const DRIVE_READONLY_SCOPE =
  'https://www.googleapis.com/auth/drive.readonly'
export const DRIVE_METADATA_SCOPE =
  'https://www.googleapis.com/auth/drive.metadata.readonly'

// Same shape as the Gmail confirmation: a reason, a typed email (empty for
// the tenant-wide shared drive list) and when it was given.
export type DriveConfirmation = GmailConfirmation

export function reasonUsable(
  c: DriveConfirmation | null,
  now: number
): c is DriveConfirmation {
  return (
    c !== null &&
    now - c.confirmedAt >= 0 &&
    now - c.confirmedAt < CONFIRMATION_TTL_MS
  )
}

export type Tone = 'danger' | 'warning' | 'neutral'

export type RestrictionsLike = {
  domainUsersOnly: boolean
  driveMembersOnly: boolean
  adminManagedRestrictions: boolean
  copyRequiresWriterPermission: boolean
  sharingFoldersRequiresOrganizerPermission: boolean
}

// Only restrictions Google reported as set are listed. A restriction that is
// not listed is not claimed to be off.
export function restrictionLabels(r: RestrictionsLike | undefined): string[] {
  if (!r) return []
  const out: string[] = []
  if (r.domainUsersOnly) out.push('Domain users only')
  if (r.driveMembersOnly) out.push('Members only')
  if (r.adminManagedRestrictions) out.push('Admin-managed restrictions')
  if (r.copyRequiresWriterPermission) out.push('No copy for readers')
  if (r.sharingFoldersRequiresOrganizerPermission)
    out.push('Only organizers share folders')
  return out
}

export const EXPOSURE_KINDS = [
  { kind: 'anyone_can_find', label: 'Anyone can find' },
  { kind: 'anyone_with_link', label: 'Anyone with the link' },
  { kind: 'domain_can_find', label: 'Domain can find' },
  { kind: 'domain_with_link', label: 'Domain with the link' },
] as const

export type ExposureLike = {
  kind: string
  role: string
  domain: string
  external?: boolean
}

export function exposureKindLabel(kind: string): string {
  return EXPOSURE_KINDS.find((k) => k.kind === kind)?.label ?? 'Unknown'
}

// Public exposure and sharing with another domain are the dangerous ones; a
// domain exposure whose domain could not be checked is flagged as such, not as
// internal.
export function exposureTone(e: ExposureLike): Tone {
  if (e.kind.startsWith('anyone_')) return 'danger'
  if (e.external === undefined) return 'warning'
  return e.external ? 'danger' : 'neutral'
}

export function exposureText(e: ExposureLike): string {
  const parts = [exposureKindLabel(e.kind)]
  if (e.kind.startsWith('domain_')) {
    const where = e.domain || 'domain not returned'
    parts.push(
      e.external === undefined
        ? `${where} (not checked)`
        : e.external
          ? `${where} (outside the tenant)`
          : where
    )
  }
  if (e.role) parts.push(e.role)
  return parts.join(' · ')
}

export type FileLike = { exposures: ExposureLike[] }

export function fileRisk(f: FileLike): Tone {
  if (f.exposures.length === 0) return 'neutral'
  const tones = f.exposures.map(exposureTone)
  return tones.includes('danger')
    ? 'danger'
    : tones.includes('warning')
      ? 'warning'
      : 'neutral'
}

// Google's file types as short words; anything else shows the raw type.
export function mimeLabel(mime: string): string {
  if (!mime) return 'Not provided'
  const g = 'application/vnd.google-apps.'
  if (mime.startsWith(g)) return mime.slice(g.length).replace(/-/g, ' ')
  if (mime === 'application/pdf') return 'pdf'
  return mime
}

// Only an https address is opened from the dashboard.
export function safeLink(link: string): string {
  return link.startsWith('https://') ? link : ''
}

export function parseMaxMembers(text: string): {
  value?: number
  error: string
} {
  const t = text.trim()
  if (!t) return { error: '' }
  if (!/^\d{1,6}$/.test(t))
    return { error: 'Use a whole number between 0 and 100000' }
  const n = Number(t)
  return n > 100000
    ? { error: 'Use a whole number between 0 and 100000' }
    : { value: n, error: '' }
}

const OU_ID = /^[A-Za-z0-9:_-]{1,100}$/
export function orgUnitIdError(text: string): string {
  const t = text.trim()
  return !t || OU_ID.test(t)
    ? ''
    : 'Use the organizational unit ID Google shows'
}
