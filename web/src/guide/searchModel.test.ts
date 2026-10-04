import { afterEach, describe, expect, it, vi } from 'vitest'
import type { GuideSearchResult } from '../api/client'
import {
  SEARCH_DEBOUNCE_MS,
  createDebouncer,
  isOnNow,
  lockText,
  normalizeQuery,
  resultActions,
  searchStatusText,
} from './searchModel'

function result(over: Partial<GuideSearchResult> = {}): GuideSearchResult {
  return {
    channelId: 3,
    guideNumber: '9.1',
    channelName: 'KMSP',
    logoUrl: '',
    start: '2026-10-04T19:00:00Z',
    stop: '2026-10-04T19:30:00Z',
    title: 'Jeopardy!',
    subtitle: '',
    description: '',
    category: '',
    ...over,
  }
}

describe('normalizeQuery', () => {
  it('trims and collapses whitespace', () => {
    expect(normalizeQuery('  wheel   of  fortune ')).toBe('wheel of fortune')
  })

  it('returns null for queries under two characters', () => {
    expect(normalizeQuery('')).toBeNull()
    expect(normalizeQuery('   ')).toBeNull()
    expect(normalizeQuery(' a ')).toBeNull()
    expect(normalizeQuery('ab')).toBe('ab')
  })
})

describe('isOnNow', () => {
  const r = result()
  it('is true inside [start, stop)', () => {
    expect(isOnNow(r, new Date('2026-10-04T19:00:00Z'))).toBe(true)
    expect(isOnNow(r, new Date('2026-10-04T19:29:59Z'))).toBe(true)
  })
  it('is false before start and at stop', () => {
    expect(isOnNow(r, new Date('2026-10-04T18:59:59Z'))).toBe(false)
    expect(isOnNow(r, new Date('2026-10-04T19:30:00Z'))).toBe(false)
  })
})

describe('resultActions', () => {
  const before = new Date('2026-10-04T18:00:00Z')
  const during = new Date('2026-10-04T19:10:00Z')

  it('offers Watch only while on now', () => {
    expect(resultActions(result(), before).watch).toBe(false)
    expect(resultActions(result(), during).watch).toBe(true)
  })

  it('offers Record and Record series for an unscheduled program', () => {
    const a = resultActions(result(), before)
    expect(a.record).toBe(true)
    expect(a.series).toBe(true)
    expect(a.markText).toBe('')
  })

  it('replaces Record with the recording state when already scheduled', () => {
    const a = resultActions(result({ recording: { id: 4, state: 'scheduled' } }), before)
    expect(a.record).toBe(false)
    expect(a.series).toBe(true)
    expect(a.markText).toBe('Recording scheduled')
  })

  it('shows Recording now while recording', () => {
    const a = resultActions(result({ recording: { id: 4, state: 'recording' } }), during)
    expect(a.record).toBe(false)
    expect(a.markText).toBe('Recording now')
  })
})

describe('lockText', () => {
  it('shows the rating next to the lock', () => {
    expect(lockText('TV-MA')).toBe('🔒 TV-MA')
  })
  it('says Not rated when the rating is empty', () => {
    expect(lockText('')).toBe('🔒 Not rated')
    expect(lockText(undefined)).toBe('🔒 Not rated')
  })
})

describe('searchStatusText', () => {
  it('is empty when there is nothing to say', () => {
    expect(searchStatusText({ query: null, loading: false, error: null, count: 0 })).toBe('')
  })
  it('reports searching, errors, no results and counts', () => {
    expect(searchStatusText({ query: 'news', loading: true, error: null, count: 0 })).toBe('Searching…')
    expect(searchStatusText({ query: 'news', loading: false, error: 'boom', count: 0 })).toBe('boom')
    expect(searchStatusText({ query: 'news', loading: false, error: null, count: 0 })).toBe(
      'No upcoming programs match “news”.',
    )
    expect(searchStatusText({ query: 'news', loading: false, error: null, count: 1 })).toBe('1 program')
    expect(searchStatusText({ query: 'news', loading: false, error: null, count: 12 })).toBe('12 programs')
  })
})

describe('createDebouncer', () => {
  afterEach(() => {
    vi.useRealTimers()
  })

  it('defaults to 300 ms', () => {
    expect(SEARCH_DEBOUNCE_MS).toBe(300)
  })

  it('calls once with the latest value after the wait', () => {
    vi.useFakeTimers()
    const fn = vi.fn()
    const d = createDebouncer(fn, 300)
    d.call('w')
    vi.advanceTimersByTime(200)
    d.call('wh')
    vi.advanceTimersByTime(299)
    expect(fn).not.toHaveBeenCalled()
    vi.advanceTimersByTime(1)
    expect(fn).toHaveBeenCalledTimes(1)
    expect(fn).toHaveBeenCalledWith('wh')
  })

  it('cancel drops the pending call', () => {
    vi.useFakeTimers()
    const fn = vi.fn()
    const d = createDebouncer(fn, 300)
    d.call('x')
    d.cancel()
    vi.advanceTimersByTime(1000)
    expect(fn).not.toHaveBeenCalled()
  })
})
