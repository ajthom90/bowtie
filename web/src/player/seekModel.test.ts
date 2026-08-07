import { afterEach, describe, expect, it, vi } from 'vitest'
import {
  HEARTBEAT_INTERVAL_MS,
  OUT_OF_WINDOW_NOTICE,
  behindSeconds,
  clampSeek,
  createHeartbeatController,
  formatBehind,
  isAtLiveEdge,
  skipBack,
  type LiveWindow,
} from './seekModel'

function window(partial: Partial<LiveWindow> & Pick<LiveWindow, 'start' | 'end' | 'liveEdge' | 'current'>): LiveWindow {
  return partial
}

describe('clampSeek', () => {
  const w = window({ start: 100, end: 200, liveEdge: 195, current: 150 })

  it('returns pos unchanged when inside window', () => {
    expect(clampSeek(150, w)).toEqual({ pos: 150, clamped: false })
    expect(clampSeek(100, w)).toEqual({ pos: 100, clamped: false })
    expect(clampSeek(195, w)).toEqual({ pos: 195, clamped: false })
  })

  it('clamps to live edge when pos is before window start (out-of-window)', () => {
    expect(clampSeek(50, w)).toEqual({ pos: 195, clamped: true })
    expect(clampSeek(99.9, w)).toEqual({ pos: 195, clamped: true })
  })

  it('clamps to live edge when pos is past live edge', () => {
    expect(clampSeek(196, w)).toEqual({ pos: 195, clamped: true })
    expect(clampSeek(250, w)).toEqual({ pos: 195, clamped: true })
  })
})

describe('formatBehind', () => {
  it('formats as -mm:ss', () => {
    expect(formatBehind(0)).toBe('-00:00')
    expect(formatBehind(5)).toBe('-00:05')
    expect(formatBehind(65)).toBe('-01:05')
    expect(formatBehind(600)).toBe('-10:00')
    expect(formatBehind(3599)).toBe('-59:59')
    expect(formatBehind(3600)).toBe('-60:00')
  })

  it('floors fractional seconds and treats negatives as zero', () => {
    expect(formatBehind(5.9)).toBe('-00:05')
    expect(formatBehind(-3)).toBe('-00:00')
  })
})

describe('behindSeconds / isAtLiveEdge', () => {
  it('computes seconds behind live edge', () => {
    const w = window({ start: 0, end: 100, liveEdge: 100, current: 70 })
    expect(behindSeconds(w)).toBe(30)
  })

  it('is at live edge within default threshold', () => {
    const at = window({ start: 0, end: 100, liveEdge: 100, current: 98.5 })
    const behind = window({ start: 0, end: 100, liveEdge: 100, current: 90 })
    expect(isAtLiveEdge(at)).toBe(true)
    expect(isAtLiveEdge(behind)).toBe(false)
  })
})

describe('skipBack', () => {
  it('seeks back 30s within window', () => {
    const w = window({ start: 100, end: 200, liveEdge: 195, current: 160 })
    expect(skipBack(w, 30)).toEqual({ pos: 130, clamped: false })
  })

  it('clamps to window start when skip would leave the seekable range', () => {
    // Seeking is clamped to the seekable range (not the out-of-window jump-to-live).
    const w = window({ start: 100, end: 200, liveEdge: 195, current: 120 })
    // 120-30=90 < start → clamp to start
    expect(skipBack(w, 30)).toEqual({ pos: 100, clamped: true })
  })
})

describe('OUT_OF_WINDOW_NOTICE', () => {
  it('is the exact notice copy', () => {
    expect(OUT_OF_WINDOW_NOTICE).toBe('Jumped to live — paused longer than the buffer')
  })
})

describe('createHeartbeatController', () => {
  afterEach(() => {
    vi.useRealTimers()
    vi.restoreAllMocks()
  })

  it('uses 15s interval constant', () => {
    expect(HEARTBEAT_INTERVAL_MS).toBe(15_000)
  })

  it('fires on interval while running and stops cleanly', () => {
    vi.useFakeTimers()
    const send = vi.fn()
    const ctrl = createHeartbeatController({ send })

    ctrl.start()
    expect(send).not.toHaveBeenCalled()

    vi.advanceTimersByTime(HEARTBEAT_INTERVAL_MS - 1)
    expect(send).not.toHaveBeenCalled()

    vi.advanceTimersByTime(1)
    expect(send).toHaveBeenCalledTimes(1)

    vi.advanceTimersByTime(HEARTBEAT_INTERVAL_MS * 2)
    expect(send).toHaveBeenCalledTimes(3)

    ctrl.stop()
    vi.advanceTimersByTime(HEARTBEAT_INTERVAL_MS * 5)
    expect(send).toHaveBeenCalledTimes(3)
  })

  it('sends one beat on visibility hidden and never tears down (A6)', () => {
    vi.useFakeTimers()
    const send = vi.fn()
    const ctrl = createHeartbeatController({ send })
    ctrl.start()

    ctrl.handleVisibilityChange('hidden')
    expect(send).toHaveBeenCalledTimes(1)

    // Interval still running after hidden beat
    vi.advanceTimersByTime(HEARTBEAT_INTERVAL_MS)
    expect(send).toHaveBeenCalledTimes(2)

    // visible does not stop or double-send
    ctrl.handleVisibilityChange('visible')
    expect(send).toHaveBeenCalledTimes(2)

    vi.advanceTimersByTime(HEARTBEAT_INTERVAL_MS)
    expect(send).toHaveBeenCalledTimes(3)

    ctrl.stop()
  })

  it('does not send visibility beat when not started (session closed)', () => {
    const send = vi.fn()
    const ctrl = createHeartbeatController({ send })
    ctrl.handleVisibilityChange('hidden')
    expect(send).not.toHaveBeenCalled()
  })

  it('continues beats through multiple intervals (session open / paused)', () => {
    vi.useFakeTimers()
    const send = vi.fn()
    const ctrl = createHeartbeatController({ send })
    ctrl.start()
    // Simulate long pause: many ticks still fire
    vi.advanceTimersByTime(HEARTBEAT_INTERVAL_MS * 10)
    expect(send).toHaveBeenCalledTimes(10)
    ctrl.stop()
  })

  it('start is idempotent; stop is safe when not running', () => {
    vi.useFakeTimers()
    const send = vi.fn()
    const ctrl = createHeartbeatController({ send })
    ctrl.stop()
    ctrl.start()
    ctrl.start()
    vi.advanceTimersByTime(HEARTBEAT_INTERVAL_MS)
    expect(send).toHaveBeenCalledTimes(1)
    ctrl.stop()
    ctrl.stop()
  })
})
