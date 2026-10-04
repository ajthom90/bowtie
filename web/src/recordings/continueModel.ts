import type { Recording } from '../api/client'

/**
 * "Continue watching": recordings the viewer started and hasn't finished.
 *
 * Rules (keep in step with the other clients):
 * - ready, not parental-locked, with a known duration;
 * - watched at least a minute (positionSec ≥ 60);
 * - not near the end (positionSec < durationSec − 120);
 * - newest positionUpdatedAt first, recordings without one last;
 * - at most 10.
 */
export const CONTINUE_MIN_SEC = 60
export const CONTINUE_END_MARGIN_SEC = 120
export const CONTINUE_MAX = 10

type Continuable = Pick<
  Recording,
  'state' | 'locked' | 'positionSec' | 'durationSec' | 'positionUpdatedAt'
>

export function isContinuable(rec: Continuable): boolean {
  if (rec.state !== 'ready' || rec.locked === true) return false
  const pos = rec.positionSec
  const dur = rec.durationSec
  if (!Number.isFinite(pos) || !Number.isFinite(dur) || dur <= 0) return false
  return pos >= CONTINUE_MIN_SEC && pos < dur - CONTINUE_END_MARGIN_SEC
}

/** Milliseconds since the epoch, or null when missing / unparseable. */
function updatedAtMs(rec: Pick<Recording, 'positionUpdatedAt'>): number | null {
  if (!rec.positionUpdatedAt) return null
  const ms = Date.parse(rec.positionUpdatedAt)
  return Number.isFinite(ms) ? ms : null
}

/** The Continue watching items, in display order. */
export function selectContinueWatching<T extends Continuable>(recs: readonly T[], max = CONTINUE_MAX): T[] {
  return recs
    .map((rec, i) => ({ rec, i, at: updatedAtMs(rec) }))
    .filter(({ rec }) => isContinuable(rec))
    .sort((a, b) => {
      if (a.at !== null && b.at !== null && a.at !== b.at) return b.at - a.at
      if (a.at === null && b.at !== null) return 1
      if (a.at !== null && b.at === null) return -1
      return a.i - b.i
    })
    .slice(0, Math.max(0, max))
    .map(({ rec }) => rec)
}

/** Seconds still to watch (never negative). */
export function secondsLeft(rec: Pick<Recording, 'positionSec' | 'durationSec'>): number {
  return Math.max(0, Math.floor(rec.durationSec - rec.positionSec))
}

/** "less than a minute left", "37 min left", "1 hr left", "1 hr 5 min left". */
export function formatTimeLeft(sec: number): string {
  const totalMin = Math.floor(Number.isFinite(sec) ? Math.max(0, sec) / 60 : 0)
  if (totalMin < 1) return 'less than a minute left'
  const h = Math.floor(totalMin / 60)
  const m = totalMin % 60
  if (h === 0) return `${m} min left`
  return m === 0 ? `${h} hr left` : `${h} hr ${m} min left`
}

/** How far in, 0–100. */
export function progressPct(rec: Pick<Recording, 'positionSec' | 'durationSec'>): number {
  if (!(rec.durationSec > 0) || !Number.isFinite(rec.positionSec)) return 0
  return Math.min(100, Math.max(0, (rec.positionSec / rec.durationSec) * 100))
}

/** Accessible name for an item's play button. */
export function resumeLabel(rec: Pick<Recording, 'title' | 'subtitle' | 'positionSec' | 'durationSec'>): string {
  const name = rec.subtitle ? `${rec.title}: ${rec.subtitle}` : rec.title
  return `Resume ${name}, ${formatTimeLeft(secondsLeft(rec))}`
}

/** Accessible name for an item's remove button. */
export function removeLabel(rec: Pick<Recording, 'title' | 'subtitle'>): string {
  const name = rec.subtitle ? `${rec.title}: ${rec.subtitle}` : rec.title
  return `Remove ${name} from Continue watching`
}

/** The recording after "Remove from Continue watching" (its position reset to 0). */
export function withPositionReset<T extends Pick<Recording, 'positionSec' | 'positionUpdatedAt'>>(
  rec: T,
  now: Date = new Date(),
): T {
  return { ...rec, positionSec: 0, positionUpdatedAt: now.toISOString() }
}

// ── Guide row: hide until something new is watched ────────────────────────

/**
 * Hiding the guide's row remembers the newest positionUpdatedAt it showed
 * (server time, so the browser's clock doesn't matter). The row comes back
 * as soon as any item was saved after that — i.e. the viewer watched
 * something since. Older servers (no positionUpdatedAt) stay hidden.
 */
export const CONTINUE_HIDDEN_KEY = 'bowtie.guide.continueHiddenAt'

/** What to remember when the viewer hides the guide row. */
export function hiddenStamp(items: readonly Pick<Recording, 'positionUpdatedAt'>[]): string {
  let newest: number | null = null
  for (const it of items) {
    const ms = updatedAtMs(it)
    if (ms !== null && (newest === null || ms > newest)) newest = ms
  }
  // Nothing timestamped: hide for good (epoch 0 is older than any real stamp,
  // so a later timestamped save still brings the row back).
  return new Date(newest ?? 0).toISOString()
}

/** Whether the guide shows the row. */
export function guideRowVisible(
  items: readonly Pick<Recording, 'positionUpdatedAt'>[],
  hiddenAt: string | null,
): boolean {
  if (items.length === 0) return false
  if (!hiddenAt) return true
  const cutoff = Date.parse(hiddenAt)
  if (!Number.isFinite(cutoff)) return true
  return items.some((it) => {
    const ms = updatedAtMs(it)
    return ms !== null && ms > cutoff
  })
}

type KV = Pick<Storage, 'getItem' | 'setItem'>

function defaultStorage(): KV | null {
  try {
    return typeof window !== 'undefined' ? window.localStorage : null
  } catch {
    return null
  }
}

/** Per account: two people sharing a browser each hide their own row. */
function hiddenKey(userId: number): string {
  return `${CONTINUE_HIDDEN_KEY}.${userId}`
}

export function loadHiddenAt(userId: number, storage: KV | null = defaultStorage()): string | null {
  if (!storage) return null
  try {
    return storage.getItem(hiddenKey(userId))
  } catch {
    return null
  }
}

export function saveHiddenAt(userId: number, stamp: string, storage: KV | null = defaultStorage()): void {
  if (!storage) return
  try {
    storage.setItem(hiddenKey(userId), stamp)
  } catch {
    // Private mode / blocked storage: the row just isn't hidden next time.
  }
}
