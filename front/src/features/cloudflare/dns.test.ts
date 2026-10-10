import { CloudflareDNSRecordSchema } from '@/gen/neobox/v1/cloudflare_pb'
import { create, type MessageInitShape } from '@bufbuild/protobuf'
import { describe, expect, it } from 'vitest'
import {
  contentSummary,
  emptyForm,
  formFromRecord,
  formatTTL,
  inputFromForm,
  requestSummary,
  proxyApplies,
  validateForm,
  type RecordForm,
} from './dns'

const record = (r: MessageInitShape<typeof CloudflareDNSRecordSchema>) =>
  create(CloudflareDNSRecordSchema, r)

const form = (f: Partial<RecordForm>): RecordForm => ({
  ...emptyForm(f.type ?? 'A'),
  ...f,
})

describe('validateForm', () => {
  it('accepts well-formed records of every editable type', () => {
    const ok: Partial<RecordForm>[] = [
      { type: 'A', name: 'www', content: '192.0.2.1' },
      { type: 'AAAA', name: 'www', content: '2001:db8::1' },
      { type: 'CNAME', name: 'docs', content: 'docs.example.net' },
      { type: 'TXT', name: '@', content: 'v=spf1 -all' },
      { type: 'MX', name: '@', content: 'mx.example.com', priority: '10' },
      { type: 'NS', name: 'sub', content: 'ns1.example.net' },
      {
        type: 'SRV',
        name: '_sip._tcp',
        srvPriority: '10',
        srvWeight: '5',
        srvPort: '5060',
        srvTarget: 'sip.example.com',
      },
      {
        type: 'CAA',
        name: '@',
        caaFlags: '0',
        caaTag: 'issue',
        caaValue: 'letsencrypt.org',
      },
    ]
    for (const f of ok) expect(validateForm(form(f)), f.type).toEqual({})
  })

  it('names the field that is wrong', () => {
    expect(
      validateForm(form({ type: 'A', name: 'www', content: '300.1.1.1' }))
    ).toHaveProperty('content')
    expect(
      validateForm(form({ type: 'AAAA', name: 'www', content: '192.0.2.1' }))
    ).toHaveProperty('content')
    expect(
      validateForm(form({ type: 'A', name: '', content: '192.0.2.1' }))
    ).toHaveProperty('name')
    expect(
      validateForm(
        form({
          type: 'MX',
          name: '@',
          content: 'mx.example.com',
          priority: '70000',
        })
      )
    ).toHaveProperty('priority')
    expect(
      validateForm(
        form({
          type: 'SRV',
          name: 'sip',
          srvPort: '5060',
          srvTarget: 'x.example.com',
        })
      )
    ).toHaveProperty('name')
    expect(
      validateForm(
        form({ type: 'CAA', name: '@', caaTag: 'bogus', caaValue: 'x' })
      )
    ).toHaveProperty('caaTag')
    expect(
      validateForm(form({ type: 'TXT', name: '@', content: 'x', ttl: '10' }))
    ).toHaveProperty('ttl')
  })
})

describe('inputFromForm', () => {
  it('sends the structured fields of SRV, CAA and MX', () => {
    expect(
      inputFromForm(
        form({
          type: 'SRV',
          name: '_sip._tcp',
          srvPriority: '10',
          srvWeight: '5',
          srvPort: '5060',
          srvTarget: 'sip.example.com',
        }),
        false
      )
    ).toMatchObject({
      type: 'SRV',
      name: '_sip._tcp',
      ttl: 1,
      srv: { priority: 10, weight: 5, port: 5060, target: 'sip.example.com' },
    })
    expect(
      inputFromForm(
        form({
          type: 'CAA',
          name: '@',
          caaFlags: '128',
          caaTag: 'iodef',
          caaValue: 'mailto:x@example.com',
        }),
        false
      )
    ).toMatchObject({
      caa: { flags: 128, tag: 'iodef', value: 'mailto:x@example.com' },
    })
    expect(
      inputFromForm(
        form({
          type: 'MX',
          name: '@',
          content: 'mx.example.com',
          priority: '20',
          ttl: '3600',
        }),
        false
      )
    ).toMatchObject({
      content: 'mx.example.com',
      priority: 20,
      ttl: 3600,
    })
  })

  it('sends proxied only where it applies', () => {
    expect(
      inputFromForm(
        form({ type: 'A', name: 'www', content: '192.0.2.1', proxied: true }),
        true
      ).proxied
    ).toBe(true)
    expect(
      inputFromForm(
        form({ type: 'A', name: 'www', content: '192.0.2.1', proxied: true }),
        false
      ).proxied
    ).toBe(false)
  })
})

describe('formFromRecord', () => {
  it('round-trips structured records', () => {
    const srv = record({
      type: 'SRV',
      name: '_sip._tcp.example.com',
      ttl: 300,
      srv: { priority: 10, weight: 5, port: 5060, target: 'sip.example.com' },
    })
    const f = formFromRecord(srv)
    expect(f).toMatchObject({
      type: 'SRV',
      name: '_sip._tcp.example.com',
      ttl: '300',
      srvPort: '5060',
      srvTarget: 'sip.example.com',
    })
    expect(inputFromForm(f, false).srv).toMatchObject({
      priority: 10,
      weight: 5,
      port: 5060,
    })
  })
})

describe('proxyApplies', () => {
  it('follows the record type when creating and Cloudflare when editing', () => {
    expect(proxyApplies('CNAME')).toBe(true)
    expect(proxyApplies('TXT')).toBe(false)
    expect(proxyApplies('A', record({ type: 'A', proxiable: false }))).toBe(
      false
    )
    expect(proxyApplies('A', record({ type: 'A', proxiable: true }))).toBe(true)
  })
})

describe('display', () => {
  it('summarises content and TTL', () => {
    expect(
      contentSummary(
        record({ type: 'MX', content: 'mx.example.com', priority: 10 })
      )
    ).toBe('10 mx.example.com')
    expect(
      contentSummary(
        record({
          type: 'CAA',
          content: '0 issue "x"',
          caa: { flags: 0, tag: 'issue', value: 'letsencrypt.org' },
        })
      )
    ).toBe('0 issue "letsencrypt.org"')
    expect(formatTTL(1)).toBe('Auto')
    expect(formatTTL(300)).toBe('5 min')
    expect(formatTTL(3600)).toBe('1 hr')
    expect(formatTTL(45)).toBe('45 s')
  })
})

describe('requestSummary', () => {
  it('lists what was sent, record fields first', () => {
    expect(
      requestSummary({
        proxied: true,
        ttl: 1,
        content: '192.0.2.1',
        name: 'www',
        type: 'A',
      })
    ).toBe('A www → 192.0.2.1 · TTL Auto · proxied')
    expect(requestSummary({ proxied: false })).toBe('DNS only')
    expect(
      requestSummary({
        name: '_sip._tcp',
        ttl: 300,
        data: {
          priority: 10,
          weight: 5,
          port: 5060,
          target: 'sip.example.com',
        },
      })
    ).toBe('_sip._tcp → 10 5 5060 sip.example.com · TTL 5 min')
    expect(
      requestSummary({
        name: '@',
        ttl: 1,
        data: { flags: 0, tag: 'issue', value: 'x.org' },
      })
    ).toBe('@ → 0 issue "x.org" · TTL Auto')
    expect(
      requestSummary({
        name: '@',
        ttl: 3600,
        content: 'mx.example.com',
        priority: 10,
      })
    ).toBe('@ → 10 mx.example.com · TTL 1 hr')
  })
})
