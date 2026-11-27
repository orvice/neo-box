import { timestampDate, type Timestamp } from '@bufbuild/protobuf/wkt'
import { Code, ConnectError } from '@connectrpc/connect'
import type { Connection } from '@/api/connections'

export const API_ACCESS_DOCS = 'https://porkbun.com/account/api'
export const API_DOCS = 'https://porkbun.com/api/json/v3/documentation'

export function porkbunConfig(connection: Connection | undefined) {
  return connection?.config.case === 'porkbun'
    ? connection.config.value
    : undefined
}

export function isSandboxKey(apiKey: string): boolean {
  return apiKey.trim().startsWith('pk1_sb_')
}

export function maskApiKey(apiKey: string): string {
  const k = apiKey.trim()
  if (k.length <= 16) return k
  return `${k.slice(0, 10)}…${k.slice(-6)}`
}

export type ExpiryStatus = {
  dateText: string
  daysLeft: number | null
  label: string
  tone: 'expired' | 'soon' | 'ok' | 'none'
}

/** Formats a domain's expiry timestamp and days remaining. */
export function expiryStatus(
  expiresAt: Timestamp | undefined,
  now: Date = new Date()
): ExpiryStatus {
  if (!expiresAt) {
    return { dateText: '-', daysLeft: null, label: '-', tone: 'none' }
  }
  const d = timestampDate(expiresAt)
  const dateText = d.toISOString().slice(0, 10)
  const diffMs = d.getTime() - now.getTime()
  const daysLeft = Math.ceil(diffMs / 86_400_000)
  if (diffMs < 0) {
    const ago = Math.max(1, Math.floor(-diffMs / 86_400_000))
    return {
      dateText,
      daysLeft,
      label: `Expired ${ago}d ago`,
      tone: 'expired',
    }
  }
  if (daysLeft <= 30) {
    return {
      dateText,
      daysLeft,
      label: daysLeft === 0 ? 'Expires today' : `${daysLeft}d left`,
      tone: 'soon',
    }
  }
  return {
    dateText,
    daysLeft,
    label: `${daysLeft}d left`,
    tone: 'ok',
  }
}

export function formatUtcDate(ts: Timestamp | undefined): string {
  if (!ts) return '-'
  return timestampDate(ts).toISOString().slice(0, 10)
}

export function formatTTL(seconds: number): string {
  if (!seconds || seconds <= 0) return '-'
  if (seconds % 86400 === 0) return `${seconds / 86400}d`
  if (seconds % 3600 === 0) return `${seconds / 3600}h`
  if (seconds % 60 === 0) return `${seconds / 60}m`
  return `${seconds}s`
}

const DNSSEC_ALGORITHMS: Record<string, string> = {
  '5': 'RSASHA1',
  '7': 'RSASHA1-NSEC3-SHA1',
  '8': 'RSASHA256',
  '10': 'RSASHA512',
  '13': 'ECDSAP256SHA256',
  '14': 'ECDSAP384SHA384',
  '15': 'ED25519',
  '16': 'ED448',
}

const DNSSEC_DIGEST_TYPES: Record<string, string> = {
  '1': 'SHA-1',
  '2': 'SHA-256',
  '4': 'SHA-384',
}

export function formatDnssecAlgorithm(alg: string): string {
  const name = DNSSEC_ALGORITHMS[alg]
  return name ? `${alg} (${name})` : alg || '-'
}

export function formatDnssecDigestType(digestType: string): string {
  const name = DNSSEC_DIGEST_TYPES[digestType]
  return name ? `${digestType} (${name})` : digestType || '-'
}

export type PorkbunProblem = {
  kind:
    | 'permission'
    | 'credentials'
    | 'api-access'
    | 'rate-limit'
    | 'notfound'
    | 'error'
  title: string
  message: string
}

export function porkbunProblem(err: unknown): PorkbunProblem {
  if (err instanceof ConnectError) {
    const msg = err.rawMessage
    switch (err.code) {
      case Code.PermissionDenied:
        return {
          kind: 'permission',
          title: 'The Porkbun API key lacks access for this query',
          message: msg,
        }
      case Code.FailedPrecondition:
        if (msg.toLowerCase().includes('api access is disabled')) {
          return {
            kind: 'api-access',
            title: 'API access is disabled for this domain',
            message: msg,
          }
        }
        return {
          kind: 'credentials',
          title: 'Porkbun rejected the API credentials',
          message: msg,
        }
      case Code.ResourceExhausted:
        return {
          kind: 'rate-limit',
          title: 'Porkbun rate limit reached',
          message: msg,
        }
      case Code.NotFound:
        return { kind: 'notfound', title: 'Not found', message: msg }
    }
    return {
      kind: 'error',
      title: 'Could not read from Porkbun',
      message: msg,
    }
  }
  return {
    kind: 'error',
    title: 'Could not read from Porkbun',
    message: err instanceof Error ? err.message : String(err),
  }
}
