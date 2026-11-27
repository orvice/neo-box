import { CloudflareZoneRuleSchema } from '@/gen/neobox/v1/cloudflare_pb'
import { create } from '@bufbuild/protobuf'
import { Code, ConnectError } from '@connectrpc/connect'
import { describe, expect, it } from 'vitest'
import {
  CloudflareHeaderOperation,
  CloudflareRedirectTargetKind,
  CloudflareRewritePartMode,
  CloudflareZoneRuleCategory,
  CloudflareZoneRulesErrorKind,
} from '@/api/cloudflare'
import {
  buildCommonExpression,
  buildRuleConfirmationItems,
  canMoveRule,
  canReturnToCommon,
  categoryIssueTitle,
  defaultRuleDraft,
  draftFromRule,
  escapeExpressionString,
  moveRuleIds,
  parseExpression,
  ruleInputFromDraft,
  summarizeRuleAction,
  unescapeExpressionString,
  validateRuleDraft,
  zoneRuleChangeError,
} from './zone-rules'

describe('expression conversion and escaping', () => {
  it('escapes and unescapes quotes and backslashes cleanly', () => {
    const raw = '/docs/"quoted"\\dir'
    const esc = escapeExpressionString(raw)
    expect(esc).toBe('/docs/\\"quoted\\"\\\\dir')
    expect(unescapeExpressionString(esc)).toBe(raw)
    expect(unescapeExpressionString('bad\\x')).toBeUndefined()
  })

  it('round-trips common exact host and optional exact/prefix path expressions', () => {
    const hostOnly = buildCommonExpression({
      host: 'app.example.com',
      pathKind: 'none',
      path: '',
    })
    expect(hostOnly).toBe('(http.host eq "app.example.com")')
    expect(parseExpression(hostOnly)).toEqual({
      mode: 'common',
      common: { host: 'app.example.com', pathKind: 'none', path: '' },
      expression: hostOnly,
    })

    const exactPath = buildCommonExpression({
      host: 'app.example.com',
      pathKind: 'exact',
      path: '/old"path',
    })
    expect(parseExpression(exactPath)).toEqual({
      mode: 'common',
      common: { host: 'app.example.com', pathKind: 'exact', path: '/old"path' },
      expression: exactPath,
    })

    const prefixPath = buildCommonExpression({
      host: 'app.example.com',
      pathKind: 'prefix',
      path: '/api/v1/',
    })
    expect(parseExpression(prefixPath)).toEqual({
      mode: 'common',
      common: {
        host: 'app.example.com',
        pathKind: 'prefix',
        path: '/api/v1/',
      },
      expression: prefixPath,
    })
    expect(canReturnToCommon(prefixPath)).toBe(true)
  })

  it('recognizes explicit match-all (true) and keeps unrepresentable expressions in advanced mode', () => {
    expect(parseExpression('true').mode).toBe('all')
    expect(parseExpression('(true)').mode).toBe('all')

    const complex =
      '(http.host eq "a.com" and ip.src.country eq "US") or http.request.method eq "POST"'
    const parsed = parseExpression(complex)
    expect(parsed.mode).toBe('advanced')
    expect(parsed.expression).toBe(complex)
    expect(canReturnToCommon(complex)).toBe(false)
  })

  it('requires explicit match-all selection instead of allowing an empty common form', () => {
    const draft = defaultRuleDraft(
      CloudflareZoneRuleCategory.SINGLE_REDIRECT,
      ''
    )
    draft.redirectTargetValue = 'https://example.com'
    const errs = validateRuleDraft(
      CloudflareZoneRuleCategory.SINGLE_REDIRECT,
      draft
    )
    expect(errs.matchHost).toMatch(/Match all requests/i)

    draft.matchMode = 'all'
    expect(
      validateRuleDraft(CloudflareZoneRuleCategory.SINGLE_REDIRECT, draft)
    ).toEqual({})
    expect(
      ruleInputFromDraft(CloudflareZoneRuleCategory.SINGLE_REDIRECT, draft)
        .expression
    ).toBe('true')
  })
})

describe('action validation and typed payload building', () => {
  it('validates Single Redirects status codes and targets', () => {
    const draft = defaultRuleDraft(
      CloudflareZoneRuleCategory.SINGLE_REDIRECT,
      'example.com'
    )
    draft.redirectTargetValue = 'https://new.example.com/path'
    draft.redirectStatusCode = 308
    draft.redirectPreserveQuery = true
    expect(
      validateRuleDraft(CloudflareZoneRuleCategory.SINGLE_REDIRECT, draft)
    ).toEqual({})

    draft.redirectStatusCode = 303
    expect(
      validateRuleDraft(CloudflareZoneRuleCategory.SINGLE_REDIRECT, draft)
        .redirectStatus
    ).toBeTruthy()
  })

  it('validates URL Rewrite requires at least path or query', () => {
    const draft = defaultRuleDraft(
      CloudflareZoneRuleCategory.URL_REWRITE,
      'example.com'
    )
    draft.rewritePathMode = CloudflareRewritePartMode.KEEP
    draft.rewriteQueryMode = CloudflareRewritePartMode.KEEP
    expect(
      validateRuleDraft(CloudflareZoneRuleCategory.URL_REWRITE, draft)
        .urlRewrite
    ).toBeTruthy()

    draft.rewriteQueryMode = CloudflareRewritePartMode.STATIC
    draft.rewriteQueryValue = '' // clearing query string is valid
    expect(
      validateRuleDraft(CloudflareZoneRuleCategory.URL_REWRITE, draft)
    ).toEqual({})
  })

  it('validates Request vs Response Header Transform operations and duplicate names', () => {
    const req = defaultRuleDraft(
      CloudflareZoneRuleCategory.REQUEST_HEADER,
      'example.com'
    )
    req.headers = [
      {
        id: '1',
        name: 'X-Add',
        operation: CloudflareHeaderOperation.ADD_STATIC,
        value: 'v',
      },
    ]
    expect(
      validateRuleDraft(CloudflareZoneRuleCategory.REQUEST_HEADER, req)
        .header_0_op
    ).toMatch(/Set or Remove/)

    // Response Header allows add.
    expect(
      validateRuleDraft(CloudflareZoneRuleCategory.RESPONSE_HEADER, req)
    ).toEqual({})

    // Duplicate header names (case-insensitive) are caught.
    req.headers = [
      {
        id: '1',
        name: 'X-Test',
        operation: CloudflareHeaderOperation.SET_STATIC,
        value: '1',
      },
      {
        id: '2',
        name: 'x-test',
        operation: CloudflareHeaderOperation.REMOVE,
        value: '',
      },
    ]
    expect(
      validateRuleDraft(CloudflareZoneRuleCategory.REQUEST_HEADER, req)
        .header_1_name
    ).toMatch(/Duplicate/)
  })

  it('validates Origin Rules overrides and port bounds', () => {
    const draft = defaultRuleDraft(
      CloudflareZoneRuleCategory.ORIGIN,
      'example.com'
    )
    expect(
      validateRuleDraft(CloudflareZoneRuleCategory.ORIGIN, draft).origin
    ).toBeTruthy()

    draft.originPort = '70000'
    expect(
      validateRuleDraft(CloudflareZoneRuleCategory.ORIGIN, draft).originPort
    ).toBeTruthy()

    draft.originPort = '8443'
    draft.originDnsRecord = 'https://bad.internal'
    expect(
      validateRuleDraft(CloudflareZoneRuleCategory.ORIGIN, draft)
        .originDnsRecord
    ).toBeTruthy()

    draft.originDnsRecord = 'backend.internal'
    expect(validateRuleDraft(CloudflareZoneRuleCategory.ORIGIN, draft)).toEqual(
      {}
    )
  })

  it('preserves advanced expression when editing a rule with supported action', () => {
    const rule = create(CloudflareZoneRuleSchema, {
      id: 'r1',
      description: 'Geo redirect',
      expression: 'ip.src.country eq "DE"',
      enabled: false,
      config: {
        case: 'singleRedirect',
        value: {
          targetKind: CloudflareRedirectTargetKind.STATIC,
          targetValue: 'https://de.example.com',
          statusCode: 302,
          preserveQueryString: true,
        },
      },
    })
    const draft = draftFromRule(
      CloudflareZoneRuleCategory.SINGLE_REDIRECT,
      rule,
      'example.com'
    )
    expect(draft.matchMode).toBe('advanced')
    expect(draft.expression).toBe('ip.src.country eq "DE"')
    expect(draft.enabled).toBe(false)
    expect(summarizeRuleAction(rule)).toContain('HTTP 302')
  })
})

describe('bounded ordering around read-only rules', () => {
  const mk = (id: string, readOnly = false) =>
    create(CloudflareZoneRuleSchema, {
      id,
      readOnly,
      readOnlyReason: readOnly ? 'Unsupported field: logging' : '',
    })

  const rules = [mk('r1'), mk('r2'), mk('ro', true), mk('r3'), mk('r4')]

  it('blocks moving read-only rules and blocks moving supported rules across them', () => {
    expect(canMoveRule(rules, 2, 'up').allowed).toBe(false)
    expect(canMoveRule(rules, 2, 'down').allowed).toBe(false)
    expect(canMoveRule(rules, 1, 'down').allowed).toBe(false)
    expect(canMoveRule(rules, 1, 'down').reason).toMatch(/read-only/i)
    expect(canMoveRule(rules, 3, 'up').allowed).toBe(false)
    expect(moveRuleIds(rules, 1, 'down')).toBeUndefined()
  })

  it('allows reordering supported rules within their intervening groups', () => {
    expect(canMoveRule(rules, 0, 'down').allowed).toBe(true)
    expect(moveRuleIds(rules, 0, 'down')).toEqual([
      'r2',
      'r1',
      'ro',
      'r3',
      'r4',
    ])
    expect(moveRuleIds(rules, 4, 'up')).toEqual(['r1', 'r2', 'ro', 'r4', 'r3'])
  })
})

describe('error classification and confirmation wording', () => {
  it('classifies conflict, uncertain, quota, permission, entitlement and token errors', () => {
    expect(
      zoneRuleChangeError(
        new ConnectError('stale snapshot', Code.Aborted),
        CloudflareZoneRuleCategory.ORIGIN
      ).kind
    ).toBe('conflict')

    const unk = zoneRuleChangeError(
      new ConnectError('timeout', Code.Unknown),
      CloudflareZoneRuleCategory.SINGLE_REDIRECT
    )
    expect(unk.kind).toBe('uncertain')
    expect(unk.uncertain).toBe(true)
    expect(unk.message).toMatch(/Refresh and verify/i)

    expect(
      zoneRuleChangeError(
        new ConnectError('rule limit reached', Code.ResourceExhausted),
        CloudflareZoneRuleCategory.URL_REWRITE
      ).kind
    ).toBe('quota')

    expect(
      zoneRuleChangeError(
        new ConnectError('grant it Origin Write', Code.PermissionDenied),
        CloudflareZoneRuleCategory.ORIGIN
      ).kind
    ).toBe('permission')

    expect(
      zoneRuleChangeError(
        new ConnectError(
          'Cloudflare plan or entitlement restriction',
          Code.FailedPrecondition
        ),
        CloudflareZoneRuleCategory.ORIGIN
      ).kind
    ).toBe('feature')

    expect(
      zoneRuleChangeError(
        new ConnectError(
          'Cloudflare does not accept this connection API token',
          Code.FailedPrecondition
        ),
        CloudflareZoneRuleCategory.ORIGIN
      ).kind
    ).toBe('token')

    expect(
      categoryIssueTitle(CloudflareZoneRulesErrorKind.PERMISSION_DENIED)
    ).toMatch(/permission/i)
  })

  it('builds reviewable confirmation items showing category, match, behavior and state', () => {
    const draft = defaultRuleDraft(
      CloudflareZoneRuleCategory.SINGLE_REDIRECT,
      'alpha.com'
    )
    draft.description = 'Redirect root'
    draft.redirectTargetValue = 'https://www.alpha.com'
    draft.enabled = false
    const items = buildRuleConfirmationItems(
      CloudflareZoneRuleCategory.SINGLE_REDIRECT,
      draft,
      'create'
    )
    expect(items.find((i) => i.label === 'Category')?.value).toBe(
      'Single Redirects'
    )
    expect(items.find((i) => i.label === 'State')?.value).toBe('Disabled')
    expect(items.find((i) => i.label === 'Match')?.value).toBe(
      '(http.host eq "alpha.com")'
    )
  })
})
