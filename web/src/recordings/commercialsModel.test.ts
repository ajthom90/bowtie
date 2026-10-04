import { describe, expect, it } from 'vitest'
import {
  END_SLOP,
  breakIndexAt,
  createCommercialSkipper,
  isSkipKey,
  loadAutoSkip,
  normalizeBreaks,
  saveAutoSkip,
} from './commercialsModel'

function memStorage(): Storage {
  const m = new Map<string, string>()
  return {
    get length() {
      return m.size
    },
    clear: () => m.clear(),
    getItem: (k) => m.get(k) ?? null,
    key: (i) => [...m.keys()][i] ?? null,
    removeItem: (k) => void m.delete(k),
    setItem: (k, v) => void m.set(k, v),
  }
}

describe('normalizeBreaks', () => {
  it('sorts, merges overlapping and drops invalid breaks', () => {
    expect(
      normalizeBreaks([
        { start: 600, end: 700 },
        { start: 10, end: 60 },
        { start: 50, end: 90 },
        { start: 300, end: 200 },
        { start: Number.NaN, end: 5 },
        { start: -4, end: 3 },
        { start: 690, end: 695 },
      ]),
    ).toEqual([
      { start: 0, end: 3 },
      { start: 10, end: 90 },
      { start: 600, end: 700 },
    ])
  })

  it('treats missing input as no breaks and does not mutate the input', () => {
    expect(normalizeBreaks(undefined)).toEqual([])
    expect(normalizeBreaks(null)).toEqual([])
    const input = [
      { start: 20, end: 40 },
      { start: 10, end: 30 },
    ]
    normalizeBreaks(input)
    expect(input).toEqual([
      { start: 20, end: 40 },
      { start: 10, end: 30 },
    ])
  })
})

describe('breakIndexAt', () => {
  const breaks = normalizeBreaks([
    { start: 100, end: 200 },
    { start: 500, end: 620 },
  ])

  it('finds the break the playhead is in (half-open)', () => {
    expect(breakIndexAt(breaks, 0)).toBe(-1)
    expect(breakIndexAt(breaks, 99.99)).toBe(-1)
    expect(breakIndexAt(breaks, 100)).toBe(0) // exactly at the start: inside
    expect(breakIndexAt(breaks, 150)).toBe(0)
    expect(breakIndexAt(breaks, 200)).toBe(-1) // exactly at the end: past it
    expect(breakIndexAt(breaks, 300)).toBe(-1)
    expect(breakIndexAt(breaks, 500)).toBe(1)
    expect(breakIndexAt(breaks, 700)).toBe(-1)
  })

  it('a seek that lands just short of the end is past the break', () => {
    expect(breakIndexAt(breaks, 200 - END_SLOP / 2)).toBe(-1)
    expect(breakIndexAt(breaks, 200 - END_SLOP - 0.01)).toBe(0)
  })

  it('handles no breaks and non-finite times', () => {
    expect(breakIndexAt([], 10)).toBe(-1)
    expect(breakIndexAt(breaks, Number.NaN)).toBe(-1)
  })
})

describe('createCommercialSkipper', () => {
  const input = [
    { start: 500, end: 620 },
    { start: 100, end: 200 },
  ]

  it('without auto-skip only reports the break', () => {
    const s = createCommercialSkipper(input)
    expect(s.update(50, false)).toEqual({ current: null, seekTo: null })
    expect(s.update(150, false)).toEqual({ current: { start: 100, end: 200 }, seekTo: null })
  })

  it('auto-skips each break once; seeking back in does not skip again', () => {
    const s = createCommercialSkipper(input)
    expect(s.update(100, true).seekTo).toBe(200)
    expect(s.update(200, true)).toEqual({ current: null, seekTo: null })
    // The viewer seeks back into the break they skipped: stay, show the button.
    expect(s.update(120, true)).toEqual({ current: { start: 100, end: 200 }, seekTo: null })
    // The next break still auto-skips.
    expect(s.update(510, true).seekTo).toBe(620)
  })

  it('a manual skip counts as skipped for auto-skip', () => {
    const s = createCommercialSkipper(input)
    expect(s.skip(150)).toBe(200)
    expect(s.update(160, true).seekTo).toBeNull()
  })

  it('skip outside a break does nothing', () => {
    const s = createCommercialSkipper(input)
    expect(s.skip(300)).toBeNull()
    expect(s.skip(200)).toBeNull()
  })

  it('turning auto-skip on mid-break skips that break', () => {
    const s = createCommercialSkipper(input)
    expect(s.update(110, false).seekTo).toBeNull()
    expect(s.update(115, true).seekTo).toBe(200)
  })

  it('exposes the normalized breaks', () => {
    expect(createCommercialSkipper(input).breaks.map((b) => b.start)).toEqual([100, 500])
    expect(createCommercialSkipper(undefined).breaks).toEqual([])
  })
})

describe('auto-skip preference', () => {
  it('defaults off and round-trips', () => {
    const st = memStorage()
    expect(loadAutoSkip(st)).toBe(false)
    saveAutoSkip(true, st)
    expect(loadAutoSkip(st)).toBe(true)
    saveAutoSkip(false, st)
    expect(loadAutoSkip(st)).toBe(false)
  })

  it('survives storage that throws or is missing', () => {
    const broken = memStorage()
    broken.getItem = () => {
      throw new Error('blocked')
    }
    broken.setItem = () => {
      throw new Error('blocked')
    }
    expect(loadAutoSkip(broken)).toBe(false)
    expect(() => saveAutoSkip(true, broken)).not.toThrow()
    expect(loadAutoSkip(undefined)).toBe(false)
  })
})

describe('isSkipKey', () => {
  const div = { tagName: 'DIV' } as unknown as EventTarget
  it('accepts s / S without modifiers', () => {
    expect(isSkipKey({ key: 's', target: div })).toBe(true)
    expect(isSkipKey({ key: 'S', target: div })).toBe(true)
    expect(isSkipKey({ key: 'd', target: div })).toBe(false)
    expect(isSkipKey({ key: 's', metaKey: true, target: div })).toBe(false)
    expect(isSkipKey({ key: 's', ctrlKey: true, target: div })).toBe(false)
  })

  it('ignores typing in a field', () => {
    for (const tagName of ['INPUT', 'TEXTAREA', 'SELECT']) {
      expect(isSkipKey({ key: 's', target: { tagName } as unknown as EventTarget })).toBe(false)
    }
    expect(
      isSkipKey({ key: 's', target: { tagName: 'DIV', isContentEditable: true } as unknown as EventTarget }),
    ).toBe(false)
  })
})
