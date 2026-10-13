import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { SLOW_START_MS, SLOW_START_NOTE, scheduleSlowStart, startingText } from './startingNote'

describe('startingText', () => {
  it('says "Starting stream…" at first and the slow-start note later', () => {
    expect(startingText(false)).toBe('Starting stream…')
    expect(startingText(true)).toBe(
      'Still starting — the stream is buffering. This can take a little while.',
    )
    expect(SLOW_START_NOTE).toBe(startingText(true))
  })
})

describe('scheduleSlowStart', () => {
  beforeEach(() => vi.useFakeTimers())
  afterEach(() => vi.useRealTimers())

  it('fires once after 5 s', () => {
    expect(SLOW_START_MS).toBe(5000)
    const onSlow = vi.fn()
    scheduleSlowStart(onSlow)
    vi.advanceTimersByTime(4999)
    expect(onSlow).not.toHaveBeenCalled()
    vi.advanceTimersByTime(1)
    expect(onSlow).toHaveBeenCalledTimes(1)
    vi.advanceTimersByTime(60_000)
    expect(onSlow).toHaveBeenCalledTimes(1)
  })

  it('a start that finishes (or a zap) first cancels it', () => {
    const onSlow = vi.fn()
    const cancel = scheduleSlowStart(onSlow)
    vi.advanceTimersByTime(3000)
    cancel()
    vi.advanceTimersByTime(10_000)
    expect(onSlow).not.toHaveBeenCalled()
  })
})
