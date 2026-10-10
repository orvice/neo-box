import { Code, ConnectError } from '@connectrpc/connect'
import {
  CloudflareRecordAccess,
  type CloudflareDNSRecord,
  type CloudflareZone,
} from '@/api/cloudflare'

/** Whether DNS records of a Zone can be changed from here, and why not. */
export type EditState = {
  canEdit: boolean
  state: 'loading' | 'unknown' | 'read-only'
  reason?: string
}

export function dnsEditState(zone: CloudflareZone | undefined): EditState {
  if (!zone) return { canEdit: false, state: 'loading' }
  if (zone.dnsAccess === CloudflareRecordAccess.READ) {
    return {
      canEdit: false,
      state: 'read-only',
      reason:
        'Cloudflare says, or has shown by refusing a change, that this token can read but not change DNS records of this Zone. Grant it "DNS Write" (Zone → DNS → Edit) and refresh to change them here.',
    }
  }
  // Cloudflare doesn't tell an API token's own permissions, so the state
  // stays unknown: edits are offered, and Cloudflare has the last word.
  return {
    canEdit: true,
    state: 'unknown',
    reason:
      'Cloudflare does not tell Neo Box whether this token may change DNS records. Changes are offered; without "DNS Write" (Zone → DNS → Edit) Cloudflare refuses them and this Zone becomes read-only here.',
  }
}

export type RecordActions = {
  edit: boolean
  remove: boolean
  proxy: boolean
  reason?: string
}

/** What can be done to one record. */
export function recordActions(
  r: CloudflareDNSRecord,
  zone: EditState
): RecordActions {
  if (!zone.canEdit)
    return { edit: false, remove: false, proxy: false, reason: zone.reason }
  if (!r.editable)
    return {
      edit: false,
      remove: false,
      proxy: false,
      reason: `${r.type} records are shown but not changed here.`,
    }
  return { edit: true, remove: true, proxy: r.proxiable }
}

/** Why a list (Zones, Workers, Pages, records) could not be read. */
export type ModuleProblem = {
  kind: 'permission' | 'token' | 'notfound' | 'error'
  title: string
  message: string
}

export function moduleProblem(err: unknown): ModuleProblem {
  if (err instanceof ConnectError) {
    switch (err.code) {
      case Code.PermissionDenied:
        return {
          kind: 'permission',
          title: 'The API token lacks this permission',
          message: err.rawMessage,
        }
      case Code.FailedPrecondition:
        return {
          kind: 'token',
          title: 'Cloudflare does not accept the API token',
          message: err.rawMessage,
        }
      case Code.NotFound:
        return { kind: 'notfound', title: 'Not found', message: err.rawMessage }
    }
    return {
      kind: 'error',
      title: 'Could not read from Cloudflare',
      message: err.rawMessage,
    }
  }
  return {
    kind: 'error',
    title: 'Could not read from Cloudflare',
    message: err instanceof Error ? err.message : String(err),
  }
}

/** A failed change, and whether it may have been made anyway. */
export function changeError(err: unknown): {
  uncertain: boolean
  message: string
} {
  const e = ConnectError.from(err)
  if (e.code === Code.Unknown) {
    return {
      uncertain: true,
      message:
        'Cloudflare did not confirm the change, so it may or may not have been made. Check the records before trying again.',
    }
  }
  return { uncertain: false, message: e.rawMessage }
}
