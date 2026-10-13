import { describe, expect, it, vi } from 'vitest'
import {
  ALL_TUNERS_BUSY,
  SOME_TUNERS_BUSY,
  busyNote,
  createRecheckController,
  isWatchable,
  watchableOnly,
  watchableRecents,
  withWatchable,
} from './watchableModel'

const ch = (channelId: number, watchable?: boolean) => ({ channelId, watchable })

describe('isWatchable', () => {
  it('treats a missing field (older servers) as watchable', () => {
    expect(isWatchable({})).toBe(true)
    expect(isWatchable({ watchable: true })).toBe(true)
    expect(isWatchable({ watchable: false })).toBe(false)
  })
})

describe('busyNote', () => {
  it('is null when every channel can start', () => {
    expect(busyNote([ch(1), ch(2, true)])).toBeNull()
    expect(busyNote([])).toBeNull()
  })

  it('says tuners are busy when any channel cannot start', () => {
    expect(busyNote([ch(1, true), ch(2, false)])).toBe(SOME_TUNERS_BUSY)
    expect(SOME_TUNERS_BUSY).toBe('All tuners are in use — showing channels you can join.')
  })

  it('says to try later when no channel can start', () => {
    expect(busyNote([ch(1, false), ch(2, false)])).toBe(ALL_TUNERS_BUSY)
    expect(ALL_TUNERS_BUSY).toBe('All tuners are in use. Try again in a few minutes.')
  })
})

describe('watchableOnly', () => {
  it('drops busy channels and keeps order', () => {
    expect(watchableOnly([ch(3), ch(1, false), ch(2, true)]).map((c) => c.channelId)).toEqual([3, 2])
  })
})

describe('watchableRecents', () => {
  it('drops recents whose channel is busy (unknown channels stay)', () => {
    const recents = [{ channelId: 1 }, { channelId: 2 }, { channelId: 9 }]
    expect(watchableRecents(recents, [ch(1, false), ch(2, true)]).map((r) => r.channelId)).toEqual([2, 9])
  })
})

describe('withWatchable', () => {
  it('updates guide rows from the channel list by id', () => {
    const rows = [
      { channelId: 1, watchable: true, name: 'A' },
      { channelId: 2, watchable: false, name: 'B' },
      { channelId: 3, name: 'C' },
    ]
    const out = withWatchable(rows, [
      { id: 1, watchable: false },
      { id: 2, watchable: true },
    ])
    expect(out).toEqual([
      { channelId: 1, watchable: false, name: 'A' },
      { channelId: 2, watchable: true, name: 'B' },
      { channelId: 3, name: 'C' },
    ])
  })

  it('returns the same array when nothing changed (no re-render)', () => {
    const rows = [{ channelId: 1, watchable: true }]
    expect(withWatchable(rows, [{ id: 1, watchable: true }])).toBe(rows)
    expect(withWatchable(rows, [{ id: 1 }])).toBe(rows)
  })
})

describe('createRecheckController', () => {
  function fakeTimers() {
    let next = 1
    const live = new Map<number, () => void>()
    return {
      setIntervalFn: ((fn: () => void) => {
        const id = next++
        live.set(id, fn)
        return id
      }) as unknown as typeof setInterval,
      clearIntervalFn: ((id: number) => {
        live.delete(id)
      }) as unknown as typeof clearInterval,
      tick: () => [...live.values()].forEach((fn) => fn()),
      count: () => live.size,
    }
  }

  it('checks on every interval while visible', () => {
    const t = fakeTimers()
    const check = vi.fn()
    const c = createRecheckController({ check, ...t })
    c.start()
    expect(check).not.toHaveBeenCalled()
    t.tick()
    t.tick()
    expect(check).toHaveBeenCalledTimes(2)
    c.stop()
    expect(t.count()).toBe(0)
  })

  it('pauses while hidden and checks at once when visible again', () => {
    const t = fakeTimers()
    const check = vi.fn()
    const c = createRecheckController({ check, ...t })
    c.start()
    c.handleVisibilityChange('hidden')
    expect(t.count()).toBe(0)
    c.handleVisibilityChange('visible')
    expect(check).toHaveBeenCalledTimes(1)
    expect(t.count()).toBe(1)
  })

  it('ignores visibility changes after stop', () => {
    const t = fakeTimers()
    const check = vi.fn()
    const c = createRecheckController({ check, ...t })
    c.start()
    c.stop()
    c.handleVisibilityChange('visible')
    expect(check).not.toHaveBeenCalled()
    expect(t.count()).toBe(0)
  })
})
