import { CloudflareAddressKind, type CloudflareAddress } from '@/api/cloudflare'

/** An address ready to show: open it when it is a URL, copy it always. */
export type AddressLink = {
  key: string
  /** What is shown and copied, as Cloudflare reported it. */
  text: string
  /** Set only for an http(s) URL that can be opened as is. */
  href?: string
  kind: CloudflareAddressKind
  /** A route pattern with wildcards: a pattern, not a URL. */
  pattern: boolean
  /** A custom domain status other than active, e.g. pending. */
  note?: string
}

export const addressKindLabel: Record<CloudflareAddressKind, string> = {
  [CloudflareAddressKind.UNSPECIFIED]: 'Address',
  [CloudflareAddressKind.WORKERS_DEV]: 'workers.dev',
  [CloudflareAddressKind.CUSTOM_DOMAIN]: 'Custom domain',
  [CloudflareAddressKind.ROUTE]: 'Route',
  [CloudflareAddressKind.PAGES_DEV]: 'pages.dev',
  [CloudflareAddressKind.PRODUCTION]: 'Production deployment',
}

const hasScheme = /^[a-z][a-z0-9+.-]*:/i

/** The http(s) URL text points at, or undefined. A bare host is https. */
function toHref(text: string): string | undefined {
  const candidate = hasScheme.test(text) ? text : `https://${text}`
  try {
    const u = new URL(candidate)
    return u.protocol === 'https:' || u.protocol === 'http:'
      ? candidate
      : undefined
  } catch {
    return undefined
  }
}

/** Compares URLs by scheme, host (case-insensitive), path and query. */
function sameURLKey(href: string) {
  const u = new URL(href)
  return `${u.protocol}//${u.host.toLowerCase()}${u.pathname.replace(/\/+$/, '')}${u.search}`
}

/** A custom domain status Neo Box couldn't read (see the proto). */
const UNKNOWN_STATUS = 'unknown'

/**
 * Turns a Worker's or Pages project's addresses into links, each once.
 * Wildcard routes stay patterns, and a custom domain that isn't active
 * (pending, or of unknown status) may not answer, so it isn't linked; only
 * http(s) is ever linked.
 */
export function addressLinks(addresses: CloudflareAddress[]): AddressLink[] {
  const seen = new Set<string>()
  const out: AddressLink[] = []
  for (const a of addresses) {
    const text = a.url.trim()
    if (!text) continue
    const pattern = a.kind === CloudflareAddressKind.ROUTE && text.includes('*')
    const inactive = !!a.status && a.status !== 'active'
    const url = pattern ? undefined : toHref(text)
    const key = url ? sameURLKey(url) : text.toLowerCase()
    if (seen.has(key)) continue
    seen.add(key)
    out.push({
      key,
      text,
      href: inactive ? undefined : url,
      kind: a.kind,
      pattern,
      note: !inactive
        ? undefined
        : a.status === UNKNOWN_STATUS
          ? 'status unknown'
          : a.status,
    })
  }
  return out
}
