import { timestampDate, type Timestamp } from '@bufbuild/protobuf/wkt'
import {
  CloudflareTunnelRecordStatus as RecordStatus,
  CloudflareTunnelStatus as Status,
  type CloudflareTunnel,
  type CloudflareTunnelHostname,
  type CloudflareTunnelRecordResult,
} from '@/api/cloudflare'

/** How a Tunnel's status reads, with the classes that colour its badge. */
export function tunnelStatus(t: CloudflareTunnel): {
  label: string
  className: string
  hint: string
} {
  switch (t.status) {
    case Status.HEALTHY:
      return {
        label: 'Healthy',
        className: 'bg-success-muted text-success-foreground',
        hint: 'cloudflared is connected.',
      }
    case Status.DEGRADED:
      return {
        label: 'Degraded',
        className: 'bg-warning-muted text-warning-foreground',
        hint: 'cloudflared is connected, but not with all its connections.',
      }
    case Status.DOWN:
      return {
        label: 'Down',
        className: 'bg-danger-muted text-danger-foreground',
        hint: 'It has connected before; nothing is connected now.',
      }
    case Status.INACTIVE:
      return {
        label: 'Inactive',
        className: 'bg-muted text-muted-foreground',
        hint: 'cloudflared has never connected.',
      }
  }
  return { label: '-', className: 'bg-muted text-muted-foreground', hint: '' }
}

/** The commands that run a remotely managed Tunnel with its token. */
export function runCommands(
  token: string
): { label: string; command: string }[] {
  return [
    { label: 'Run', command: `cloudflared tunnel run --token ${token}` },
    {
      label: 'Install as a service',
      command: `cloudflared service install ${token}`,
    },
    {
      label: 'Docker',
      command: `docker run -d --restart unless-stopped cloudflare/cloudflared:latest tunnel --no-autoupdate run --token ${token}`,
    },
  ]
}

/**
 * The URL a hostname answers on, or undefined for a wildcard (it is a
 * pattern, not a place to open).
 */
export function hostnameUrl(
  h: Pick<CloudflareTunnelHostname, 'hostname' | 'path'>
) {
  if (!h.hostname || h.hostname.includes('*')) return undefined
  return `https://${h.hostname}`
}

/** What a hostname form holds; the hostname is only asked when adding. */
export type HostnameForm = {
  hostname: string
  path: string
  service: string
  createDns: boolean
}

export type HostnameErrors = Partial<
  Record<'hostname' | 'path' | 'service', string>
>

const LABEL = /^[a-z0-9_]([a-z0-9_-]*[a-z0-9_])?$/
const SCHEMES = ['http', 'https', 'ws', 'wss', 'tcp', 'ssh', 'rdp', 'smb']

export function emptyHostnameForm(): HostnameForm {
  return { hostname: '', path: '', service: '', createDns: true }
}

export function formFromHostname(h: CloudflareTunnelHostname): HostnameForm {
  return {
    hostname: h.hostname,
    path: h.path,
    service: h.service,
    createDns: false,
  }
}

/**
 * Light checks that save a round trip; the backend and Cloudflare check
 * again and have the last word.
 */
export function validateHostnameForm(
  form: HostnameForm,
  withHostname: boolean
): HostnameErrors {
  const errors: HostnameErrors = {}
  if (withHostname) {
    const h = form.hostname.trim().toLowerCase()
    const labels = h.split('.')
    if (!h) errors.hostname = 'Enter the public hostname.'
    else if (
      labels.length < 2 ||
      !labels.every((l, i) => (i === 0 && l === '*') || LABEL.test(l))
    )
      errors.hostname =
        'Use a domain name such as app.example.com, without a scheme, port or path.'
  }
  if (form.path.trim()) {
    try {
      new RegExp(form.path.trim())
    } catch {
      errors.path = 'The path is a regular expression, and this is not one.'
    }
  }
  const message = serviceProblem(form.service.trim())
  if (message) errors.service = message
  return errors
}

function serviceProblem(s: string): string | undefined {
  if (!s) return 'Enter where requests go.'
  if (s === 'hello_world' || s === 'bastion') return
  const status = /^http_status:(\d+)$/.exec(s)
  if (status) {
    const n = Number(status[1])
    return n >= 100 && n <= 599
      ? undefined
      : 'http_status needs a status code between 100 and 599.'
  }
  if (/^unix(\+tls)?:./.test(s) && !/^unix(\+tls)?:\/?$/.test(s)) return
  let u: URL
  try {
    u = new URL(s)
  } catch {
    return 'Use an origin URL such as http://localhost:8080.'
  }
  const scheme = u.protocol.replace(/:$/, '')
  if (!u.hostname || !SCHEMES.includes(scheme))
    return `Use an origin URL such as http://localhost:8080 (${SCHEMES.join(', ')}).`
  if (
    (u.pathname !== '/' && u.pathname !== '') ||
    u.search ||
    u.hash ||
    u.username
  )
    return 'Give only scheme, host and port; put a path in the path field.'
}

/** How the DNS part of a hostname change went, for a message to the user. */
export function recordOutcome(r: CloudflareTunnelRecordResult | undefined): {
  tone: 'success' | 'warning' | 'none'
  message: string
} {
  if (!r) return { tone: 'none', message: '' }
  switch (r.status) {
    case RecordStatus.CREATED:
      return {
        tone: 'success',
        message: 'A proxied CNAME to the tunnel was created.',
      }
    case RecordStatus.EXISTS:
      return {
        tone: 'success',
        message: r.message || 'DNS already points at the tunnel.',
      }
    case RecordStatus.DELETED:
      return {
        tone: 'success',
        message: 'The CNAME to the tunnel was deleted.',
      }
    case RecordStatus.KEPT:
    case RecordStatus.NO_ZONE:
    case RecordStatus.CONFLICT:
    case RecordStatus.FAILED:
      return { tone: 'warning', message: r.message || 'DNS was not changed.' }
  }
  return { tone: 'none', message: '' }
}

/** A moment in the viewer's time zone, or "-" when there is none. */
export function formatTime(ts: Timestamp | undefined): string {
  return ts ? timestampDate(ts).toLocaleString() : '-'
}
