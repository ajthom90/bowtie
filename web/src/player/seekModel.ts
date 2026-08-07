/**
 * Pure live-window math for the web player seek bar.
 *
 * Player.tsx builds LiveWindow from hls.js levelDetails + liveSyncPosition +
 * video.currentTime (A8). This module never imports hls.js.
 */

/** Sliding DVR window + playback cursor (media timeline seconds). */
export type LiveWindow = {
  start: number
  end: number
  liveEdge: number
  current: number
}

/** Exact out-of-window notice copy (spec B). */
export const OUT_OF_WINDOW_NOTICE = 'Jumped to live — paused longer than the buffer'

/** Client heartbeat interval (spec C). */
export const HEARTBEAT_INTERVAL_MS = 15_000

/** Seconds behind live edge still counted as "at edge" for the LIVE badge. */
export const LIVE_EDGE_THRESHOLD_S = 3

/**
 * Clamp a seek target to the live window.
 * Positions before `start` (out of buffer) jump to `liveEdge` (spec B).
 * Positions past `liveEdge` clamp to `liveEdge`.
 */
export function clampSeek(
  pos: number,
  window: Pick<LiveWindow, 'start' | 'liveEdge'>,
): { pos: number; clamped: boolean } {
  if (!Number.isFinite(pos)) {
    return { pos: window.liveEdge, clamped: true }
  }
  if (pos < window.start) {
    return { pos: window.liveEdge, clamped: true }
  }
  if (pos > window.liveEdge) {
    return { pos: window.liveEdge, clamped: true }
  }
  return { pos, clamped: false }
}

/**
 * Skip backward within the seekable range. Lands on `start` when the step
 * would leave the window (does not force jump-to-live — that is only for
 * out-of-window *playback* via clampSeek on current).
 */
export function skipBack(
  window: LiveWindow,
  seconds = 30,
): { pos: number; clamped: boolean } {
  const target = window.current - seconds
  if (target < window.start) {
    return { pos: window.start, clamped: true }
  }
  if (target > window.liveEdge) {
    return { pos: window.liveEdge, clamped: true }
  }
  return { pos: target, clamped: false }
}

/** Seconds the playhead is behind the live edge (floored at 0). */
export function behindSeconds(window: Pick<LiveWindow, 'liveEdge' | 'current'>): number {
  const d = window.liveEdge - window.current
  if (!Number.isFinite(d) || d <= 0) return 0
  return d
}

/** True when playhead is at (or within threshold of) the live edge. */
export function isAtLiveEdge(
  window: Pick<LiveWindow, 'liveEdge' | 'current'>,
  thresholdS = LIVE_EDGE_THRESHOLD_S,
): boolean {
  return behindSeconds(window) <= thresholdS
}

/**
 * Format distance-behind for the LIVE badge: "-mm:ss".
 * Non-positive values become "-00:00".
 */
export function formatBehind(seconds: number): string {
  const s = Number.isFinite(seconds) ? Math.max(0, Math.floor(seconds)) : 0
  const m = Math.floor(s / 60)
  const rem = s % 60
  return `-${String(m).padStart(2, '0')}:${String(rem).padStart(2, '0')}`
}

// ── Heartbeat scheduling (testable; A6) ────────────────────────────────────

export type HeartbeatController = {
  /** Begin 15s interval ticks. Idempotent. */
  start: () => void
  /** Stop ticks. Idempotent. Safe when not running. */
  stop: () => void
  /**
   * visibilitychange handler. On 'hidden' while running: send ONE beat.
   * Never tears down the session / controller (A6).
   */
  handleVisibilityChange: (visibilityState: DocumentVisibilityState) => void
  /** Whether the interval is currently armed. */
  isRunning: () => boolean
}

type HeartbeatDeps = {
  send: () => void
  intervalMs?: number
  setIntervalFn?: typeof setInterval
  clearIntervalFn?: typeof clearInterval
}

/**
 * Session-open heartbeat: interval while running; one immediate beat on
 * document hidden. Stops only via stop() (real leave / unmount).
 */
export function createHeartbeatController(deps: HeartbeatDeps): HeartbeatController {
  const intervalMs = deps.intervalMs ?? HEARTBEAT_INTERVAL_MS
  const setI = deps.setIntervalFn ?? setInterval
  const clearI = deps.clearIntervalFn ?? clearInterval
  let timer: ReturnType<typeof setInterval> | null = null

  const start = () => {
    if (timer != null) return
    timer = setI(() => {
      deps.send()
    }, intervalMs)
  }

  const stop = () => {
    if (timer == null) return
    clearI(timer)
    timer = null
  }

  const handleVisibilityChange = (visibilityState: DocumentVisibilityState) => {
    if (visibilityState === 'hidden' && timer != null) {
      deps.send()
    }
  }

  return {
    start,
    stop,
    handleVisibilityChange,
    isRunning: () => timer != null,
  }
}
