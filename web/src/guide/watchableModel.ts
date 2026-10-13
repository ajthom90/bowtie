/**
 * All tuners busy: which channels can start right now.
 *
 * The server marks a channel `watchable: false` when every tuner on its
 * HDHomeRun is busy with other channels and nobody in Bowtie is watching or
 * recording this one. Channel lists show only the watchable ones; the guide
 * keeps every row (people still browse and record there) but dims the busy
 * ones and turns off their Watch actions.
 */

/** Above a channel list when some channels are hidden because tuners are busy. */
export const SOME_TUNERS_BUSY = 'All tuners are in use — showing channels you can join.'

/** When no channel can start. */
export const ALL_TUNERS_BUSY = 'All tuners are in use. Try again in a few minutes.'

/** How often an open channel list asks again (a tuner may have freed up). */
export const RECHECK_INTERVAL_MS = 30_000

/** Older servers don't send `watchable`: every channel can start. */
export function isWatchable(channel: { watchable?: boolean }): boolean {
  return channel.watchable !== false
}

/** The note above a channel list, or null when every channel can start. */
export function busyNote(channels: readonly { watchable?: boolean }[]): string | null {
  if (channels.every(isWatchable)) return null
  return channels.some(isWatchable) ? SOME_TUNERS_BUSY : ALL_TUNERS_BUSY
}

/** Only the channels that can start, in their incoming order. */
export function watchableOnly<T extends { watchable?: boolean }>(channels: readonly T[]): T[] {
  return channels.filter(isWatchable)
}

/** Recents (which carry no `watchable`) minus those whose channel is busy. */
export function watchableRecents<T extends { channelId: number }>(
  recents: readonly T[],
  channels: readonly { channelId: number; watchable?: boolean }[],
): T[] {
  const busy = new Set(channels.filter((c) => !isWatchable(c)).map((c) => c.channelId))
  return recents.filter((r) => !busy.has(r.channelId))
}

/**
 * Guide rows with `watchable` refreshed from GET /channels (matched by id).
 * Returns `rows` itself when nothing changed, so React skips the re-render.
 */
export function withWatchable<T extends { channelId: number; watchable?: boolean }>(
  rows: T[],
  channels: readonly { id: number; watchable?: boolean }[],
): T[] {
  const latest = new Map(channels.map((c) => [c.id, isWatchable(c)]))
  let changed = false
  const out = rows.map((r) => {
    const w = latest.get(r.channelId)
    if (w === undefined || w === isWatchable(r)) return r
    changed = true
    return { ...r, watchable: w }
  })
  return changed ? out : rows
}

// ── Re-check scheduling (testable, like the player's heartbeat) ───────────

export type RecheckController = {
  /** Begin checking every interval. Idempotent. */
  start: () => void
  /** Stop checking. Idempotent. */
  stop: () => void
  /** visibilitychange: pause while hidden; check at once on return. */
  handleVisibilityChange: (visibilityState: DocumentVisibilityState) => void
}

type RecheckDeps = {
  check: () => void
  intervalMs?: number
  setIntervalFn?: typeof setInterval
  clearIntervalFn?: typeof clearInterval
}

/** Checks every 30 s while the page is visible, and as soon as it comes back. */
export function createRecheckController(deps: RecheckDeps): RecheckController {
  const intervalMs = deps.intervalMs ?? RECHECK_INTERVAL_MS
  const setI = deps.setIntervalFn ?? setInterval
  const clearI = deps.clearIntervalFn ?? clearInterval
  let running = false
  let timer: ReturnType<typeof setInterval> | null = null

  const arm = () => {
    if (timer == null) timer = setI(() => deps.check(), intervalMs)
  }
  const disarm = () => {
    if (timer == null) return
    clearI(timer)
    timer = null
  }

  return {
    start: () => {
      running = true
      arm()
    },
    stop: () => {
      running = false
      disarm()
    },
    handleVisibilityChange: (state) => {
      if (!running) return
      if (state === 'hidden') {
        disarm()
        return
      }
      deps.check()
      arm()
    },
  }
}
