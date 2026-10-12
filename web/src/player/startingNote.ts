import { useEffect, useState } from 'react'

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
