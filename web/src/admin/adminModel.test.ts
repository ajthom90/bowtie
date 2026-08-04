import { describe, expect, it } from 'vitest'
import type { AdminChannel } from '../api/client'
import {
  compareGuideNumbers,
  filterAndSortChannels,
  formatTimestamp,
  formatUptime,
  guideNumberParts,
  isTunerIdle,
  isZeroTime,
  qualityLabel,
  signalPercent,
} from './adminModel'

function ch(
  guideNumber: string,
  name = 'Ch',
  id = 1,
): AdminChannel {
  return {
    id,
    deviceId: 'dev1',
    guideNumber,
    name,
    enabled: true,
    epgChannelId: '',
  }
}

describe('compareGuideNumbers', () => {
  it('sorts numeric guide numbers so 10.1 comes after 5.1', () => {
    const nums = ['10.1', '2.1', '5.1', '10.2', '5']
    const sorted = nums.slice().sort(compareGuideNumbers)
    expect(sorted).toEqual(['2.1', '5', '5.1', '10.1', '10.2'])
  })

  it('handles multi-segment and plain integers', () => {
    expect(compareGuideNumbers('4', '4.1')).toBeLessThan(0)
    expect(compareGuideNumbers('4.1', '4.1')).toBe(0)
    expect(compareGuideNumbers('11', '2')).toBeGreaterThan(0)
  })
})

describe('guideNumberParts', () => {
  it('splits on dots', () => {
    expect(guideNumberParts('10.1')).toEqual([10, 1])
    expect(guideNumberParts(' 5 ')).toEqual([5])
  })
})

describe('filterAndSortChannels', () => {
  const list = [
    ch('10.1', 'KABC', 1),
    ch('5.1', 'KTLA', 2),
    ch('2.1', 'KCBS', 3),
    ch('5.2', 'KTLA-2', 4),
  ]

  it('sorts by guide number numerically', () => {
    const out = filterAndSortChannels(list, '')
    expect(out.map((c) => c.guideNumber)).toEqual(['2.1', '5.1', '5.2', '10.1'])
  })

  it('filters by name case-insensitively', () => {
    const out = filterAndSortChannels(list, 'ktla')
    expect(out.map((c) => c.guideNumber)).toEqual(['5.1', '5.2'])
  })

  it('filters by guide number substring', () => {
    const out = filterAndSortChannels(list, '10')
    expect(out.map((c) => c.guideNumber)).toEqual(['10.1'])
  })

  it('returns empty when nothing matches', () => {
    expect(filterAndSortChannels(list, 'zzz')).toEqual([])
  })
})

describe('isZeroTime / formatTimestamp', () => {
  it('treats empty and go-zero as never', () => {
    expect(isZeroTime('')).toBe(true)
    expect(isZeroTime('0001-01-01T00:00:00Z')).toBe(true)
    expect(isZeroTime(null)).toBe(true)
    expect(formatTimestamp('')).toBe('never')
  })

  it('formats a real timestamp', () => {
    // Use local constructor so format is stable regardless of TZ offset math.
    const d = new Date(2026, 7, 4, 14, 5, 9)
    const formatted = formatTimestamp(d.toISOString())
    expect(formatted).toMatch(/2026-08-04 \d{2}:\d{2}:\d{2}/)
  })
})

describe('formatUptime', () => {
  it('formats seconds, minutes, hours', () => {
    const now = new Date('2026-08-04T12:00:00.000Z')
    expect(formatUptime('2026-08-04T11:59:50.000Z', now)).toBe('10s')
    expect(formatUptime('2026-08-04T11:58:00.000Z', now)).toBe('2m 00s')
    expect(formatUptime('2026-08-04T10:00:00.000Z', now)).toBe('2h 00m 00s')
  })
})

describe('signalPercent / isTunerIdle', () => {
  it('clamps signal percent', () => {
    expect(signalPercent(undefined)).toBe(0)
    expect(signalPercent(150)).toBe(100)
    expect(signalPercent(-5)).toBe(0)
    expect(signalPercent(72.4)).toBe(72)
  })

  it('detects idle tuners', () => {
    expect(isTunerIdle({})).toBe(true)
    expect(isTunerIdle({ signalStrengthPercent: 0 })).toBe(true)
    expect(isTunerIdle({ vctNumber: '5.1' })).toBe(false)
    expect(isTunerIdle({ vctName: 'KTLA', signalStrengthPercent: 80 })).toBe(false)
  })
})

describe('qualityLabel', () => {
  it('maps known values', () => {
    expect(qualityLabel('')).toBe('Unlimited')
    expect(qualityLabel('high')).toBe('High')
  })
})
