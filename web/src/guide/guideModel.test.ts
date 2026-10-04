import { describe, expect, it } from 'vitest'
import {
  compareGuideNumber,
  formatGuideTime,
  sortFavoritesFirst,
  supportsFavorites,
  withFavorite,
  GUIDE_COPY,
  halfHourTicks,
  layoutRow,
  nowLinePct,
  selectGuidePageState,
  type GuideProgram,
  receptionNote,
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

describe('selectGuidePageState', () => {
  it('returns loading when channels are null and loading', () => {
    expect(
      selectGuidePageState({
        channels: null,
        loading: true,
        error: null,
        role: 'viewer',
      }),
    ).toEqual({ kind: 'loading' })
  })

  it('returns error when load failed (channels null)', () => {
    expect(
      selectGuidePageState({
        channels: null,
        loading: false,
        error: 'Failed to load guide',
        role: 'admin',
      }),
    ).toEqual({ kind: 'error', message: 'Failed to load guide' })
  })

  it('returns admin empty copy + admin link when channels is []', () => {
    const state = selectGuidePageState({
      channels: [],
      loading: false,
      error: null,
      role: 'admin',
    })
    expect(state).toEqual({
      kind: 'empty',
      copy: GUIDE_COPY.noChannelsAdmin,
      role: 'admin',
      showAdminLink: true,
    })
    expect(state.kind === 'empty' && state.copy).toBe(
      'No channels enabled yet. Add your HDHomeRun and enable channels in Admin → Channels.',
    )
  })

  it('returns viewer empty copy without admin link when channels is []', () => {
    const state = selectGuidePageState({
      channels: [],
      loading: false,
      error: null,
      role: 'viewer',
    })
    expect(state).toEqual({
      kind: 'empty',
      copy: GUIDE_COPY.noChannelsViewer,
      role: 'viewer',
      showAdminLink: false,
    })
    expect(state.kind === 'empty' && state.copy).toBe(
      'No channels enabled yet. Ask your admin to enable some channels.',
    )
  })

  it('returns ready when channels are non-empty (program-less is per-row)', () => {
    expect(
      selectGuidePageState({
        channels: [{ channelId: 1 }],
        loading: false,
        error: null,
        role: 'viewer',
      }),
    ).toEqual({ kind: 'ready' })
  })

  it('exposes program-less cell copy constant', () => {
    expect(GUIDE_COPY.noGuideData).toBe('No guide data — press to watch')
  })
})

describe('receptionNote', () => {
  it('labels channels the antenna could not receive on the last tune', () => {
    expect(receptionNote('noSignal')).toBe('No signal')
  })
  it('says nothing for received or never-tuned channels', () => {
    expect(receptionNote('ok')).toBeNull()
    expect(receptionNote('unknown')).toBeNull()
    expect(receptionNote(undefined)).toBeNull()
  })
})

type FavCh = { channelId: number; guideNumber: string; favorite?: boolean }

function ch(channelId: number, guideNumber: string, favorite?: boolean): FavCh {
  return favorite === undefined ? { channelId, guideNumber } : { channelId, guideNumber, favorite }
}

const ids = (list: FavCh[]) => list.map((c) => c.channelId)

describe('compareGuideNumber', () => {
  it('orders major then minor numerically, not as strings', () => {
    expect(compareGuideNumber('9.1', '11.1')).toBeLessThan(0)
    expect(compareGuideNumber('5.10', '5.2')).toBeGreaterThan(0)
    expect(compareGuideNumber('5', '5.1')).toBeLessThan(0)
    expect(compareGuideNumber('4.1', '4.1')).toBe(0)
  })
})

describe('sortFavoritesFirst', () => {
  it('puts favorites first in guide-number order, then the rest in server order', () => {
    const input = [
      ch(1, '11.1', true),
      ch(2, '2.1', false),
      ch(3, '9.1', true),
      ch(4, '13.1', false),
      ch(5, '4.1', false),
    ]
    expect(ids(sortFavoritesFirst(input))).toEqual([3, 1, 2, 4, 5])
  })

  it('keeps server order when nothing is starred', () => {
    const input = [ch(1, '11.1', false), ch(2, '2.1', false)]
    expect(ids(sortFavoritesFirst(input))).toEqual([1, 2])
  })

  it('sorts by guide number when everything is starred', () => {
    const input = [ch(1, '11.1', true), ch(2, '2.1', true), ch(3, '2.10', true), ch(4, '2.2', true)]
    expect(ids(sortFavoritesFirst(input))).toEqual([2, 4, 3, 1])
  })

  it('leaves an older server (no favorite field) untouched', () => {
    const input = [ch(1, '11.1'), ch(2, '2.1')]
    expect(ids(sortFavoritesFirst(input))).toEqual([1, 2])
  })

  it('handles an empty list and does not mutate its input', () => {
    expect(sortFavoritesFirst([])).toEqual([])
    const input = [ch(1, '11.1', false), ch(2, '9.1', true)]
    const out = sortFavoritesFirst(input)
    expect(ids(out)).toEqual([2, 1])
    expect(ids(input)).toEqual([1, 2])
  })
})

describe('supportsFavorites', () => {
  it('is true when the server sends the favorite field', () => {
    expect(supportsFavorites([ch(1, '2.1', false)])).toBe(true)
    expect(supportsFavorites([ch(1, '2.1', true)])).toBe(true)
  })
  it('is false for an older server or an empty guide', () => {
    expect(supportsFavorites([ch(1, '2.1')])).toBe(false)
    expect(supportsFavorites([])).toBe(false)
  })
})

describe('withFavorite', () => {
  it('sets favorite on the one channel and returns a new array', () => {
    const input = [ch(1, '2.1', false), ch(2, '4.1', false)]
    const out = withFavorite(input, 2, true)
    expect(out).not.toBe(input)
    expect(out.map((c) => c.favorite)).toEqual([false, true])
    expect(input[1].favorite).toBe(false)
  })
  it('clears favorite and leaves unknown ids alone', () => {
    const input = [ch(1, '2.1', true)]
    expect(withFavorite(input, 1, false)[0].favorite).toBe(false)
    expect(withFavorite(input, 99, false)[0].favorite).toBe(true)
  })
})
