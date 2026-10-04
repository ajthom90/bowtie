import { describe, expect, it } from 'vitest'
import {
  CODE_LENGTH,
  codeFromSearch,
  deviceLabel,
  formatCodeInput,
  isCompleteCode,
  isLinkPath,
  normalizeCode,
} from './linkModel'

describe('normalizeCode', () => {
  it('uppercases and strips everything but letters and digits', () => {
    expect(normalizeCode('bcdf-2345')).toBe('BCDF2345')
    expect(normalizeCode(' BC DF–23 45 ')).toBe('BCDF2345')
  })
  it('caps at eight characters', () => {
    expect(CODE_LENGTH).toBe(8)
    expect(normalizeCode('BCDF2345XYZ')).toBe('BCDF2345')
  })
})

describe('formatCodeInput', () => {
  it('inserts the dash after four characters', () => {
    expect(formatCodeInput('bcd')).toBe('BCD')
    expect(formatCodeInput('bcdf')).toBe('BCDF')
    expect(formatCodeInput('bcdf2')).toBe('BCDF-2')
    expect(formatCodeInput('BCDF2345')).toBe('BCDF-2345')
    expect(formatCodeInput('bcdf-2345-99')).toBe('BCDF-2345')
  })
})

describe('isCompleteCode', () => {
  it('needs eight letters or digits', () => {
    expect(isCompleteCode('BCDF-2345')).toBe(true)
    expect(isCompleteCode('bcdf2345')).toBe(true)
    expect(isCompleteCode('BCDF-234')).toBe(false)
    expect(isCompleteCode('')).toBe(false)
  })
})

describe('codeFromSearch', () => {
  it('reads ?code= and normalizes it', () => {
    expect(codeFromSearch('?code=BCDF2345')).toBe('BCDF2345')
    expect(codeFromSearch('?code=bcdf-2345&x=1')).toBe('BCDF2345')
  })
  it('is empty when absent', () => {
    expect(codeFromSearch('')).toBe('')
    expect(codeFromSearch('?other=1')).toBe('')
  })
})

describe('isLinkPath', () => {
  it('matches /link with or without a trailing slash', () => {
    expect(isLinkPath('/link')).toBe(true)
    expect(isLinkPath('/link/')).toBe(true)
    expect(isLinkPath('/')).toBe(false)
    expect(isLinkPath('/linked')).toBe(false)
  })
})

describe('deviceLabel', () => {
  it('uses the device name or a fallback', () => {
    expect(deviceLabel('Living room Apple TV')).toBe('Living room Apple TV')
    expect(deviceLabel('  ')).toBe('a TV')
  })
})
