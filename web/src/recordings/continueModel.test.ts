import { describe, expect, it } from 'vitest'
import type { Recording } from '../api/client'
import {
  CONTINUE_HIDDEN_KEY,
  CONTINUE_MAX,
  formatTimeLeft,
  guideRowVisible,
  hiddenStamp,
  isContinuable,
  loadHiddenAt,
  progressPct,
  removeLabel,
  resumeLabel,
  saveHiddenAt,
  secondsLeft,
  selectContinueWatching,
  withPositionReset,
} from './continueModel'

let nextId = 1
function rec(over: Partial<Recording> = {}): Recording {
  return {
    id: nextId++,
    title: 'Jeopardy!',
    subtitle: '',
    description: '',
    category: '',
    channelId: 3,
    channelName: '5.1 KSTP',
    start: '2026-10-03T00:00:00Z',
    stop: '2026-10-03T01:00:00Z',
    state: 'ready',
    partial: false,
    failure: '',
    failureDetail: '',
    durationSec: 3600,
    sizeBytes: 1,
    protected: false,
    positionSec: 600,
    positionUpdatedAt: '2026-10-03T12:00:00Z',
    scheduledBy: 'andrew',
    canManage: true,
    ...over,
  }
}

describe('isContinuable', () => {
  it('needs at least a minute watched (59 s is not enough, 60 s is)', () => {
    expect(isContinuable(rec({ positionSec: 59 }))).toBe(false)
    expect(isContinuable(rec({ positionSec: 60 }))).toBe(true)
    expect(isContinuable(rec({ positionSec: 0 }))).toBe(false)
  })

  it('drops recordings within two minutes of the end (exactly duration − 120 is out)', () => {
    expect(isContinuable(rec({ durationSec: 3600, positionSec: 3479 }))).toBe(true)
    expect(isContinuable(rec({ durationSec: 3600, positionSec: 3480 }))).toBe(false)
    expect(isContinuable(rec({ durationSec: 3600, positionSec: 3600 }))).toBe(false)
  })

  it('only ready recordings', () => {
    for (const state of ['scheduled', 'waiting', 'recording', 'converting', 'failed'] as const) {
      expect(isContinuable(rec({ state }))).toBe(false)
    }
  })

  it('excludes parental-locked recordings', () => {
    expect(isContinuable(rec({ locked: true }))).toBe(false)
    expect(isContinuable(rec({ locked: false }))).toBe(true)
  })

  it('needs a known duration', () => {
    expect(isContinuable(rec({ durationSec: 0, positionSec: 600 }))).toBe(false)
    expect(isContinuable(rec({ durationSec: 150, positionSec: 60 }))).toBe(false)
  })
})

describe('selectContinueWatching', () => {
  it('sorts by positionUpdatedAt, newest first, with missing timestamps last', () => {
    const old = rec({ title: 'old', positionUpdatedAt: '2026-10-01T00:00:00Z' })
    const none = rec({ title: 'none', positionUpdatedAt: undefined })
    const fresh = rec({ title: 'fresh', positionUpdatedAt: '2026-10-03T20:00:00Z' })
    const bad = rec({ title: 'bad', positionUpdatedAt: 'not a date' })
    const mid = rec({ title: 'mid', positionUpdatedAt: '2026-10-02T09:30:00+02:00' })
    expect(selectContinueWatching([old, none, fresh, bad, mid]).map((r) => r.title)).toEqual([
      'fresh',
      'mid',
      'old',
      'none',
      'bad',
    ])
  })

  it('keeps list order for ties', () => {
    const a = rec({ title: 'a' })
    const b = rec({ title: 'b' })
    expect(selectContinueWatching([a, b]).map((r) => r.title)).toEqual(['a', 'b'])
  })

  it('filters with the selection rules', () => {
    const rows = [
      rec({ title: 'keep' }),
      rec({ title: 'barely started', positionSec: 59 }),
      rec({ title: 'finished', positionSec: 3500 }),
      rec({ title: 'locked', locked: true }),
      rec({ title: 'converting', state: 'converting' }),
    ]
    expect(selectContinueWatching(rows).map((r) => r.title)).toEqual(['keep'])
  })

  it('shows at most 10', () => {
    const rows = Array.from({ length: 14 }, (_, i) =>
      rec({ title: `r${i}`, positionUpdatedAt: new Date(Date.UTC(2026, 9, 1, i)).toISOString() }),
    )
    const got = selectContinueWatching(rows)
    expect(CONTINUE_MAX).toBe(10)
    expect(got).toHaveLength(10)
    expect(got[0].title).toBe('r13')
    expect(got[9].title).toBe('r4')
  })

  it('is empty when nothing qualifies', () => {
    expect(selectContinueWatching([])).toEqual([])
    expect(selectContinueWatching([rec({ positionSec: 0 })])).toEqual([])
  })
})

describe('formatTimeLeft', () => {
  it('under a minute', () => {
    expect(formatTimeLeft(0)).toBe('less than a minute left')
    expect(formatTimeLeft(59)).toBe('less than a minute left')
    expect(formatTimeLeft(-5)).toBe('less than a minute left')
    expect(formatTimeLeft(Number.NaN)).toBe('less than a minute left')
  })

  it('minutes (whole minutes, rounded down)', () => {
    expect(formatTimeLeft(60)).toBe('1 min left')
    expect(formatTimeLeft(37 * 60 + 59)).toBe('37 min left')
    expect(formatTimeLeft(59 * 60 + 59)).toBe('59 min left')
  })

  it('hours and minutes', () => {
    expect(formatTimeLeft(3600)).toBe('1 hr left')
    expect(formatTimeLeft(65 * 60)).toBe('1 hr 5 min left')
    expect(formatTimeLeft(2 * 3600 + 30)).toBe('2 hr left')
    expect(formatTimeLeft(2 * 3600 + 59 * 60)).toBe('2 hr 59 min left')
  })

  it('from a recording', () => {
    expect(secondsLeft(rec({ durationSec: 3600, positionSec: 1380 }))).toBe(2220)
    expect(formatTimeLeft(secondsLeft(rec({ durationSec: 3600, positionSec: 1380 })))).toBe('37 min left')
    expect(secondsLeft(rec({ durationSec: 100, positionSec: 200 }))).toBe(0)
  })
})

describe('progressPct', () => {
  it('is position / duration, clamped', () => {
    expect(progressPct(rec({ durationSec: 3600, positionSec: 900 }))).toBe(25)
    expect(progressPct(rec({ durationSec: 3600, positionSec: 9000 }))).toBe(100)
    expect(progressPct(rec({ durationSec: 0, positionSec: 900 }))).toBe(0)
  })
})

describe('labels', () => {
  it('names the program (with episode) and the time left', () => {
    expect(resumeLabel(rec({ title: 'Nova', subtitle: 'Ice', durationSec: 3600, positionSec: 1380 }))).toBe(
      'Resume Nova: Ice, 37 min left',
    )
    expect(removeLabel(rec({ title: 'Nova', subtitle: '' }))).toBe('Remove Nova from Continue watching')
  })
})

describe('withPositionReset', () => {
  it('sets the position to 0 so the item drops out', () => {
    const r = withPositionReset(rec(), new Date('2026-10-04T00:00:00Z'))
    expect(r.positionSec).toBe(0)
    expect(r.positionUpdatedAt).toBe('2026-10-04T00:00:00.000Z')
    expect(isContinuable(r)).toBe(false)
  })
})

describe('guide row hiding', () => {
  const a = rec({ positionUpdatedAt: '2026-10-03T10:00:00Z' })
  const b = rec({ positionUpdatedAt: '2026-10-03T12:00:00Z' })

  it('is shown when there are items and it was never hidden', () => {
    expect(guideRowVisible([a, b], null)).toBe(true)
    expect(guideRowVisible([], null)).toBe(false)
  })

  it('stays hidden until something is watched after hiding', () => {
    const stamp = hiddenStamp([a, b])
    expect(stamp).toBe('2026-10-03T12:00:00.000Z')
    expect(guideRowVisible([a, b], stamp)).toBe(false)
    const later = rec({ positionUpdatedAt: '2026-10-03T12:00:01Z' })
    expect(guideRowVisible([later, a, b], stamp)).toBe(true)
  })

  it('without timestamps (older server) hiding sticks', () => {
    const plain = rec({ positionUpdatedAt: undefined })
    const stamp = hiddenStamp([plain])
    expect(guideRowVisible([plain], stamp)).toBe(false)
    expect(guideRowVisible([rec({ positionUpdatedAt: '2026-10-03T00:00:00Z' })], stamp)).toBe(true)
  })

  it('ignores a garbled stored value', () => {
    expect(guideRowVisible([a], 'garbage')).toBe(true)
  })

  it('remembers the stamp per browser (and survives blocked storage)', () => {
    const m = new Map<string, string>()
    const kv = { getItem: (k: string) => m.get(k) ?? null, setItem: (k: string, v: string) => void m.set(k, v) }
    expect(loadHiddenAt(kv)).toBeNull()
    saveHiddenAt('2026-10-03T12:00:00.000Z', kv)
    expect(m.get(CONTINUE_HIDDEN_KEY)).toBe('2026-10-03T12:00:00.000Z')
    expect(loadHiddenAt(kv)).toBe('2026-10-03T12:00:00.000Z')
    const broken = {
      getItem: () => {
        throw new Error('blocked')
      },
      setItem: () => {
        throw new Error('blocked')
      },
    }
    expect(loadHiddenAt(broken)).toBeNull()
    expect(() => saveHiddenAt('x', broken)).not.toThrow()
    expect(loadHiddenAt(null)).toBeNull()
  })
})
