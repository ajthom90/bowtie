import { describe, expect, it } from 'vitest'
import {
  formatGuideTime,
  halfHourTicks,
  layoutRow,
  nowLinePct,
  type GuideProgram,
} from './guideModel'

/** Fixed UTC helpers so layout math is timezone-stable. */
function utc(iso: string): Date {
  return new Date(iso)
}

function prog(start: string, stop: string, title = 'Show'): GuideProgram {
  return {
    start,
    stop,
    title,
    subtitle: '',
    description: '',
    category: '',
  }
}

describe('layoutRow', () => {
  const windowStart = utc('2026-08-04T12:00:00.000Z')
  const windowStop = utc('2026-08-04T16:00:00.000Z') // 4h

  it('returns a single full-window gap when there are no programs', () => {
    const cells = layoutRow([], windowStart, windowStop)
    expect(cells).toHaveLength(1)
    expect(cells[0]).toMatchObject({
      kind: 'gap',
      leftPct: 0,
      widthPct: 100,
    })
  })

  it('clips a program that starts before the window', () => {
    const cells = layoutRow(
      [prog('2026-08-04T11:00:00.000Z', '2026-08-04T13:00:00.000Z', 'Early')],
      windowStart,
      windowStop,
    )
    // 11:00–13:00 clipped to 12:00–13:00 = 1h of 4h = 25%, then trailing gap 75%
    expect(cells).toHaveLength(2)
    expect(cells[0]).toMatchObject({
      kind: 'program',
      leftPct: 0,
      widthPct: 25,
    })
    if (cells[0].kind === 'program') {
      expect(cells[0].start.toISOString()).toBe('2026-08-04T12:00:00.000Z')
      expect(cells[0].stop.toISOString()).toBe('2026-08-04T13:00:00.000Z')
      expect(cells[0].program.title).toBe('Early')
    }
    expect(cells[1]).toMatchObject({ kind: 'gap', leftPct: 25, widthPct: 75 })
  })

  it('clips a program that ends after the window', () => {
    const cells = layoutRow(
      [prog('2026-08-04T15:00:00.000Z', '2026-08-04T18:00:00.000Z', 'Late')],
      windowStart,
      windowStop,
    )
    // leading gap 12:00–15:00 = 75%, program 15:00–16:00 = 25%
    expect(cells).toHaveLength(2)
    expect(cells[0]).toMatchObject({ kind: 'gap', leftPct: 0, widthPct: 75 })
    expect(cells[1]).toMatchObject({
      kind: 'program',
      leftPct: 75,
      widthPct: 25,
    })
    if (cells[1].kind === 'program') {
      expect(cells[1].stop.toISOString()).toBe('2026-08-04T16:00:00.000Z')
    }
  })

  it('inserts gap cells for holes between programs', () => {
    const cells = layoutRow(
      [
        prog('2026-08-04T12:00:00.000Z', '2026-08-04T13:00:00.000Z', 'A'),
        // hole 13:00–14:00
        prog('2026-08-04T14:00:00.000Z', '2026-08-04T15:00:00.000Z', 'B'),
      ],
      windowStart,
      windowStop,
    )
    // A 25%, gap 25%, B 25%, trailing gap 25%
    expect(cells.map((c) => c.kind)).toEqual(['program', 'gap', 'program', 'gap'])
    expect(cells[0]).toMatchObject({ leftPct: 0, widthPct: 25 })
    expect(cells[1]).toMatchObject({ leftPct: 25, widthPct: 25 })
    expect(cells[2]).toMatchObject({ leftPct: 50, widthPct: 25 })
    expect(cells[3]).toMatchObject({ leftPct: 75, widthPct: 25 })
  })

  it('computes percent offsets for a program fully inside the window', () => {
    // 13:00–14:30 within 12:00–16:00 → left 25%, width 37.5%
    const cells = layoutRow(
      [prog('2026-08-04T13:00:00.000Z', '2026-08-04T14:30:00.000Z', 'Mid')],
      windowStart,
      windowStop,
    )
    expect(cells).toHaveLength(3)
    expect(cells[0]).toMatchObject({ kind: 'gap', leftPct: 0, widthPct: 25 })
    expect(cells[1]).toMatchObject({
      kind: 'program',
      leftPct: 25,
      widthPct: 37.5,
    })
    expect(cells[2]).toMatchObject({ kind: 'gap', leftPct: 62.5, widthPct: 37.5 })
  })

  it('skips programs completely outside the window', () => {
    const cells = layoutRow(
      [
        prog('2026-08-04T08:00:00.000Z', '2026-08-04T09:00:00.000Z', 'Before'),
        prog('2026-08-04T18:00:00.000Z', '2026-08-04T19:00:00.000Z', 'After'),
      ],
      windowStart,
      windowStop,
    )
    expect(cells).toHaveLength(1)
    expect(cells[0].kind).toBe('gap')
    expect(cells[0].widthPct).toBe(100)
  })

  it('returns empty array for a zero-duration window', () => {
    expect(layoutRow([prog('2026-08-04T12:00:00.000Z', '2026-08-04T13:00:00.000Z')], windowStart, windowStart)).toEqual(
      [],
    )
  })

  it('sums cell widths to 100% when programs cover the window', () => {
    const cells = layoutRow(
      [
        prog('2026-08-04T12:00:00.000Z', '2026-08-04T14:00:00.000Z', 'A'),
        prog('2026-08-04T14:00:00.000Z', '2026-08-04T16:00:00.000Z', 'B'),
      ],
      windowStart,
      windowStop,
    )
    const sum = cells.reduce((s, c) => s + c.widthPct, 0)
    expect(sum).toBeCloseTo(100, 5)
    expect(cells.every((c) => c.kind === 'program')).toBe(true)
  })
})

describe('nowLinePct', () => {
  const start = utc('2026-08-04T12:00:00.000Z')
  const stop = utc('2026-08-04T16:00:00.000Z')

  it('returns 0 at window start and 50 at midpoint', () => {
    expect(nowLinePct(start, start, stop)).toBe(0)
    expect(nowLinePct(utc('2026-08-04T14:00:00.000Z'), start, stop)).toBe(50)
  })

  it('returns null when now is outside the window', () => {
    expect(nowLinePct(utc('2026-08-04T11:00:00.000Z'), start, stop)).toBeNull()
    expect(nowLinePct(utc('2026-08-04T17:00:00.000Z'), start, stop)).toBeNull()
  })
})

describe('halfHourTicks', () => {
  it('emits 30-minute boundaries inside the window', () => {
    const start = utc('2026-08-04T12:00:00.000Z')
    const stop = utc('2026-08-04T14:00:00.000Z')
    const ticks = halfHourTicks(start, stop)
    expect(ticks.map((t) => t.toISOString())).toEqual([
      '2026-08-04T12:00:00.000Z',
      '2026-08-04T12:30:00.000Z',
      '2026-08-04T13:00:00.000Z',
      '2026-08-04T13:30:00.000Z',
    ])
  })
})

describe('formatGuideTime', () => {
  it('formats local HH:MM with zero padding', () => {
    // Use a date whose local components we control via constructor
    const d = new Date(2026, 7, 4, 9, 5, 0) // Aug 4 2026 09:05 local
    expect(formatGuideTime(d)).toBe('09:05')
  })
})
