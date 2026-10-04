import type {
  GuideRecordingMark,
  Recording,
  RecordingFailure,
  RecordingsFilter,
} from '../api/client'
import { formatTimeRange } from '../guide/guideModel'

// ── Tabs ────────────────────────────────────────────────────────────────────

export type RecordingsTab = 'upcoming' | 'recorded' | 'missed'

export const RECORDINGS_TABS: { id: RecordingsTab; label: string }[] = [
  { id: 'upcoming', label: 'Upcoming' },
  { id: 'recorded', label: 'Recorded' },
  { id: 'missed', label: 'Missed' },
]

/** Server filter for a tab. The server filters and sorts; Missed = failed. */
export function tabQuery(tab: RecordingsTab): RecordingsFilter {
  return tab === 'missed' ? 'failed' : tab
}

export const EMPTY_TAB_COPY: Record<RecordingsTab, string> = {
  upcoming: 'Nothing scheduled. Pick a show in the guide and choose Record.',
  recorded: 'No recordings yet.',
  missed: 'No missed recordings.',
}

// ── Labels ──────────────────────────────────────────────────────────────────

export type Badge = { label: string; tone: 'neutral' | 'live' | 'warn' }

/** State badges for a row. Ready shows none; failed rows show failure text instead. */
export function recordingBadges(rec: Pick<Recording, 'state' | 'partial'>): Badge[] {
  const out: Badge[] = []
  switch (rec.state) {
    case 'scheduled':
      out.push({ label: 'Scheduled', tone: 'neutral' })
      break
    case 'waiting':
      out.push({ label: 'Waiting for a tuner', tone: 'warn' })
      break
    case 'recording':
      out.push({ label: '● Recording', tone: 'live' })
      break
    case 'converting':
      out.push({ label: 'Converting', tone: 'neutral' })
      break
    default:
      break
  }
  if (rec.partial && (rec.state === 'ready' || rec.state === 'converting')) {
    out.push({ label: 'Partial', tone: 'warn' })
  }
  return out
}

/** Why a recording failed, in plain words. */
export function failureText(failure: RecordingFailure | string, detail: string): string {
  switch (failure) {
    case 'noTuner':
      return 'No tuner was free'
    case 'noSignal':
      return 'No signal'
    case 'diskFull':
      return 'Disk full'
    case 'error':
      return detail.trim() || 'Something went wrong'
    default:
      return ''
  }
}

// ── Formatting ──────────────────────────────────────────────────────────────

/** "33 min", "1 h 30 min"; "—" when unknown. */
export function formatDuration(sec: number): string {
  if (!Number.isFinite(sec) || sec <= 0) return '—'
  if (sec < 60) return '<1 min'
  const totalMin = Math.round(sec / 60)
  const h = Math.floor(totalMin / 60)
  const m = totalMin % 60
  if (h === 0) return `${m} min`
  return m === 0 ? `${h} h` : `${h} h ${m} min`
}

const SIZE_UNITS = ['B', 'KB', 'MB', 'GB', 'TB'] as const

/** Decimal units ("3.8 GB"); one decimal below 10; "—" when unknown. */
export function formatSize(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes <= 0) return '—'
  let v = bytes
  let i = 0
  while (v >= 1000 && i < SIZE_UNITS.length - 1) {
    v /= 1000
    i++
  }
  if (i === 0) return `${Math.round(v)} B`
  const num = v < 10 ? v.toFixed(1).replace(/\.0$/, '') : String(Math.round(v))
  return `${num} ${SIZE_UNITS[i]}`
}

/** Playback clock: "0:05", "12:34", "1:02:03". */
export function formatClock(sec: number): string {
  const s = Math.max(0, Math.floor(Number.isFinite(sec) ? sec : 0))
  const h = Math.floor(s / 3600)
  const m = Math.floor((s % 3600) / 60)
  const ss = String(s % 60).padStart(2, '0')
  return h > 0 ? `${h}:${String(m).padStart(2, '0')}:${ss}` : `${m}:${ss}`
}

function sameDay(a: Date, b: Date): boolean {
  return (
    a.getFullYear() === b.getFullYear() && a.getMonth() === b.getMonth() && a.getDate() === b.getDate()
  )
}

/** "Today 19:00–19:30", "Tomorrow …", "Yesterday …", else a short local date. */
export function formatWhen(start: string, stop: string, now: Date = new Date()): string {
  const a = new Date(start)
  const b = new Date(stop)
  const day = (offset: number) => {
    const d = new Date(now)
    d.setDate(d.getDate() + offset)
    return d
  }
  let label: string
  if (sameDay(a, now)) label = 'Today'
  else if (sameDay(a, day(1))) label = 'Tomorrow'
  else if (sameDay(a, day(-1))) label = 'Yesterday'
  else
    label = a.toLocaleDateString(undefined, {
      weekday: 'short',
      month: 'short',
      day: 'numeric',
      ...(a.getFullYear() !== now.getFullYear() ? { year: 'numeric' } : {}),
    })
  return `${label} ${formatTimeRange(a, b)}`
}

// ── Guide ───────────────────────────────────────────────────────────────────

/** Red-dot text on a guide cell: "● REC" while recording, "●" otherwise. */
export function guideRecLabel(mark: GuideRecordingMark | undefined): string | null {
  if (!mark) return null
  return mark.state === 'recording' ? '● REC' : '●'
}

export type ProgramRecordAction = 'record' | 'cancel' | 'stop' | 'recorded'

/** What the guide popover offers for a program, or null for nothing. */
export function programRecordAction(
  program: { start: string; stop: string; recording?: GuideRecordingMark },
  now: Date,
): ProgramRecordAction | null {
  const mark = program.recording
  if (mark) {
    switch (mark.state) {
      case 'scheduled':
      case 'waiting':
        return 'cancel'
      case 'recording':
        return 'stop'
      case 'converting':
      case 'ready':
        return 'recorded'
      default:
        break
    }
  }
  return Date.parse(program.stop) > now.getTime() ? 'record' : null
}

/** 409 dialog heading, built from the tuner count. */
export function conflictHeading(tunerCount: number): string {
  return `Only ${tunerCount} ${tunerCount === 1 ? 'tuner' : 'tuners'} — these recordings already need them:`
}

export function conflictLine(rec: Pick<Recording, 'title' | 'channelName' | 'start' | 'stop'>): string {
  return `${rec.title} · ${rec.channelName} · ${formatTimeRange(new Date(rec.start), new Date(rec.stop))}`
}

// ── Row actions ─────────────────────────────────────────────────────────────

export type RowActions = {
  play: boolean
  stop: boolean
  /** Cancel an upcoming recording, or delete one with files. */
  remove: 'cancel' | 'delete' | null
  /** Protected toggle. */
  keep: boolean
}

export function rowActions(rec: Pick<Recording, 'state' | 'canManage'>): RowActions {
  const manage = rec.canManage
  const upcoming = rec.state === 'scheduled' || rec.state === 'waiting'
  return {
    play: rec.state === 'ready',
    stop: manage && rec.state === 'recording',
    remove: manage ? (upcoming ? 'cancel' : 'delete') : null,
    keep: manage && (rec.state === 'ready' || rec.state === 'converting'),
  }
}

export function removeConfirmText(rec: Pick<Recording, 'title'>, kind: 'cancel' | 'delete'): string {
  return kind === 'cancel'
    ? `Cancel recording “${rec.title}”?`
    : `Delete “${rec.title}”? This removes the recording for everyone.`
}

// ── Playback ────────────────────────────────────────────────────────────────

export type ResumeDecision = { kind: 'ask'; positionSec: number } | { kind: 'start' }

/**
 * Offer "Resume from …" only past the first 10 s and before the last 30 s.
 * With an unknown duration, any position past 10 s is offered.
 */
export function resumeDecision(positionSec: number, durationSec: number): ResumeDecision {
  if (!(positionSec > 10)) return { kind: 'start' }
  if (durationSec > 0 && positionSec >= durationSec - 30) return { kind: 'start' }
  return { kind: 'ask', positionSec }
}

export const POSITION_SAVE_MS = 15_000

/**
 * Saves the playback position every interval when it changed, and on flush
 * (close / page hide). Save errors are swallowed; a failed save is retried
 * on the next tick.
 */
export function createPositionSaver(opts: {
  save: (positionSec: number) => void | Promise<unknown>
  /** Current position in seconds, or null when not playing yet. */
  getPosition: () => number | null
  intervalMs?: number
}) {
  const intervalMs = opts.intervalMs ?? POSITION_SAVE_MS
  let timer: ReturnType<typeof setInterval> | null = null
  let lastSaved: number | null = null

  const saveNow = async (): Promise<void> => {
    const raw = opts.getPosition()
    if (raw == null || !Number.isFinite(raw)) return
    const pos = Math.max(0, Math.floor(raw))
    if (pos === lastSaved) return
    const prev = lastSaved
    lastSaved = pos
    try {
      await opts.save(pos)
    } catch {
      if (lastSaved === pos) lastSaved = prev
    }
  }

  return {
    start() {
      if (timer != null) return
      timer = setInterval(() => void saveNow(), intervalMs)
    },
    stop() {
      if (timer != null) clearInterval(timer)
      timer = null
    },
    flush: saveNow,
  }
}
