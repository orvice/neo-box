import type {
  CloudflareDNSRecord,
  CloudflareDNSRecordInit,
} from '@/api/cloudflare'

/** The record types Neo Box changes; others are shown read-only. */
export const EDITABLE_TYPES = [
  'A',
  'AAAA',
  'CNAME',
  'TXT',
  'MX',
  'NS',
  'SRV',
  'CAA',
] as const

const PROXIABLE_TYPES = ['A', 'AAAA', 'CNAME']

export const CAA_TAGS = ['issue', 'issuewild', 'iodef'] as const

export const TTL_OPTIONS: { value: string; label: string }[] = [
  1, 60, 120, 300, 600, 900, 1800, 3600, 7200, 18000, 43200, 86400,
].map((n) => ({ value: String(n), label: formatTTL(n) }))

/** The record form's fields, as typed. */
export type RecordForm = {
  type: string
  name: string
  content: string
  ttl: string
  proxied: boolean
  priority: string
  srvPriority: string
  srvWeight: string
  srvPort: string
  srvTarget: string
  caaFlags: string
  caaTag: string
  caaValue: string
}

export type FormErrors = Partial<Record<keyof RecordForm, string>>

export function emptyForm(type = 'A'): RecordForm {
  return {
    type,
    name: '',
    content: '',
    ttl: '1',
    proxied: false,
    priority: '10',
    srvPriority: '10',
    srvWeight: '5',
    srvPort: '',
    srvTarget: '',
    caaFlags: '0',
    caaTag: 'issue',
    caaValue: '',
  }
}

export function formFromRecord(r: CloudflareDNSRecord): RecordForm {
  return {
    ...emptyForm(r.type),
    name: r.name,
    content: r.type === 'SRV' || r.type === 'CAA' ? '' : r.content,
    ttl: String(r.ttl || 1),
    proxied: r.proxied,
    priority: String(r.priority),
    srvPriority: String(r.srv?.priority ?? 10),
    srvWeight: String(r.srv?.weight ?? 5),
    srvPort: r.srv ? String(r.srv.port) : '',
    srvTarget: r.srv?.target ?? '',
    caaFlags: String(r.caa?.flags ?? 0),
    caaTag: r.caa?.tag || 'issue',
    caaValue: r.caa?.value ?? '',
  }
}

/**
 * Whether the proxy switch applies: by type for a new record, by what
 * Cloudflare says for an existing one.
 */
export function proxyApplies(type: string, existing?: CloudflareDNSRecord) {
  return existing ? existing.proxiable : PROXIABLE_TYPES.includes(type)
}

const ipv4 =
  /^(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)(\.(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)){3}$/

function isIPv6(v: string) {
  if (!v.includes(':')) return false
  try {
    new URL(`http://[${v}]`)
    return true
  } catch {
    return false
  }
}

function isHostname(v: string) {
  const s = v.replace(/\.$/, '')
  if (!s || s.length > 253 || /[\s/:@]/.test(s)) return false
  return s.split('.').every((l) => l.length > 0 && l.length <= 63)
}

function intIn(v: string, lo: number, hi: number) {
  if (!/^\d+$/.test(v.trim())) return false
  const n = Number(v)
  return n >= lo && n <= hi
}

/** Checks the form the way the server does; Cloudflare checks the rest. */
export function validateForm(f: RecordForm): FormErrors {
  const e: FormErrors = {}
  const name = f.name.trim()
  const content = f.content.trim()
  if (!name) e.name = 'Required'
  else if (name.length > 255 || /\s/.test(name))
    e.name = 'A DNS name without spaces, at most 255 characters'
  const ttl = Number(f.ttl)
  if (!(ttl === 1 || intIn(f.ttl, 30, 86400)))
    e.ttl = 'Auto, or 30 to 86400 seconds'
  switch (f.type) {
    case 'A':
      if (!ipv4.test(content)) e.content = 'An IPv4 address, e.g. 192.0.2.1'
      break
    case 'AAAA':
      if (!isIPv6(content)) e.content = 'An IPv6 address, e.g. 2001:db8::1'
      break
    case 'CNAME':
    case 'NS':
      if (!isHostname(content)) e.content = 'A host name'
      break
    case 'TXT':
      if (!content) e.content = 'Required'
      break
    case 'MX':
      if (!isHostname(content)) e.content = 'A mail server host name'
      if (!intIn(f.priority, 0, 65535)) e.priority = '0 to 65535'
      break
    case 'SRV': {
      const labels = name.split('.')
      if (
        name &&
        !(
          labels.length >= 2 &&
          labels[0].startsWith('_') &&
          labels[1].startsWith('_')
        )
      )
        e.name = 'Starts with _service._protocol, e.g. _sip._tcp'
      if (!intIn(f.srvPriority, 0, 65535)) e.srvPriority = '0 to 65535'
      if (!intIn(f.srvWeight, 0, 65535)) e.srvWeight = '0 to 65535'
      if (!intIn(f.srvPort, 0, 65535)) e.srvPort = '0 to 65535'
      const target = f.srvTarget.trim()
      if (target !== '.' && !isHostname(target))
        e.srvTarget = 'A host name, or . for no service'
      break
    }
    case 'CAA':
      if (!intIn(f.caaFlags, 0, 255)) e.caaFlags = '0 to 255'
      if (!(CAA_TAGS as readonly string[]).includes(f.caaTag))
        e.caaTag = 'issue, issuewild or iodef'
      if (!f.caaValue.trim()) e.caaValue = 'Required'
      break
    default:
      e.type = 'Not a type Neo Box edits'
  }
  return e
}

/** The request for the form. proxyAllowed comes from proxyApplies. */
export function inputFromForm(
  f: RecordForm,
  proxyAllowed: boolean
): CloudflareDNSRecordInit {
  const input: CloudflareDNSRecordInit = {
    type: f.type,
    name: f.name.trim(),
    ttl: Number(f.ttl) || 1,
    proxied: proxyAllowed && f.proxied,
  }
  switch (f.type) {
    case 'SRV':
      input.srv = {
        priority: Number(f.srvPriority),
        weight: Number(f.srvWeight),
        port: Number(f.srvPort),
        target: f.srvTarget.trim(),
      }
      break
    case 'CAA':
      input.caa = {
        flags: Number(f.caaFlags),
        tag: f.caaTag,
        value: f.caaValue.trim(),
      }
      break
    case 'MX':
      input.content = f.content.trim()
      input.priority = Number(f.priority)
      break
    default:
      input.content = f.content.trim()
  }
  return input
}

/** A record's value as one line. */
export function contentSummary(r: CloudflareDNSRecord) {
  if (r.type === 'MX') return `${r.priority} ${r.content}`
  if (r.type === 'SRV' && r.srv)
    return `${r.srv.priority} ${r.srv.weight} ${r.srv.port} ${r.srv.target}`
  if (r.type === 'CAA' && r.caa)
    return `${r.caa.flags} ${r.caa.tag} "${r.caa.value}"`
  return r.content
}

export function formatTTL(ttl: number) {
  if (ttl === 1) return 'Auto'
  if (ttl === 86400) return '1 day'
  if (ttl >= 3600 && ttl % 3600 === 0) return `${ttl / 3600} hr`
  if (ttl >= 60 && ttl % 60 === 0) return `${ttl / 60} min`
  return `${ttl} s`
}

/**
 * What a change sent to Cloudflare (an operation's request), as one line:
 * "A www → 192.0.2.1 · TTL Auto · proxied". Only the fields sent appear.
 */
export function requestSummary(req: Record<string, unknown>) {
  const parts: string[] = []
  const name = [req.type, req.name].filter(Boolean).join(' ')
  const data = req.data as Record<string, unknown> | undefined
  let value = ''
  if (data && 'port' in data)
    value = `${data.priority} ${data.weight} ${data.port} ${data.target}`
  else if (data && 'tag' in data)
    value = `${data.flags} ${data.tag} "${data.value}"`
  else if (req.content !== undefined)
    value =
      req.priority !== undefined
        ? `${req.priority} ${req.content}`
        : String(req.content)
  if (name || value) parts.push(value ? `${name} → ${value}` : name)
  if (typeof req.ttl === 'number') parts.push(`TTL ${formatTTL(req.ttl)}`)
  if (typeof req.proxied === 'boolean')
    parts.push(req.proxied ? 'proxied' : 'DNS only')
  return parts.join(' · ')
}
