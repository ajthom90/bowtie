/**
 * Admin → Recordings: storage gauge, padding and quality form logic.
 * Server is authority for validation; client hints are advisory only.
 */
import type { DVRStorage, RecordingQuality, SettingsDVR } from '../api/client'

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

export const DEFAULT_RECORDING_QUALITY: RecordingQuality = '720p'

export interface RecordingQualityOption {
  value: RecordingQuality
  label: string
  /** Target bitrates the server converts at (H.264 video + AAC audio). */
  videoKbps: number
  audioKbps: number
}

/**
 * dvr.quality choices, default first. Bitrates match the server's VOD
 * conversion; 1080p is the most a recording can take (1080i channels).
 */
export const RECORDING_QUALITIES: readonly RecordingQualityOption[] = [
  { value: '720p', label: '720p', videoKbps: 4000, audioKbps: 160 },
  { value: '1080p', label: 'Up to 1080p', videoKbps: 8000, audioKbps: 160 },
]

/** Bytes an hour of recording takes at the given bitrates (kb/s). */
export function bytesPerHour(videoKbps: number, audioKbps: number): number {
  return ((videoKbps + audioKbps) * 1000 * 3600) / 8
}

/** A select option's text: the choice and its size per hour. */
export function qualityOptionLabel(opt: RecordingQualityOption): string {
  const size = formatBytes(bytesPerHour(opt.videoKbps, opt.audioKbps))
  return `${opt.label} — ${opt.value === '1080p' ? 'up to' : 'about'} ${size}/hour`
}

/** One-line size explanation for a quality choice. */
export function qualitySizeHint(q: RecordingQuality): string {
  const opt = RECORDING_QUALITIES.find((o) => o.value === q) ?? RECORDING_QUALITIES[0]
  const size = formatBytes(bytesPerHour(opt.videoKbps, opt.audioKbps))
  if (opt.value === '1080p') {
    return `Up to about ${size} per hour — 1080i channels keep full resolution; 720p channels stay 720p.`
  }
  return `About ${size} per hour of recording.`
}

function toQuality(q: unknown): RecordingQuality {
  return RECORDING_QUALITIES.some((o) => o.value === q) ? (q as RecordingQuality) : DEFAULT_RECORDING_QUALITY
}

export interface PaddingForm {
  start: string
  end: string
  /** null: the server has no quality setting (don't send one). */
  quality: RecordingQuality | null
}

export function paddingToForm(dvr: SettingsDVR | undefined): PaddingForm {
  return {
    start: String(secondsToMinutes(dvr?.padStartSeconds ?? DEFAULT_PAD_START_SECONDS)),
    end: String(secondsToMinutes(dvr?.padEndSeconds ?? DEFAULT_PAD_END_SECONDS)),
    quality: dvr?.quality === undefined ? null : toQuality(dvr.quality),
  }
}

export type PaddingPayload = { ok: true; dvr: SettingsDVR } | { ok: false; error: string }

/** Validates the minute inputs and converts the form to the settings section. */
export function buildPaddingPayload(form: PaddingForm): PaddingPayload {
  const start = parseMinutes(form.start, 'Start early', MAX_PAD_START_MINUTES)
  if (typeof start === 'string') return { ok: false, error: start }
  const end = parseMinutes(form.end, 'Keep recording after', MAX_PAD_END_MINUTES)
  if (typeof end === 'string') return { ok: false, error: end }
  const dvr: SettingsDVR = { padStartSeconds: minutesToSeconds(start), padEndSeconds: minutesToSeconds(end) }
  if (form.quality !== null) dvr.quality = form.quality
  return { ok: true, dvr }
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
