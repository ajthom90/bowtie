/**
 * Admin → Recordings: storage gauge and padding form logic.
 * Server is authority for validation; client hints are advisory only.
 */
import type { DVRStorage, SettingsDVR } from '../api/client'

const MB = 1024 ** 2
const GB = 1024 ** 3
const TB = 1024 ** 4

export const DEFAULT_PAD_START_SECONDS = 60
export const DEFAULT_PAD_END_SECONDS = 180
const MAX_PAD_START_MINUTES = 30
const MAX_PAD_END_MINUTES = 60

export const DISK_FULL_WARNING =
  "Disk almost full — new recordings won't start until space is freed."
export const DISK_LOW_NOTE =
  "Free space is below the retention limit — the oldest recordings that aren't kept will be deleted to make room."

/** GB/TB with one decimal; small sizes in MB so they never read "0.0 GB". */
export function formatBytes(bytes: number): string {
  const n = Number.isFinite(bytes) && bytes > 0 ? bytes : 0
  if (n === 0) return '0 GB'
  if (n >= TB) return `${(n / TB).toFixed(1)} TB`
  if (n >= GB) return `${(n / GB).toFixed(1)} GB`
  if (n >= MB) return `${Math.round(n / MB)} MB`
  return '< 1 MB'
}

export interface GaugeSegments {
  recordingsBytes: number
  otherBytes: number
  freeBytes: number
  recordingsPct: number
  otherPct: number
  freePct: number
}

/** Splits the disk into used-by-recordings / other used / free, clamped to the total. */
export function gaugeSegments(s: DVRStorage): GaugeSegments {
  const total = Math.max(0, s.totalBytes)
  const free = clamp(s.freeBytes, 0, total)
  const recordings = clamp(s.usedBytes, 0, total - free)
  const other = total - free - recordings
  const pct = (n: number) => (total > 0 ? (n / total) * 100 : 0)
  return {
    recordingsBytes: recordings,
    otherBytes: other,
    freeBytes: free,
    recordingsPct: pct(recordings),
    otherPct: pct(other),
    freePct: pct(free),
  }
}

/** 'full': captures won't start; 'low': the retention sweep is deleting. */
export function storageWarning(s: DVRStorage): 'full' | 'low' | null {
  if (s.freeBytes < s.floorBytes) return 'full'
  if (s.minFreeBytes > 0 && s.freeBytes < s.minFreeBytes) return 'low'
  return null
}

export function secondsToMinutes(sec: number): number {
  return Math.round(sec / 6) / 10
}

export function minutesToSeconds(min: number): number {
  return Math.round(min * 60)
}

export interface PaddingForm {
  start: string
  end: string
}

export function paddingToForm(dvr: SettingsDVR | undefined): PaddingForm {
  return {
    start: String(secondsToMinutes(dvr?.padStartSeconds ?? DEFAULT_PAD_START_SECONDS)),
    end: String(secondsToMinutes(dvr?.padEndSeconds ?? DEFAULT_PAD_END_SECONDS)),
  }
}

export type PaddingPayload = { ok: true; dvr: SettingsDVR } | { ok: false; error: string }

/** Validates the minute inputs and converts them to the settings section. */
export function buildPaddingPayload(form: PaddingForm): PaddingPayload {
  const start = parseMinutes(form.start, 'Start early', MAX_PAD_START_MINUTES)
  if (typeof start === 'string') return { ok: false, error: start }
  const end = parseMinutes(form.end, 'Keep recording after', MAX_PAD_END_MINUTES)
  if (typeof end === 'string') return { ok: false, error: end }
  return { ok: true, dvr: { padStartSeconds: minutesToSeconds(start), padEndSeconds: minutesToSeconds(end) } }
}

function parseMinutes(raw: string, label: string, max: number): number | string {
  const s = raw.trim()
  const n = Number(s)
  if (s === '' || !Number.isFinite(n)) return `${label} must be a number of minutes`
  if (n < 0 || n > max) return `${label} must be between 0 and ${max} minutes`
  return n
}

function clamp(n: number, lo: number, hi: number): number {
  if (!Number.isFinite(n)) return lo
  return Math.min(Math.max(n, lo), hi)
}
