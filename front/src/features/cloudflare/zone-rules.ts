import { Code, ConnectError } from '@connectrpc/connect'
import {
  CloudflareHeaderOperation,
  CloudflareRedirectTargetKind,
  CloudflareRewritePartMode,
  CloudflareZoneRuleCategory,
  CloudflareZoneRulesErrorKind,
  type CloudflareZoneRule,
  type CloudflareZoneRuleInit,
} from '@/api/cloudflare'

export type ZoneRuleCategoryMeta = {
  category: CloudflareZoneRuleCategory
  label: string
  description: string
  readPermission: string
  writePermission: string
}

export const ZONE_RULE_CATEGORIES: ZoneRuleCategoryMeta[] = [
  {
    category: CloudflareZoneRuleCategory.SINGLE_REDIRECT,
    label: 'Single Redirects',
    description:
      'Redirect matching requests to a static or dynamic target URL with a 301, 302, 307 or 308 status.',
    readPermission: 'Zone · Dynamic URL Redirects · Read',
    writePermission: 'Zone · Dynamic URL Redirects · Edit',
  },
  {
    category: CloudflareZoneRuleCategory.URL_REWRITE,
    label: 'URL Rewrite Rules',
    description:
      'Rewrite the incoming request path and/or query string before sending it to the origin.',
    readPermission: 'Zone · Transform Rules · Read',
    writePermission: 'Zone · Transform Rules · Edit',
  },
  {
    category: CloudflareZoneRuleCategory.REQUEST_HEADER,
    label: 'Request Header Transform Rules',
    description:
      'Set or remove HTTP headers on requests forwarded to your origin.',
    readPermission: 'Zone · Transform Rules · Read',
    writePermission: 'Zone · Transform Rules · Edit',
  },
  {
    category: CloudflareZoneRuleCategory.RESPONSE_HEADER,
    label: 'Response Header Transform Rules',
    description:
      'Set, add or remove HTTP headers on responses returned to visitors.',
    readPermission: 'Zone · Transform Rules · Read',
    writePermission: 'Zone · Transform Rules · Edit',
  },
  {
    category: CloudflareZoneRuleCategory.ORIGIN,
    label: 'Origin Rules',
    description:
      'Override destination port, Host header, TLS SNI or DNS resolution target without modifying DNS records.',
    readPermission: 'Zone · Origin Rules · Read',
    writePermission: 'Zone · Origin Rules · Edit',
  },
]

export function categoryMeta(
  category: CloudflareZoneRuleCategory
): ZoneRuleCategoryMeta {
  return (
    ZONE_RULE_CATEGORIES.find((c) => c.category === category) ??
    ZONE_RULE_CATEGORIES[0]
  )
}

// --- Matching expression conversion & escaping ---

export type MatchMode = 'common' | 'all' | 'advanced'
export type PathMatchKind = 'none' | 'exact' | 'prefix'

export type CommonMatch = {
  host: string
  pathKind: PathMatchKind
  path: string
}

export function escapeExpressionString(value: string): string {
  return value.replace(/\\/g, '\\\\').replace(/"/g, '\\"')
}

export function unescapeExpressionString(raw: string): string | undefined {
  let out = ''
  for (let i = 0; i < raw.length; i++) {
    const ch = raw[i]
    if (ch === '\\') {
      if (i + 1 >= raw.length) return undefined
      const next = raw[i + 1]
      if (next !== '\\' && next !== '"') return undefined
      out += next
      i++
    } else if (ch === '"') {
      return undefined
    } else {
      out += ch
    }
  }
  return out
}

export function buildCommonExpression(match: CommonMatch): string {
  const host = match.host.trim()
  const hostPart = `http.host eq "${escapeExpressionString(host)}"`
  if (match.pathKind === 'none') {
    return `(${hostPart})`
  }
  const path = match.path
  if (match.pathKind === 'exact') {
    return `(${hostPart} and http.request.uri.path eq "${escapeExpressionString(path)}")`
  }
  return `(${hostPart} and starts_with(http.request.uri.path, "${escapeExpressionString(path)}"))`
}

const QUOTED = '"((?:\\\\.|[^"\\\\])*)"'
const HOST_ONLY_RE = new RegExp(`^http\\.host\\s+eq\\s+${QUOTED}$`)
const HOST_EXACT_PATH_RE = new RegExp(
  `^http\\.host\\s+eq\\s+${QUOTED}\\s+and\\s+http\\.request\\.uri\\.path\\s+eq\\s+${QUOTED}$`
)
const HOST_PREFIX_PATH_RE = new RegExp(
  `^http\\.host\\s+eq\\s+${QUOTED}\\s+and\\s+starts_with\\(\\s*http\\.request\\.uri\\.path\\s*,\\s*${QUOTED}\\s*\\)$`
)

function stripOuterParens(expr: string): string {
  let s = expr.trim()
  while (s.startsWith('(') && s.endsWith(')')) {
    let depth = 0
    let inString = false
    let wrapsWhole = true
    for (let i = 0; i < s.length; i++) {
      const ch = s[i]
      if (inString) {
        if (ch === '\\') i++
        else if (ch === '"') inString = false
      } else if (ch === '"') {
        inString = true
      } else if (ch === '(') {
        depth++
      } else if (ch === ')') {
        depth--
        if (depth === 0 && i < s.length - 1) {
          wrapsWhole = false
          break
        }
      }
    }
    if (!wrapsWhole || depth !== 0) break
    s = s.slice(1, -1).trim()
  }
  return s
}

/**
 * Inspects a Cloudflare expression to determine whether it represents all
 * requests, the common exact-host (+ optional exact/prefix path) form, or an
 * advanced expression that must stay in advanced mode to preserve meaning.
 */
export function parseExpression(expr: string): {
  mode: MatchMode
  common?: CommonMatch
  expression: string
} {
  const trimmed = expr.trim()
  const inner = stripOuterParens(trimmed)
  if (inner === 'true') {
    return { mode: 'all', expression: 'true' }
  }
  let m = HOST_ONLY_RE.exec(inner)
  if (m) {
    const host = unescapeExpressionString(m[1])
    if (host !== undefined) {
      return {
        mode: 'common',
        common: { host, pathKind: 'none', path: '' },
        expression: trimmed,
      }
    }
  }
  m = HOST_EXACT_PATH_RE.exec(inner)
  if (m) {
    const host = unescapeExpressionString(m[1])
    const path = unescapeExpressionString(m[2])
    if (host !== undefined && path !== undefined) {
      return {
        mode: 'common',
        common: { host, pathKind: 'exact', path },
        expression: trimmed,
      }
    }
  }
  m = HOST_PREFIX_PATH_RE.exec(inner)
  if (m) {
    const host = unescapeExpressionString(m[1])
    const path = unescapeExpressionString(m[2])
    if (host !== undefined && path !== undefined) {
      return {
        mode: 'common',
        common: { host, pathKind: 'prefix', path },
        expression: trimmed,
      }
    }
  }
  return { mode: 'advanced', expression: trimmed }
}

/**
 * Reports whether an expression can return to the common matcher form without
 * changing its meaning.
 */
export function canReturnToCommon(expr: string): boolean {
  return parseExpression(expr).mode === 'common'
}

// --- Rule draft state & validation ---

export type HeaderRowDraft = {
  id: string
  name: string
  operation: CloudflareHeaderOperation
  value: string
}

export type RuleDraft = {
  description: string
  enabled: boolean
  matchMode: MatchMode
  common: CommonMatch
  expression: string
  // Single Redirect
  redirectTargetKind: CloudflareRedirectTargetKind
  redirectTargetValue: string
  redirectStatusCode: number
  redirectPreserveQuery: boolean
  // URL Rewrite
  rewritePathMode: CloudflareRewritePartMode
  rewritePathValue: string
  rewriteQueryMode: CloudflareRewritePartMode
  rewriteQueryValue: string
  // Request / Response Header Transform
  headers: HeaderRowDraft[]
  // Origin Rules
  originPort: string
  originHostHeader: string
  originSni: string
  originDnsRecord: string
}

const HEADER_NAME_RE = /^[!#$%&'*+\-.^_`|~0-9A-Za-z]+$/
const REDIRECT_STATUSES = [301, 302, 307, 308] as const

export const VALID_REDIRECT_STATUSES = REDIRECT_STATUSES

let rowCounter = 1
export function nextHeaderRowId(): string {
  rowCounter += 1
  return `hdr-${rowCounter}`
}

export function defaultRuleDraft(
  _category: CloudflareZoneRuleCategory,
  zoneName: string
): RuleDraft {
  return {
    description: '',
    enabled: true,
    matchMode: 'common',
    common: { host: zoneName, pathKind: 'none', path: '' },
    expression: zoneName
      ? buildCommonExpression({ host: zoneName, pathKind: 'none', path: '' })
      : '',
    redirectTargetKind: CloudflareRedirectTargetKind.STATIC,
    redirectTargetValue: '',
    redirectStatusCode: 301,
    redirectPreserveQuery: false,
    rewritePathMode: CloudflareRewritePartMode.STATIC,
    rewritePathValue: '',
    rewriteQueryMode: CloudflareRewritePartMode.KEEP,
    rewriteQueryValue: '',
    headers: [
      {
        id: nextHeaderRowId(),
        name: '',
        operation: CloudflareHeaderOperation.SET_STATIC,
        value: '',
      },
    ],
    originPort: '',
    originHostHeader: '',
    originSni: '',
    originDnsRecord: '',
  }
}

export function draftFromRule(
  category: CloudflareZoneRuleCategory,
  rule: CloudflareZoneRule,
  zoneName: string
): RuleDraft {
  const base = defaultRuleDraft(category, zoneName)
  const parsed = parseExpression(rule.expression)
  base.description = rule.description
  base.enabled = rule.enabled ?? true
  base.matchMode = parsed.mode
  base.expression = rule.expression
  if (parsed.common) {
    base.common = parsed.common
  }
  switch (rule.config.case) {
    case 'singleRedirect': {
      const sr = rule.config.value
      base.redirectTargetKind =
        sr.targetKind || CloudflareRedirectTargetKind.STATIC
      base.redirectTargetValue = sr.targetValue
      base.redirectStatusCode = sr.statusCode || 301
      base.redirectPreserveQuery = sr.preserveQueryString
      break
    }
    case 'urlRewrite': {
      const ur = rule.config.value
      base.rewritePathMode = ur.pathMode || CloudflareRewritePartMode.KEEP
      base.rewritePathValue = ur.pathValue
      base.rewriteQueryMode = ur.queryMode || CloudflareRewritePartMode.KEEP
      base.rewriteQueryValue = ur.queryValue
      break
    }
    case 'requestHeader':
    case 'responseHeader': {
      const hdrs = rule.config.value.headers
      base.headers =
        hdrs.length > 0
          ? hdrs.map((h) => ({
              id: nextHeaderRowId(),
              name: h.name,
              operation: h.operation,
              value: h.value,
            }))
          : base.headers
      break
    }
    case 'origin': {
      const o = rule.config.value
      base.originPort =
        o.destinationPort !== undefined ? String(o.destinationPort) : ''
      base.originHostHeader = o.hostHeader ?? ''
      base.originSni = o.sni ?? ''
      base.originDnsRecord = o.dnsRecord ?? ''
      break
    }
  }
  return base
}

/** Resolves the effective Cloudflare expression for the draft's match mode. */
export function draftExpression(draft: RuleDraft): string {
  if (draft.matchMode === 'all') return 'true'
  if (draft.matchMode === 'common') return buildCommonExpression(draft.common)
  return draft.expression.trim()
}

export type DraftErrors = Record<string, string>

export function validateRuleDraft(
  category: CloudflareZoneRuleCategory,
  draft: RuleDraft
): DraftErrors {
  const errors: DraftErrors = {}
  if (draft.description.trim().length > 512) {
    errors.description = 'Description must be at most 512 characters.'
  }

  if (draft.matchMode === 'common') {
    const host = draft.common.host.trim()
    if (!host) {
      errors.matchHost =
        'Enter an exact hostname, or explicitly choose "Match all requests".'
    } else if (/[\s/]/.test(host) || host.includes('://')) {
      errors.matchHost =
        'Enter a hostname such as app.example.com without a scheme or path.'
    }
    if (draft.common.pathKind !== 'none' && !draft.common.path) {
      errors.matchPath = 'Enter the path to match, e.g. /old-path.'
    }
  } else if (draft.matchMode === 'advanced') {
    if (!draft.expression.trim()) {
      errors.expression =
        'Enter a Cloudflare matching expression, or choose "Match all requests".'
    } else if (draft.expression.trim().length > 4096) {
      errors.expression = 'Expression must be at most 4096 characters.'
    }
  }

  switch (category) {
    case CloudflareZoneRuleCategory.SINGLE_REDIRECT: {
      const target = draft.redirectTargetValue.trim()
      if (!target) {
        errors.redirectTarget =
          draft.redirectTargetKind === CloudflareRedirectTargetKind.EXPRESSION
            ? 'Enter a target URL expression.'
            : 'Enter a target URL.'
      } else if (
        draft.redirectTargetKind === CloudflareRedirectTargetKind.STATIC &&
        /\s/.test(target)
      ) {
        errors.redirectTarget = 'A static target URL cannot contain spaces.'
      }
      if (
        !REDIRECT_STATUSES.includes(
          draft.redirectStatusCode as (typeof REDIRECT_STATUSES)[number]
        )
      ) {
        errors.redirectStatus = 'Choose 301, 302, 307 or 308.'
      }
      break
    }
    case CloudflareZoneRuleCategory.URL_REWRITE: {
      if (
        draft.rewritePathMode === CloudflareRewritePartMode.KEEP &&
        draft.rewriteQueryMode === CloudflareRewritePartMode.KEEP
      ) {
        errors.urlRewrite = 'Rewrite at least the path or the query string.'
      }
      if (
        draft.rewritePathMode === CloudflareRewritePartMode.STATIC &&
        !draft.rewritePathValue.trim()
      ) {
        errors.rewritePath = 'Enter a static path value, e.g. /new-path.'
      }
      if (
        draft.rewritePathMode === CloudflareRewritePartMode.EXPRESSION &&
        !draft.rewritePathValue.trim()
      ) {
        errors.rewritePath = 'Enter a path expression.'
      }
      if (
        draft.rewriteQueryMode === CloudflareRewritePartMode.EXPRESSION &&
        !draft.rewriteQueryValue.trim()
      ) {
        errors.rewriteQuery = 'Enter a query expression.'
      }
      break
    }
    case CloudflareZoneRuleCategory.REQUEST_HEADER:
    case CloudflareZoneRuleCategory.RESPONSE_HEADER: {
      const allowAdd = category === CloudflareZoneRuleCategory.RESPONSE_HEADER
      if (draft.headers.length === 0) {
        errors.headers = 'Add at least one header modification.'
      } else {
        const seen = new Set<string>()
        for (let i = 0; i < draft.headers.length; i++) {
          const h = draft.headers[i]
          const name = h.name.trim()
          if (!name || !HEADER_NAME_RE.test(name)) {
            errors[`header_${i}_name`] = 'Enter a valid HTTP header name.'
          } else {
            const lower = name.toLowerCase()
            if (seen.has(lower)) {
              errors[`header_${i}_name`] = `Duplicate header "${name}".`
            }
            seen.add(lower)
          }
          if (
            !allowAdd &&
            (h.operation === CloudflareHeaderOperation.ADD_STATIC ||
              h.operation === CloudflareHeaderOperation.ADD_EXPRESSION)
          ) {
            errors[`header_${i}_op`] =
              'Request Header Transform Rules only support Set or Remove.'
          }
          if (
            h.operation === CloudflareHeaderOperation.SET_STATIC ||
            h.operation === CloudflareHeaderOperation.ADD_STATIC
          ) {
            if (!h.value) {
              errors[`header_${i}_value`] = 'Enter a header value.'
            } else if (/[\r\n]/.test(h.value)) {
              errors[`header_${i}_value`] =
                'Header values cannot contain line breaks.'
            }
          } else if (
            h.operation === CloudflareHeaderOperation.SET_EXPRESSION ||
            h.operation === CloudflareHeaderOperation.ADD_EXPRESSION
          ) {
            if (!h.value.trim()) {
              errors[`header_${i}_value`] = 'Enter a header expression.'
            }
          }
        }
      }
      break
    }
    case CloudflareZoneRuleCategory.ORIGIN: {
      const portStr = draft.originPort.trim()
      const host = draft.originHostHeader.trim()
      const sni = draft.originSni.trim()
      const dns = draft.originDnsRecord.trim()
      if (!portStr && !host && !sni && !dns) {
        errors.origin =
          'Set at least one override (destination port, Host header, SNI or DNS target).'
      }
      if (portStr) {
        if (!/^\d+$/.test(portStr)) {
          errors.originPort = 'Port must be a number between 1 and 65535.'
        } else {
          const port = Number(portStr)
          if (port < 1 || port > 65535) {
            errors.originPort = 'Port must be between 1 and 65535.'
          }
        }
      }
      const badHost = (v: string) => /[\s/]/.test(v) || v.includes('://')
      if (host && badHost(host)) {
        errors.originHostHeader =
          'Enter a hostname without scheme, path or whitespace.'
      }
      if (sni && badHost(sni)) {
        errors.originSni =
          'Enter a hostname without scheme, path or whitespace.'
      }
      if (dns && badHost(dns)) {
        errors.originDnsRecord =
          'Enter a hostname without scheme, path or whitespace.'
      }
      break
    }
  }

  return errors
}

export function ruleInputFromDraft(
  category: CloudflareZoneRuleCategory,
  draft: RuleDraft
): CloudflareZoneRuleInit {
  const base: CloudflareZoneRuleInit = {
    description: draft.description.trim(),
    expression: draftExpression(draft),
    enabled: draft.enabled,
  }
  switch (category) {
    case CloudflareZoneRuleCategory.SINGLE_REDIRECT:
      return {
        ...base,
        config: {
          case: 'singleRedirect',
          value: {
            targetKind: draft.redirectTargetKind,
            targetValue: draft.redirectTargetValue.trim(),
            statusCode: draft.redirectStatusCode,
            preserveQueryString: draft.redirectPreserveQuery,
          },
        },
      }
    case CloudflareZoneRuleCategory.URL_REWRITE:
      return {
        ...base,
        config: {
          case: 'urlRewrite',
          value: {
            pathMode: draft.rewritePathMode,
            pathValue:
              draft.rewritePathMode === CloudflareRewritePartMode.EXPRESSION
                ? draft.rewritePathValue.trim()
                : draft.rewritePathMode === CloudflareRewritePartMode.STATIC
                  ? draft.rewritePathValue
                  : '',
            queryMode: draft.rewriteQueryMode,
            queryValue:
              draft.rewriteQueryMode === CloudflareRewritePartMode.EXPRESSION
                ? draft.rewriteQueryValue.trim()
                : draft.rewriteQueryMode === CloudflareRewritePartMode.STATIC
                  ? draft.rewriteQueryValue
                  : '',
          },
        },
      }
    case CloudflareZoneRuleCategory.REQUEST_HEADER:
      return {
        ...base,
        config: {
          case: 'requestHeader',
          value: {
            headers: draft.headers.map((h) => ({
              name: h.name.trim(),
              operation: h.operation,
              value:
                h.operation === CloudflareHeaderOperation.REMOVE ? '' : h.value,
            })),
          },
        },
      }
    case CloudflareZoneRuleCategory.RESPONSE_HEADER:
      return {
        ...base,
        config: {
          case: 'responseHeader',
          value: {
            headers: draft.headers.map((h) => ({
              name: h.name.trim(),
              operation: h.operation,
              value:
                h.operation === CloudflareHeaderOperation.REMOVE ? '' : h.value,
            })),
          },
        },
      }
    case CloudflareZoneRuleCategory.ORIGIN: {
      const portStr = draft.originPort.trim()
      const host = draft.originHostHeader.trim()
      const sni = draft.originSni.trim()
      const dns = draft.originDnsRecord.trim()
      return {
        ...base,
        config: {
          case: 'origin',
          value: {
            destinationPort: portStr ? Number(portStr) : undefined,
            hostHeader: host || undefined,
            sni: sni || undefined,
            dnsRecord: dns || undefined,
          },
        },
      }
    }
    default:
      return base
  }
}

// --- Summaries & Confirmation Wording ---

function headerOpShort(op: CloudflareHeaderOperation): string {
  switch (op) {
    case CloudflareHeaderOperation.SET_STATIC:
      return 'set'
    case CloudflareHeaderOperation.SET_EXPRESSION:
      return 'set expr'
    case CloudflareHeaderOperation.ADD_STATIC:
      return 'add'
    case CloudflareHeaderOperation.ADD_EXPRESSION:
      return 'add expr'
    case CloudflareHeaderOperation.REMOVE:
      return 'remove'
    default:
      return 'modify'
  }
}

export function summarizeRuleAction(rule: CloudflareZoneRule): string {
  switch (rule.config.case) {
    case 'singleRedirect': {
      const sr = rule.config.value
      const kind =
        sr.targetKind === CloudflareRedirectTargetKind.EXPRESSION
          ? 'expr'
          : 'URL'
      const qs = sr.preserveQueryString ? ' · keep query' : ''
      return `HTTP ${sr.statusCode} → (${kind}) ${sr.targetValue}${qs}`
    }
    case 'urlRewrite': {
      const ur = rule.config.value
      const parts: string[] = []
      if (ur.pathMode === CloudflareRewritePartMode.STATIC)
        parts.push(`path = "${ur.pathValue}"`)
      else if (ur.pathMode === CloudflareRewritePartMode.EXPRESSION)
        parts.push(`path = expr(${ur.pathValue})`)
      if (ur.queryMode === CloudflareRewritePartMode.STATIC)
        parts.push(`query = "${ur.queryValue}"`)
      else if (ur.queryMode === CloudflareRewritePartMode.EXPRESSION)
        parts.push(`query = expr(${ur.queryValue})`)
      return parts.join(' · ') || 'URL rewrite'
    }
    case 'requestHeader':
    case 'responseHeader': {
      return rule.config.value.headers
        .map((h) =>
          h.operation === CloudflareHeaderOperation.REMOVE
            ? `remove ${h.name}`
            : `${headerOpShort(h.operation)} ${h.name}`
        )
        .join(', ')
    }
    case 'origin': {
      const o = rule.config.value
      const parts: string[] = []
      if (o.destinationPort !== undefined)
        parts.push(`port ${o.destinationPort}`)
      if (o.hostHeader) parts.push(`Host ${o.hostHeader}`)
      if (o.sni) parts.push(`SNI ${o.sni}`)
      if (o.dnsRecord) parts.push(`DNS ${o.dnsRecord}`)
      return parts.join(' · ') || 'Origin override'
    }
    default:
      return rule.action || 'Unsupported configuration'
  }
}

export function summarizeDraftAction(
  category: CloudflareZoneRuleCategory,
  draft: RuleDraft
): string {
  const init = ruleInputFromDraft(category, draft)
  return summarizeRuleAction({
    id: '',
    version: '',
    description: init.description ?? '',
    expression: init.expression ?? '',
    action: '',
    enabled: init.enabled,
    readOnly: false,
    readOnlyReason: '',
    lastUpdated: '',
    rawConfigJson: '',
    config: init.config ?? { case: undefined },
  } as CloudflareZoneRule)
}

export type ConfirmationItem = {
  label: string
  value: string
  mono?: boolean
}

export function buildRuleConfirmationItems(
  category: CloudflareZoneRuleCategory,
  draft: RuleDraft,
  mode: 'create' | 'edit',
  existingRule?: CloudflareZoneRule
): ConfirmationItem[] {
  const items: ConfirmationItem[] = [
    { label: 'Category', value: categoryMeta(category).label },
    {
      label: mode === 'create' ? 'Action' : 'Rule',
      value:
        mode === 'create'
          ? 'Create rule (appended to end of category)'
          : existingRule?.description || existingRule?.id || 'Existing rule',
    },
    {
      label: 'Description',
      value: draft.description.trim() || '(no description)',
    },
    {
      label: 'Match',
      value: draftExpression(draft),
      mono: true,
    },
    {
      label: 'Behavior',
      value: summarizeDraftAction(category, draft),
    },
    {
      label: 'State',
      value: draft.enabled ? 'Enabled' : 'Disabled',
    },
  ]
  return items
}

export const TRAFFIC_NOT_TESTED_NOTE =
  'A confirmed save means Cloudflare accepted this rule configuration; Neo Box does not send test traffic to verify live request matching or origin reachability.'

export const ORIGIN_NO_DNS_NOTE =
  'Origin DNS overrides change this rule only and never create or modify DNS records.'

// --- Bounded Reordering (Respects Read-Only Rules) ---

export function canMoveRule(
  rules: CloudflareZoneRule[],
  index: number,
  direction: 'up' | 'down'
): { allowed: boolean; reason?: string } {
  const rule = rules[index]
  if (!rule) return { allowed: false, reason: 'Rule not found.' }
  if (rule.readOnly) {
    return {
      allowed: false,
      reason: rule.readOnlyReason || 'Read-only rules cannot be moved.',
    }
  }
  const target = direction === 'up' ? index - 1 : index + 1
  if (target < 0 || target >= rules.length) {
    return {
      allowed: false,
      reason:
        direction === 'up'
          ? 'Already first in this category.'
          : 'Already last in this category.',
    }
  }
  if (rules[target].readOnly) {
    return {
      allowed: false,
      reason:
        'Cannot move across a read-only rule because that would indirectly change its position.',
    }
  }
  return { allowed: true }
}

export function moveRuleIds(
  rules: CloudflareZoneRule[],
  index: number,
  direction: 'up' | 'down'
): string[] | undefined {
  if (!canMoveRule(rules, index, direction).allowed) return undefined
  const target = direction === 'up' ? index - 1 : index + 1
  const ids = rules.map((r) => r.id)
  const tmp = ids[index]
  ids[index] = ids[target]
  ids[target] = tmp
  return ids
}

export function describeRuleLabel(
  rule: CloudflareZoneRule,
  index: number
): string {
  return rule.description || rule.id || `Rule #${index + 1}`
}

// --- Error Classification ---

export type ZoneRuleErrorInfo = {
  kind:
    | 'conflict'
    | 'uncertain'
    | 'permission'
    | 'quota'
    | 'feature'
    | 'token'
    | 'readonly'
    | 'invalid'
    | 'error'
  title: string
  message: string
  uncertain: boolean
}

export function zoneRuleChangeError(
  err: unknown,
  category: CloudflareZoneRuleCategory
): ZoneRuleErrorInfo {
  const label = categoryMeta(category).label
  const e = ConnectError.from(err)
  const raw = e.rawMessage
  const lower = raw.toLowerCase()

  switch (e.code) {
    case Code.Aborted:
      return {
        kind: 'conflict',
        title: `${label} changed since it was loaded`,
        message:
          raw ||
          'Refresh this category to inspect external edits. Your entered values have been kept.',
        uncertain: false,
      }
    case Code.Unknown:
      return {
        kind: 'uncertain',
        title: 'Result unknown',
        message: `Cloudflare did not confirm the change to ${label}, so it may or may not have been applied. Refresh and verify the live rules before trying again.`,
        uncertain: true,
      }
    case Code.PermissionDenied:
      return {
        kind: 'permission',
        title: 'API token lacks permission',
        message: raw,
        uncertain: false,
      }
    case Code.ResourceExhausted:
      return {
        kind: 'quota',
        title: 'Cloudflare rule quota reached',
        message: raw,
        uncertain: false,
      }
    case Code.InvalidArgument:
      return {
        kind: 'invalid',
        title: 'Cloudflare rejected the rule input',
        message: raw,
        uncertain: false,
      }
    case Code.FailedPrecondition:
      if (lower.includes('api token')) {
        return {
          kind: 'token',
          title: 'Cloudflare rejected the API token',
          message: raw,
          uncertain: false,
        }
      }
      if (lower.includes('plan') || lower.includes('entitlement')) {
        return {
          kind: 'feature',
          title: 'Not available on this Cloudflare plan',
          message: raw,
          uncertain: false,
        }
      }
      if (lower.includes('read-only')) {
        return {
          kind: 'readonly',
          title: 'Rule is protected as read-only',
          message: raw,
          uncertain: false,
        }
      }
      return {
        kind: 'error',
        title: 'Change could not be applied',
        message: raw,
        uncertain: false,
      }
    default:
      return {
        kind: 'error',
        title: 'Change failed',
        message: raw,
        uncertain: false,
      }
  }
}

export function categoryIssueTitle(kind: CloudflareZoneRulesErrorKind): string {
  switch (kind) {
    case CloudflareZoneRulesErrorKind.PERMISSION_DENIED:
      return 'Missing API token permission'
    case CloudflareZoneRulesErrorKind.TOKEN_REJECTED:
      return 'Cloudflare rejected the API token'
    case CloudflareZoneRulesErrorKind.FEATURE_RESTRICTED:
      return 'Restricted by Cloudflare plan or entitlement'
    default:
      return 'Could not load this category'
  }
}
