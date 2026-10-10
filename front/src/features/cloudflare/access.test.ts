import {
  CloudflareDNSRecordSchema,
  CloudflareZoneSchema,
} from '@/gen/neobox/v1/cloudflare_pb'
import { create } from '@bufbuild/protobuf'
import { Code, ConnectError } from '@connectrpc/connect'
import { describe, expect, it } from 'vitest'
import { CloudflareRecordAccess, isPermissionRefusal } from '@/api/cloudflare'
import {
  changeError,
  dnsEditState,
  moduleProblem,
  recordActions,
} from './access'

const zone = (dnsAccess: CloudflareRecordAccess) =>
  create(CloudflareZoneSchema, { id: 'z', name: 'alpha.com', dnsAccess })
const rec = (type: string, proxiable = false) =>
  create(CloudflareDNSRecordSchema, {
    id: 'r',
    type,
    proxiable,
    editable: !['HTTPS', 'LOC'].includes(type),
  })

describe('dnsEditState', () => {
  it('disables edits Cloudflare rules out', () => {
    const s = dnsEditState(zone(CloudflareRecordAccess.READ))
    expect(s.canEdit).toBe(false)
    expect(s.state).toBe('read-only')
    expect(s.reason).toMatch(/DNS Write/)
  })

  it('allows edits but says so when the permission is unknown', () => {
    const s = dnsEditState(zone(CloudflareRecordAccess.UNKNOWN))
    expect(s.canEdit).toBe(true)
    expect(s.state).toBe('unknown')
  })

  it('allows nothing before the zone has loaded', () => {
    expect(dnsEditState(undefined).canEdit).toBe(false)
  })
})

describe('recordActions', () => {
  const open = dnsEditState(zone(CloudflareRecordAccess.UNKNOWN))
  it('edits only the supported types and proxies only proxiable records', () => {
    expect(recordActions(rec('A', true), open)).toEqual({
      edit: true,
      remove: true,
      proxy: true,
    })
    expect(recordActions(rec('TXT'), open)).toMatchObject({
      edit: true,
      remove: true,
      proxy: false,
    })
    expect(recordActions(rec('HTTPS'), open)).toMatchObject({
      edit: false,
      remove: false,
      proxy: false,
    })
    expect(recordActions(rec('HTTPS'), open).reason).toMatch(/HTTPS/)
  })
  it('turns everything off on a read-only zone', () => {
    const ro = dnsEditState(zone(CloudflareRecordAccess.READ))
    expect(recordActions(rec('A', true), ro)).toMatchObject({
      edit: false,
      remove: false,
      proxy: false,
    })
  })
})

describe('moduleProblem', () => {
  it('tells a missing permission from a rejected token and a failure', () => {
    expect(
      moduleProblem(new ConnectError('no Workers', Code.PermissionDenied)).kind
    ).toBe('permission')
    expect(
      moduleProblem(new ConnectError('token revoked', Code.FailedPrecondition))
        .kind
    ).toBe('token')
    expect(moduleProblem(new ConnectError('down', Code.Unavailable)).kind).toBe(
      'error'
    )
    expect(moduleProblem(new Error('boom')).kind).toBe('error')
  })
})

describe('isPermissionRefusal', () => {
  it('is a change Cloudflare refused for lack of permission', () => {
    expect(
      isPermissionRefusal(
        new ConnectError('grant it "DNS Write"', Code.PermissionDenied)
      )
    ).toBe(true)
    expect(
      isPermissionRefusal(new ConnectError('bad TTL', Code.InvalidArgument))
    ).toBe(false)
    expect(isPermissionRefusal(new Error('network'))).toBe(false)
  })
})

describe('changeError', () => {
  it('warns that an unconfirmed change may have been made', () => {
    const e = changeError(
      new ConnectError('cloudflare did not confirm', Code.Unknown)
    )
    expect(e.uncertain).toBe(true)
    expect(e.message).toMatch(/may or may not/)
    expect(
      changeError(
        new ConnectError('Cloudflare refused it: bad TTL', Code.InvalidArgument)
      ).uncertain
    ).toBe(false)
  })
})
