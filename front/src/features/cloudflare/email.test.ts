import {
  CloudflareEmailAddressSchema,
  CloudflareEmailCatchAllSchema,
  CloudflareEmailDNSRecordSchema,
  CloudflareEmailRoutingSettingsSchema,
  CloudflareEmailRuleSchema,
} from '@/gen/neobox/v1/cloudflare_pb'
import { create } from '@bufbuild/protobuf'
import { describe, expect, it } from 'vitest'
import { CloudflareEmailAction as Action } from '@/api/cloudflare'
import {
  addressStatus,
  catchAllMode,
  catchAllProblem,
  catchAllRequest,
  catchAllSummary,
  dnsRecordSummary,
  emailProblem,
  routingState,
  ruleAddressProblem,
  ruleSummary,
  targetProblem,
  verifiedEmails,
} from './email'

describe('input checks', () => {
  it('checks an email address', () => {
    expect(emailProblem('me@gmail.com')).toBeUndefined()
    expect(emailProblem(' me@gmail.com ')).toBeUndefined()
    expect(emailProblem('')).toBeTruthy()
    expect(emailProblem('nope')).toBeTruthy()
    expect(emailProblem('a b@c.com')).toBeTruthy()
    expect(emailProblem(`${'x'.repeat(90)}@c.com`)).toBeTruthy()
  })

  it('checks a rule address against its zone', () => {
    expect(
      ruleAddressProblem('info@example.com', 'example.com')
    ).toBeUndefined()
    expect(
      ruleAddressProblem('Info@Example.COM', 'example.com')
    ).toBeUndefined()
    expect(ruleAddressProblem('info@other.com', 'example.com')).toMatch(
      /@example\.com/
    )
    expect(ruleAddressProblem('info', 'example.com')).toBeTruthy()
  })

  it('needs a target only for a forward', () => {
    expect(targetProblem(Action.FORWARD, 'me@gmail.com')).toBeUndefined()
    expect(targetProblem(Action.FORWARD, '')).toBeTruthy()
    expect(targetProblem(Action.DROP, '')).toBeUndefined()
  })
})

describe('wording', () => {
  it('summarizes a rule', () => {
    expect(
      ruleSummary(
        create(CloudflareEmailRuleSchema, {
          action: Action.FORWARD,
          target: 'me@gmail.com',
        })
      )
    ).toBe('Forward to me@gmail.com')
    expect(
      ruleSummary(create(CloudflareEmailRuleSchema, { action: Action.DROP }))
    ).toBe('Drop')
    expect(
      ruleSummary(
        create(CloudflareEmailRuleSchema, {
          action: Action.WORKER,
          target: 'mailer',
        })
      )
    ).toBe('Send to Worker mailer')
  })

  it('summarizes the catch-all', () => {
    expect(catchAllSummary(undefined)).toBe('Unknown')
    expect(
      catchAllSummary(create(CloudflareEmailCatchAllSchema, { enabled: false }))
    ).toBe('Off')
    expect(
      catchAllSummary(
        create(CloudflareEmailCatchAllSchema, {
          enabled: true,
          action: Action.FORWARD,
          target: 'me@gmail.com',
        })
      )
    ).toBe('Forward to me@gmail.com')
  })

  it('tells a verified address from a pending one', () => {
    const ok = create(CloudflareEmailAddressSchema, { verified: true })
    const pending = create(CloudflareEmailAddressSchema, { verified: false })
    expect(addressStatus(ok).label).toBe('Verified')
    expect(addressStatus(pending).label).toMatch(/verification/i)
    expect(
      verifiedEmails([
        create(CloudflareEmailAddressSchema, {
          email: 'b@x.com',
          verified: true,
        }),
        create(CloudflareEmailAddressSchema, { email: 'p@x.com' }),
        create(CloudflareEmailAddressSchema, {
          email: 'a@x.com',
          verified: true,
        }),
      ])
    ).toEqual(['a@x.com', 'b@x.com'])
  })

  it('states how Email Routing stands', () => {
    const s = (v: object) => create(CloudflareEmailRoutingSettingsSchema, v)
    expect(routingState(undefined).label).toBe('Loading')
    expect(routingState(s({ enabled: false })).label).toBe('Not enabled')
    expect(routingState(s({ enabled: true, status: 'ready' })).label).toBe(
      'Active'
    )
    const bad = routingState(s({ enabled: true, status: 'misconfigured' }))
    expect(bad.label).toBe('Needs attention')
    expect(bad.hint).toMatch(/misconfigured/)
  })

  it('describes a required DNS record', () => {
    expect(
      dnsRecordSummary(
        create(CloudflareEmailDNSRecordSchema, {
          type: 'MX',
          name: 'example.com',
          content: 'route1.mx.cloudflare.net',
          priority: 20,
        })
      )
    ).toBe('MX example.com → route1.mx.cloudflare.net (priority 20)')
    expect(
      dnsRecordSummary(
        create(CloudflareEmailDNSRecordSchema, {
          type: 'TXT',
          name: 'example.com',
          content: 'v=spf1 ~all',
        })
      )
    ).toBe('TXT example.com → v=spf1 ~all')
  })
})

describe('catch-all choice', () => {
  it('reads the mode from the catch-all', () => {
    const c = (v: object) => create(CloudflareEmailCatchAllSchema, v)
    expect(catchAllMode(c({ enabled: false, action: Action.FORWARD }))).toBe(
      'off'
    )
    expect(catchAllMode(c({ enabled: true, action: Action.DROP }))).toBe('drop')
    expect(catchAllMode(c({ enabled: true, action: Action.FORWARD }))).toBe(
      'forward'
    )
  })

  it('builds the request from the choice', () => {
    expect(catchAllRequest('off', 'x')).toEqual({
      enabled: false,
      action: Action.DROP,
      target: '',
    })
    expect(catchAllRequest('drop', 'x')).toEqual({
      enabled: true,
      action: Action.DROP,
      target: '',
    })
    expect(catchAllRequest('forward', ' me@gmail.com ')).toEqual({
      enabled: true,
      action: Action.FORWARD,
      target: 'me@gmail.com',
    })
  })

  it('needs a target only to forward', () => {
    expect(catchAllProblem('forward', '')).toBeTruthy()
    expect(catchAllProblem('forward', 'me@gmail.com')).toBeUndefined()
    expect(catchAllProblem('drop', '')).toBeUndefined()
    expect(catchAllProblem('off', '')).toBeUndefined()
  })
})
