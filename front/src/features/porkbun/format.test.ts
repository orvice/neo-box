import { timestampFromDate } from '@bufbuild/protobuf/wkt'
import { Code, ConnectError } from '@connectrpc/connect'
import { describe, expect, it } from 'vitest'
import {
  expiryStatus,
  formatDnssecAlgorithm,
  formatDnssecDigestType,
  formatTTL,
  isSandboxKey,
  maskApiKey,
  porkbunProblem,
} from './format'

describe('porkbun format helpers', () => {
  it('detects sandbox keys and masks long keys', () => {
    expect(isSandboxKey('pk1_sb_123456')).toBe(true)
    expect(isSandboxKey('pk1_live_123456')).toBe(false)
    expect(maskApiKey('pk1_1234567890abcdef123456')).toBe('pk1_123456…123456')
    expect(maskApiKey('pk1_short')).toBe('pk1_short')
  })

  it('classifies domain expiry states', () => {
    const now = new Date('2026-06-01T00:00:00Z')
    expect(
      expiryStatus(timestampFromDate(new Date('2026-05-25T00:00:00Z')), now)
    ).toMatchObject({
      dateText: '2026-05-25',
      tone: 'expired',
      label: 'Expired 7d ago',
    })
    expect(
      expiryStatus(timestampFromDate(new Date('2026-06-15T00:00:00Z')), now)
    ).toMatchObject({
      dateText: '2026-06-15',
      tone: 'soon',
      label: '14d left',
    })
    expect(
      expiryStatus(timestampFromDate(new Date('2027-06-01T00:00:00Z')), now)
    ).toMatchObject({
      dateText: '2027-06-01',
      tone: 'ok',
    })
    expect(expiryStatus(undefined, now).tone).toBe('none')
  })

  it('formats TTL and DNSSEC codes', () => {
    expect(formatTTL(600)).toBe('10m')
    expect(formatTTL(3600)).toBe('1h')
    expect(formatTTL(86400)).toBe('1d')
    expect(formatTTL(45)).toBe('45s')
    expect(formatDnssecAlgorithm('13')).toBe('13 (ECDSAP256SHA256)')
    expect(formatDnssecDigestType('2')).toBe('2 (SHA-256)')
  })

  it('maps Connect errors to porkbun problem kinds', () => {
    expect(
      porkbunProblem(
        new ConnectError(
          'API access is disabled for example.com',
          Code.FailedPrecondition
        )
      ).kind
    ).toBe('api-access')
    expect(
      porkbunProblem(
        new ConnectError('Invalid API keys', Code.FailedPrecondition)
      ).kind
    ).toBe('credentials')
    expect(
      porkbunProblem(new ConnectError('Missing scope', Code.PermissionDenied))
        .kind
    ).toBe('permission')
    expect(
      porkbunProblem(new ConnectError('Rate limited', Code.ResourceExhausted))
        .kind
    ).toBe('rate-limit')
  })
})
