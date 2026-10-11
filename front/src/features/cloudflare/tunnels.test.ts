import {
  CloudflareTunnelHostnameSchema,
  CloudflareTunnelRecordResultSchema,
  CloudflareTunnelSchema,
} from '@/gen/neobox/v1/cloudflare_pb'
import { create } from '@bufbuild/protobuf'
import { describe, expect, it } from 'vitest'
import {
  CloudflareTunnelRecordStatus as RecordStatus,
  CloudflareTunnelStatus as Status,
} from '@/api/cloudflare'
import {
  emptyHostnameForm,
  hostnameUrl,
  recordOutcome,
  runCommands,
  tunnelStatus,
  validateHostnameForm,
} from './tunnels'

const form = (over: Partial<ReturnType<typeof emptyHostnameForm>>) => ({
  ...emptyHostnameForm(),
  hostname: 'app.example.com',
  service: 'http://localhost:8080',
  ...over,
})

describe('validateHostnameForm', () => {
  it('accepts a hostname and an origin', () => {
    expect(validateHostnameForm(form({}), true)).toEqual({})
    expect(
      validateHostnameForm(form({ hostname: '*.example.com' }), true)
    ).toEqual({})
  })

  it('asks for the hostname only when adding', () => {
    expect(
      validateHostnameForm(form({ hostname: '' }), true).hostname
    ).toBeTruthy()
    expect(validateHostnameForm(form({ hostname: '' }), false)).toEqual({})
  })

  it.each([
    'localhost',
    'https://app.example.com',
    'app.example.com/x',
    'app.example.com:8080',
    'a..example.com',
    'app.*.com',
  ])('rejects the hostname %s', (hostname) => {
    expect(validateHostnameForm(form({ hostname }), true).hostname).toBeTruthy()
  })

  it.each([
    'http://localhost:8080',
    'https://10.0.0.5',
    'ssh://host:22',
    'tcp://db:5432',
    'unix:/run/app.sock',
    'http_status:404',
    'hello_world',
  ])('accepts the service %s', (service) => {
    expect(
      validateHostnameForm(form({ service }), true).service
    ).toBeUndefined()
  })

  it.each([
    '',
    'localhost:8080',
    'ftp://host',
    'http://host/path',
    'http_status:99',
    'unix:',
  ])('rejects the service %j', (service) => {
    expect(validateHostnameForm(form({ service }), true).service).toBeTruthy()
  })

  it('checks that a path is a regular expression', () => {
    expect(
      validateHostnameForm(form({ path: '^/api/' }), true).path
    ).toBeUndefined()
    expect(
      validateHostnameForm(form({ path: '(open' }), true).path
    ).toBeTruthy()
  })
})

describe('hostnameUrl', () => {
  it('opens real hostnames and leaves wildcards as patterns', () => {
    const h = (hostname: string) =>
      create(CloudflareTunnelHostnameSchema, { hostname })
    expect(hostnameUrl(h('app.example.com'))).toBe('https://app.example.com')
    expect(hostnameUrl(h('*.example.com'))).toBeUndefined()
    expect(hostnameUrl(h(''))).toBeUndefined()
  })
})

describe('runCommands', () => {
  it('puts the token in every command', () => {
    const cmds = runCommands('TOKEN123')
    expect(cmds.length).toBeGreaterThan(1)
    for (const c of cmds) expect(c.command).toContain('TOKEN123')
  })
})

describe('tunnelStatus', () => {
  it('says what each status means', () => {
    const label = (status: Status) =>
      tunnelStatus(create(CloudflareTunnelSchema, { status })).label
    expect(label(Status.HEALTHY)).toBe('Healthy')
    expect(label(Status.DEGRADED)).toBe('Degraded')
    expect(label(Status.DOWN)).toBe('Down')
    expect(label(Status.INACTIVE)).toBe('Inactive')
    expect(label(Status.UNSPECIFIED)).toBe('-')
  })
})

describe('recordOutcome', () => {
  const outcome = (status: RecordStatus, message = '') =>
    recordOutcome(
      create(CloudflareTunnelRecordResultSchema, { status, message })
    )

  it('is quiet when DNS was not asked for', () => {
    expect(outcome(RecordStatus.NOT_REQUESTED).tone).toBe('none')
    expect(recordOutcome(undefined).tone).toBe('none')
  })

  it('reports a DNS change that was made as a success', () => {
    expect(outcome(RecordStatus.CREATED).tone).toBe('success')
    expect(outcome(RecordStatus.EXISTS).tone).toBe('success')
    expect(outcome(RecordStatus.DELETED).tone).toBe('success')
  })

  it('warns, with the reason, when DNS was left alone or failed', () => {
    for (const s of [
      RecordStatus.KEPT,
      RecordStatus.NO_ZONE,
      RecordStatus.CONFLICT,
      RecordStatus.FAILED,
    ]) {
      expect(outcome(s, 'because').message).toBe('because')
      expect(outcome(s, 'because').tone).toBe('warning')
    }
  })
})
