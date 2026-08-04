/** Program as returned by GET /api/v1/guide (camelCase). */
export interface GuideProgram {
  start: string
  stop: string
  title: string
  subtitle: string
  description: string
  category: string
}

export type ProgramCell = {
  kind: 'program'
  program: GuideProgram
  /** Clipped start within the window. */
  start: Date
  /** Clipped stop within the window. */
  stop: Date
  /** Left offset as percent of window width [0, 100]. */
  leftPct: number
  /** Width as percent of window width (0, 100]. */
  widthPct: number
}

export type GapCell = {
  kind: 'gap'
  start: Date
  stop: Date
  leftPct: number
  widthPct: number
}

export type Cell = ProgramCell | GapCell

/**
 * Layout a channel's programs into a row of cells for a fixed time window.
 * Clips programs to the window, computes % offsets/widths, and inserts gap
 * cells for holes (including leading/trailing empty regions).
 */
export function layoutRow(
  programs: GuideProgram[],
  windowStart: Date,
  windowStop: Date,
): Cell[] {
  const winStartMs = windowStart.getTime()
  const winStopMs = windowStop.getTime()
  const durationMs = winStopMs - winStartMs
  if (durationMs <= 0) {
    return []
  }

  const sorted = programs
    .map((p) => ({
      program: p,
      startMs: Date.parse(p.start),
      stopMs: Date.parse(p.stop),
    }))
    .filter((p) => !Number.isNaN(p.startMs) && !Number.isNaN(p.stopMs) && p.stopMs > p.startMs)
    .sort((a, b) => a.startMs - b.startMs)

  const cells: Cell[] = []
  let cursor = winStartMs

  const pushGap = (from: number, to: number) => {
    if (to <= from) return
    cells.push({
      kind: 'gap',
      start: new Date(from),
      stop: new Date(to),
      leftPct: pct(from, winStartMs, durationMs),
      widthPct: pctWidth(from, to, durationMs),
    })
  }

  for (const item of sorted) {
    // Completely outside window.
    if (item.stopMs <= winStartMs || item.startMs >= winStopMs) {
      continue
    }
    const clippedStart = Math.max(item.startMs, winStartMs)
    const clippedStop = Math.min(item.stopMs, winStopMs)
    if (clippedStop <= clippedStart) {
      continue
    }

    // Gap before this program.
    if (clippedStart > cursor) {
      pushGap(cursor, clippedStart)
    }

    // Overlap with previous cell (overlapping programs): advance only.
    if (clippedStop <= cursor) {
      continue
    }
    const progStart = Math.max(clippedStart, cursor)
    const progStop = clippedStop

    cells.push({
      kind: 'program',
      program: item.program,
      start: new Date(progStart),
      stop: new Date(progStop),
      leftPct: pct(progStart, winStartMs, durationMs),
      widthPct: pctWidth(progStart, progStop, durationMs),
    })
    cursor = progStop
  }

  // Trailing gap.
  if (cursor < winStopMs) {
    pushGap(cursor, winStopMs)
  }

  return cells
}

/** 30-minute gridline timestamps within [windowStart, windowStop). */
export function halfHourTicks(windowStart: Date, windowStop: Date): Date[] {
  const ticks: Date[] = []
  const startMs = windowStart.getTime()
  const stopMs = windowStop.getTime()
  if (stopMs <= startMs) return ticks

  let t = floorToHalfHour(windowStart).getTime()
  if (t < startMs) {
    t += 30 * 60 * 1000
  }
  while (t < stopMs) {
    ticks.push(new Date(t))
    t += 30 * 60 * 1000
  }
  return ticks
}

/** Left offset of `now` within the window as a percentage, or null if outside. */
export function nowLinePct(now: Date, windowStart: Date, windowStop: Date): number | null {
  const n = now.getTime()
  const a = windowStart.getTime()
  const b = windowStop.getTime()
  if (n < a || n > b || b <= a) return null
  return ((n - a) / (b - a)) * 100
}

function pct(ms: number, winStartMs: number, durationMs: number): number {
  return ((ms - winStartMs) / durationMs) * 100
}

function pctWidth(from: number, to: number, durationMs: number): number {
  return ((to - from) / durationMs) * 100
}

/** Format a time for guide axis labels (local, 24h-ish HH:MM). */
export function formatGuideTime(d: Date): string {
  const h = d.getHours()
  const m = d.getMinutes()
  return `${String(h).padStart(2, '0')}:${String(m).padStart(2, '0')}`
}

/** Format a program time range for cell subtitles. */
export function formatTimeRange(start: Date, stop: Date): string {
  return `${formatGuideTime(start)}–${formatGuideTime(stop)}`
}

/** Default guide window: 4 hours starting at the nearest prior half-hour (or now if aligned). */
export function defaultWindow(now: Date = new Date()): { start: Date; stop: Date } {
  const start = floorToHalfHour(now)
  const stop = new Date(start.getTime() + 4 * 60 * 60 * 1000)
  return { start, stop }
}

export function floorToHalfHour(d: Date): Date {
  const out = new Date(d)
  out.setSeconds(0, 0)
  const mins = out.getMinutes()
  if (mins < 30) {
    out.setMinutes(0)
  } else {
    out.setMinutes(30)
  }
  return out
}

export function shiftWindow(
  start: Date,
  stop: Date,
  direction: -1 | 1,
): { start: Date; stop: Date } {
  const span = stop.getTime() - start.getTime()
  const delta = direction * span
  return {
    start: new Date(start.getTime() + delta),
    stop: new Date(stop.getTime() + delta),
  }
}
