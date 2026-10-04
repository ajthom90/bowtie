import type { CommercialBreak } from '../api/client'

/**
 * Commercial skipping for the recording player. Breaks come from the server
 * (Comskip) as seconds on the playback timeline. A break is half-open
 * [start, end): the playhead exactly at its start is inside, exactly at its
 * end is past it. END_SLOP: a seek to a break's end can land a hair early
 * (the nearest frame), which must not count as still inside.
 */
export const END_SLOP = 0.25

/** Valid breaks, sorted, overlapping ones merged (the server already does this; be defensive). */
export function normalizeBreaks(input: readonly CommercialBreak[] | null | undefined): CommercialBreak[] {
  const valid = (input ?? [])
    .filter((b) => Number.isFinite(b.start) && Number.isFinite(b.end) && b.end > b.start)
    .map((b) => ({ start: Math.max(0, b.start), end: b.end }))
    .filter((b) => b.end > b.start)
    .sort((a, b) => a.start - b.start || a.end - b.end)
  const out: CommercialBreak[] = []
  for (const b of valid) {
    const last = out[out.length - 1]
    if (last && b.start <= last.end) {
      last.end = Math.max(last.end, b.end)
    } else {
      out.push({ ...b })
    }
  }
  return out
}

/** Index of the break the playhead is in, or -1. `breaks` must be normalized. */
export function breakIndexAt(breaks: readonly CommercialBreak[], t: number): number {
  if (!Number.isFinite(t)) return -1
  for (let i = 0; i < breaks.length; i++) {
    const b = breaks[i]
    if (t < b.start) return -1
    if (t < b.end - END_SLOP) return i
  }
  return -1
}

export type SkipUpdate = {
  /** The break the playhead is in (null: none). */
  current: CommercialBreak | null
  /** Seek here now (auto-skip), or null. */
  seekTo: number | null
}

export type CommercialSkipper = {
  readonly breaks: readonly CommercialBreak[]
  /** On every time update: where we are, and whether to auto-skip. */
  update(t: number, autoSkip: boolean): SkipUpdate
  /** The Skip button / S key: where to seek (the break's end), or null outside a break. */
  skip(t: number): number | null
}

/**
 * Auto-skip skips each break once: after it skipped a break (or the viewer
 * did), seeking back into it leaves the viewer there with the Skip button.
 */
export function createCommercialSkipper(
  input: readonly CommercialBreak[] | null | undefined,
): CommercialSkipper {
  const breaks = normalizeBreaks(input)
  const skipped = new Set<number>()
  return {
    breaks,
    update(t, autoSkip) {
      const i = breakIndexAt(breaks, t)
      if (i < 0) return { current: null, seekTo: null }
      if (autoSkip && !skipped.has(i)) {
        skipped.add(i)
        return { current: breaks[i], seekTo: breaks[i].end }
      }
      return { current: breaks[i], seekTo: null }
    },
    skip(t) {
      const i = breakIndexAt(breaks, t)
      if (i < 0) return null
      skipped.add(i)
      return breaks[i].end
    },
  }
}

const AUTO_SKIP_KEY = 'bowtie.autoSkipAds'

function defaultStorage(): Storage | undefined {
  try {
    return window.localStorage
  } catch {
    return undefined
  }
}

/** The per-browser "Auto-skip ads" choice (default off). */
export function loadAutoSkip(storage: Storage | undefined = defaultStorage()): boolean {
  try {
    return storage?.getItem(AUTO_SKIP_KEY) === '1'
  } catch {
    return false
  }
}

export function saveAutoSkip(on: boolean, storage: Storage | undefined = defaultStorage()): void {
  try {
    storage?.setItem(AUTO_SKIP_KEY, on ? '1' : '0')
  } catch {
    // private mode / blocked storage: the choice lasts for this page only
  }
}

/** The S key skips, unless the viewer is typing somewhere or holding a modifier. */
export function isSkipKey(e: {
  key: string
  ctrlKey?: boolean
  metaKey?: boolean
  altKey?: boolean
  target?: EventTarget | null
}): boolean {
  if (e.key !== 's' && e.key !== 'S') return false
  if (e.ctrlKey || e.metaKey || e.altKey) return false
  const el = e.target as (HTMLElement & { isContentEditable?: boolean }) | null | undefined
  const tag = el?.tagName
  if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT') return false
  if (el?.isContentEditable) return false
  return true
}
