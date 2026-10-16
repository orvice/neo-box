import { describe, expect, it } from 'vitest'
import {
  formatPercent,
  orgUnitLabel,
  quotaText,
  storageLevel,
} from './storage-format'

describe('storageLevel', () => {
  it('compares with the threshold and treats unknown as no level', () => {
    expect(storageLevel(undefined, 80)).toBe('none')
    expect(storageLevel(0, 80)).toBe('ok')
    expect(storageLevel(79.9, 80)).toBe('ok')
    expect(storageLevel(80, 80)).toBe('near')
    expect(storageLevel(99.9, 80)).toBe('near')
    expect(storageLevel(100, 80)).toBe('over')
    expect(storageLevel(250, 95)).toBe('over')
  })
})

describe('formatPercent', () => {
  it('keeps unknown apart from zero', () => {
    expect(formatPercent(undefined)).toBe('—')
    expect(formatPercent(0)).toBe('0.0%')
    expect(formatPercent(4.26)).toBe('4.3%')
    expect(formatPercent(87.4)).toBe('87%')
    expect(formatPercent(200)).toBe('200%')
  })
})

describe('orgUnitLabel', () => {
  it('names the top level and the unknown', () => {
    expect(orgUnitLabel('')).toBe('Unknown unit')
    expect(orgUnitLabel('/')).toBe('/ (top level)')
    expect(orgUnitLabel('/Sales/EU')).toBe('/Sales/EU')
  })
})

describe('quotaText', () => {
  it('says mixed or not reported instead of a number', () => {
    const f = (mb: bigint) => `${mb} MB`
    expect(quotaText(1000n, false, f)).toBe('1000 MB')
    expect(quotaText(undefined, false, f)).toBe('Not reported')
    expect(quotaText(undefined, true, f)).toBe('Mixed')
    expect(quotaText(1000n, true, f)).toBe('Mixed')
  })
})
