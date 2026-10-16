import {
  CloudflareEmailAction as Action,
  type CloudflareEmailAddress,
  type CloudflareEmailCatchAll,
  type CloudflareEmailDNSRecord,
  type CloudflareEmailRoutingSettings,
  type CloudflareEmailRule,
} from '@/api/cloudflare'

// Light checks that save a round trip; the backend and Cloudflare check
// again and have the last word.

const ADDRESS = /^[^\s@]+@[^\s@]+\.[^\s@]+$/

/** The problem with an email address, or undefined when it looks right. */
export function emailProblem(input: string): string | undefined {
  const a = input.trim()
  if (!a) return 'Enter the email address.'
  if (a.length > 90 || !ADDRESS.test(a))
    return 'Use an address such as name@example.com.'
}

/** The problem with the address a rule matches in a Zone, or undefined. */
export function ruleAddressProblem(
  input: string,
  zoneName: string
): string | undefined {
  const p = emailProblem(input)
  if (p) return p
  const domain = input.trim().split('@').pop() ?? ''
  if (domain.toLowerCase() !== zoneName.toLowerCase())
    return `The address must end in @${zoneName}.`
}

/** A forward needs a destination address; a drop needs none. */
export function targetProblem(
  action: Action,
  target: string
): string | undefined {
  if (action !== Action.FORWARD) return undefined
  if (emailProblem(target)) return 'Enter the destination address.'
}

/** What a rule does, in words. */
export function ruleSummary(
  r: Pick<CloudflareEmailRule, 'action' | 'target'>
): string {
  switch (r.action) {
    case Action.FORWARD:
      return `Forward to ${r.target}`
    case Action.DROP:
      return 'Drop'
    case Action.WORKER:
      return `Send to Worker ${r.target}`
  }
  return '-'
}

/** What the catch-all does, in words. */
export function catchAllSummary(
  c: CloudflareEmailCatchAll | undefined
): string {
  if (!c) return 'Unknown'
  if (!c.enabled) return 'Off'
  return ruleSummary(c)
}

export type CatchAllMode = 'off' | 'drop' | 'forward'

export function catchAllMode(c: CloudflareEmailCatchAll): CatchAllMode {
  if (!c.enabled) return 'off'
  return c.action === Action.FORWARD ? 'forward' : 'drop'
}

/** Off/drop/forward controls apply only to catch-alls Neo Box can edit. */
export function catchAllConfigurable(c: CloudflareEmailCatchAll): boolean {
  if (!c.editable) return false
  if (c.enabled && c.action === Action.WORKER) return false
  return true
}

/** The change request a catch-all choice stands for. */
export function catchAllRequest(
  mode: CatchAllMode,
  target: string
): { enabled: boolean; action: Action; target: string } {
  switch (mode) {
    case 'forward':
      return { enabled: true, action: Action.FORWARD, target: target.trim() }
    case 'drop':
      return { enabled: true, action: Action.DROP, target: '' }
  }
  return { enabled: false, action: Action.DROP, target: '' }
}

export function catchAllProblem(
  mode: CatchAllMode,
  target: string
): string | undefined {
  return mode === 'forward' ? emailProblem(target) : undefined
}

/** How a destination address stands, with the classes that colour its badge. */
export function addressStatus(a: Pick<CloudflareEmailAddress, 'verified'>): {
  label: string
  className: string
} {
  return a.verified
    ? {
        label: 'Verified',
        className: 'bg-success-muted text-success-foreground',
      }
    : {
        label: 'Waiting for verification',
        className: 'bg-warning-muted text-warning-foreground',
      }
}

/** The verified destination addresses, sorted. */
export function verifiedEmails(addresses: CloudflareEmailAddress[]): string[] {
  return addresses
    .filter((a) => a.verified)
    .map((a) => a.email)
    .sort((x, y) => x.localeCompare(y))
}

/** How Email Routing stands in a Zone, with the classes that colour its badge. */
export function routingState(s: CloudflareEmailRoutingSettings | undefined): {
  label: string
  className: string
  hint: string
} {
  if (!s)
    return {
      label: 'Loading',
      className: 'bg-muted text-muted-foreground',
      hint: '',
    }
  if (!s.enabled)
    return {
      label: 'Not enabled',
      className: 'bg-muted text-muted-foreground',
      hint: 'Enabling adds the MX and SPF records Cloudflare needs, and locks them.',
    }
  if (s.status === 'ready')
    return {
      label: 'Active',
      className: 'bg-success-muted text-success-foreground',
      hint: 'Mail to this zone is routed by the rules below.',
    }
  return {
    label: 'Needs attention',
    className: 'bg-warning-muted text-warning-foreground',
    hint: `Cloudflare reports the status "${s.status}". Check the DNS records in the Cloudflare dashboard.`,
  }
}

/** One required DNS record on a line. */
export function dnsRecordSummary(r: CloudflareEmailDNSRecord): string {
  const base = `${r.type} ${r.name} → ${r.content}`
  return r.type === 'MX' ? `${base} (priority ${r.priority})` : base
}
