import { describe, expect, it } from 'vitest'
import { lastLoginText, triState } from './observability-format'

describe('security display preserves unknown data', () => {
  it('distinguishes missing and false two-step values', () => {
    expect(triState(undefined)).toBe('Unknown')
    expect(triState(false)).toBe('No')
    expect(triState(true)).toBe('Yes')
  })
  it('never equates a missing or malformed login date with an unused account', () => {
    expect(lastLoginText('')).toBe('Unknown')
    expect(lastLoginText('invalid')).toBe('Unknown')
    expect(lastLoginText('1970-01-01T00:00:00.000Z')).toBe('Never logged in')
  })
})
