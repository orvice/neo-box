// Pure helpers for the Drive governance tab: Reports API only, no file content.

export const DRIVE_AUDIT_SCOPE =
  'https://www.googleapis.com/auth/admin.reports.audit.readonly'
export const DRIVE_USAGE_SCOPE =
  'https://www.googleapis.com/auth/admin.reports.usage.readonly'

export const DRIVE_PRESETS = [
  { key: 'all', label: 'All Drive activity' },
  { key: 'public_web', label: 'Public on the web' },
  { key: 'link_sharing', label: 'Anyone with the link' },
  { key: 'external_share', label: 'Shared outside the domain' },
  { key: 'ownership', label: 'Ownership transfers' },
  { key: 'downloads', label: 'Downloads' },
  { key: 'deletions', label: 'Deletions' },
  { key: 'trash', label: 'Moved to trash' },
  { key: 'shared_drive_members', label: 'Shared drive members' },
  { key: 'shared_drive_settings', label: 'Shared drive settings' },
] as const

export type ExposureTone = 'danger' | 'warn' | 'ok' | 'neutral'

// Visibility values Google documents; anything else is shown as unknown,
// never as private.
export function exposureOf(visibility: string): {
  label: string
  tone: ExposureTone
} {
  switch (visibility) {
    case 'public_on_the_web':
      return { label: 'Public on the web', tone: 'danger' }
    case 'people_with_link':
      return { label: 'Anyone with the link', tone: 'warn' }
    case 'shared_externally':
      return { label: 'Shared externally', tone: 'warn' }
    case 'shared_internally':
    case 'people_within_domain_with_link':
    case 'public_in_the_domain':
      return { label: 'Inside the domain', tone: 'neutral' }
    case 'private':
      return { label: 'Private', tone: 'ok' }
    default:
      return { label: 'Unknown', tone: 'neutral' }
  }
}

export function humanizeDriveValue(value: string) {
  return value ? value.replace(/_/g, ' ') : 'Not provided'
}

export type DriveEventFields = {
  name: string
  visibility: string
  oldVisibility: string
  targetUser: string
  targetDomain: string
  target: string
  oldRole: string
  newRole: string
  newOwner: string
  membershipChangeType: string
  addedRole: string
  removedRole: string
}

// One line describing what changed, using only fields Google returned.
export function driveEventSummary(e: DriveEventFields): string {
  const who = e.targetUser || e.targetDomain || e.target
  switch (e.name) {
    case 'change_document_visibility':
      return `Visibility ${humanizeDriveValue(e.oldVisibility)} → ${humanizeDriveValue(e.visibility)}`
    case 'change_user_access':
      return `${who || 'Not provided'}: ${e.oldRole ? humanizeDriveValue(e.oldRole) : 'no access'} → ${e.newRole ? humanizeDriveValue(e.newRole) : 'Not provided'}`
    case 'change_owner':
      return `Ownership → ${e.newOwner || 'Not provided'}`
    case 'shared_drive_membership_change': {
      const role = e.addedRole || e.removedRole
      return [
        humanizeDriveValue(e.membershipChangeType),
        who,
        role && humanizeDriveValue(role),
      ]
        .filter(Boolean)
        .join(' · ')
    }
    default:
      return humanizeDriveValue(e.name)
  }
}

const UNITS = ['MB', 'GB', 'TB', 'PB']

// Usage figures arrive in MB (1024-based). Missing stays "Unknown", never 0.
export function formatUsageMB(mb: bigint | number | undefined): string {
  if (mb === undefined) return 'Unknown'
  let value = Number(mb)
  let unit = 0
  while (value >= 1024 && unit < UNITS.length - 1) {
    value /= 1024
    unit++
  }
  const text = unit === 0 ? String(value) : value.toFixed(value < 10 ? 2 : 1)
  return `${text} ${UNITS[unit]}`
}

export function formatQuota(mb: bigint | undefined): string {
  if (mb === undefined) return 'Unknown'
  return mb === 0n ? 'Unlimited' : formatUsageMB(mb)
}

export function formatShare(percent: number | undefined): string {
  if (percent === undefined) return '—'
  return `${percent.toFixed(percent < 10 ? 1 : 0)}%`
}

export function usageShareTone(percent: number | undefined): ExposureTone {
  if (percent === undefined) return 'neutral'
  if (percent >= 95) return 'danger'
  if (percent >= 80) return 'warn'
  return 'ok'
}
