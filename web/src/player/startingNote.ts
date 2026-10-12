import { useEffect, useState, type RefObject } from 'react'

/**
 * A channel start can take a while: the server answers once the tuner is on
 * and the first segment is written, then the first frame still has to load.
 * After SLOW_START_MS of "Starting stream…" the player says why.
 */
export const SLOW_START_MS = 5000
export const STARTING_TEXT = 'Starting stream…'
export const SLOW_START_NOTE = 'Still starting — the stream is buffering. This can take a little while.'

export function startingText(slow: boolean): string {
  return slow ? SLOW_START_NOTE : STARTING_TEXT
}

/** Calls onSlow once after `ms`; returns a cancel function. */
export function scheduleSlowStart(onSlow: () => void, ms: number = SLOW_START_MS): () => void {
  const id = setTimeout(onSlow, ms)
  return () => clearTimeout(id)
}

/**
 * True once `active` (still starting) has lasted SLOW_START_MS. A new
 * `restartKey` (zap, quality change, Try again) starts the count over.
 */
export function useSlowStart(active: boolean, restartKey: unknown): boolean {
  const [slow, setSlow] = useState(false)
  useEffect(() => {
    setSlow(false)
    if (!active) return
    return scheduleSlowStart(() => setSlow(true))
  }, [active, restartKey])
  return slow
}

/**
 * The viewer is open but its first frame hasn't loaded: arm it when the
 * server answers; the video's `loadeddata` or `playing` disarms it.
 */
export function useAwaitingFirstFrame(
  videoRef: RefObject<HTMLVideoElement | null>,
): [boolean, (on: boolean) => void] {
  const [awaiting, setAwaiting] = useState(false)
  useEffect(() => {
    const video = videoRef.current
    if (!video || !awaiting) return
    const onFrame = () => setAwaiting(false)
    video.addEventListener('loadeddata', onFrame)
    video.addEventListener('playing', onFrame)
    return () => {
      video.removeEventListener('loadeddata', onFrame)
      video.removeEventListener('playing', onFrame)
    }
  }, [videoRef, awaiting])
  return [awaiting, setAwaiting]
}

/** "Starting stream…" shows while the server starts the stream and until the first frame. */
export function showsStarting(loading: boolean, awaitingFrame: boolean, failed: boolean): boolean {
  return (loading || awaitingFrame) && !failed
}
