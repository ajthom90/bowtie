import type { AdminChannel, EPGSourceState, EPGSourceStatus } from '../api/client'

/**
 * Parse a guide number into numeric segments for natural sort.
 * "10.1" → [10, 1]; non-numeric segments sort after numeric peers.
 */
export function guideNumberParts(guideNumber: string): number[] {
  const raw = guideNumber.trim()
  if (!raw) return []
  return raw.split(/[.\-_]/).map((seg) => {
    const n = Number(seg)
    return Number.isFinite(n) ? n : Number.POSITIVE_INFINITY
  })
}

/** Numeric-aware comparison: 5.1 < 10.1; falls back to localeCompare on ties. */
export function compareGuideNumbers(a: string, b: string): number {
  const pa = guideNumberParts(a)
  const pb = guideNumberParts(b)
  const len = Math.max(pa.length, pb.length)
  for (let i = 0; i < len; i++) {
    const va = pa[i] ?? 0
    const vb = pb[i] ?? 0
    if (va !== vb) return va - vb
  }
  return a.localeCompare(b)
}

/**
 * Filter channels by guide number or name (case-insensitive substring),
 * then sort by guide number (numeric-aware).
 */
export function filterAndSortChannels(
  channels: AdminChannel[],
  filter: string,
): AdminChannel[] {
  const q = filter.trim().toLowerCase()
  const filtered = q
    ? channels.filter(
        (ch) =>
          ch.guideNumber.toLowerCase().includes(q) ||
          ch.name.toLowerCase().includes(q) ||
          ch.deviceId.toLowerCase().includes(q),
      )
    : channels.slice()

  return filtered.sort((a, b) => compareGuideNumbers(a.guideNumber, b.guideNumber))
}

/** True when lastSuccess is missing or a zero-ish timestamp. */
export function isZeroTime(iso: string | undefined | null): boolean {
  if (!iso || !iso.trim()) return true
  const t = Date.parse(iso)
  if (Number.isNaN(t)) return true
  // Go zero time is year 1; treat pre-1970 as never.
  return t < 0 || new Date(t).getUTCFullYear() < 1970
}

/** Format an ISO timestamp for mono readouts (local, compact). */
export function formatTimestamp(iso: string | undefined | null): string {
  if (isZeroTime(iso)) return 'never'
  const d = new Date(iso!)
  const y = d.getFullYear()
  const mo = String(d.getMonth() + 1).padStart(2, '0')
  const day = String(d.getDate()).padStart(2, '0')
  const h = String(d.getHours()).padStart(2, '0')
  const mi = String(d.getMinutes()).padStart(2, '0')
  const s = String(d.getSeconds()).padStart(2, '0')
  return `${y}-${mo}-${day} ${h}:${mi}:${s}`
}

/** Human uptime from an ISO start time to `now`. */
export function formatUptime(startedAt: string, now: Date = new Date()): string {
  const start = Date.parse(startedAt)
  if (Number.isNaN(start)) return '—'
  let sec = Math.max(0, Math.floor((now.getTime() - start) / 1000))
  const h = Math.floor(sec / 3600)
  sec %= 3600
  const m = Math.floor(sec / 60)
  const s = sec % 60
  if (h > 0) return `${h}h ${String(m).padStart(2, '0')}m ${String(s).padStart(2, '0')}s`
  if (m > 0) return `${m}m ${String(s).padStart(2, '0')}s`
  return `${s}s`
}

export type EPGSourceKey = 'hdhomerun' | 'xmltv' | 'sd'

export const EPG_SOURCE_LABELS: Record<EPGSourceKey, string> = {
  hdhomerun: 'HDHomeRun (free guide)',
  xmltv: 'XMLTV',
  sd: 'Schedules Direct',
}

/** Guide sources to show, HDHomeRun first (absent on older servers). */
export function epgSources(
  status: EPGSourceStatus,
): { key: EPGSourceKey; label: string; state: EPGSourceState }[] {
  const keys: EPGSourceKey[] = ['hdhomerun', 'xmltv', 'sd']
  return keys.flatMap((key) => {
    const state = status[key]
    return state ? [{ key, label: EPG_SOURCE_LABELS[key], state }] : []
  })
}

export function anyEpgStale(status: EPGSourceStatus): boolean {
  return epgSources(status).some((s) => s.state.stale)
}

export function anyEpgConfigured(status: EPGSourceStatus): boolean {
  return epgSources(status).some((s) => s.state.configured)
}

/**
 * A source's state in words. The server calls a source that has never
 * succeeded "stale"; here that's "never" (no error yet) or "failing".
 */
export type EPGHealth = 'off' | 'ok' | 'stale' | 'never' | 'failing'

export function epgHealth(state: EPGSourceState): EPGHealth {
  if (!state.configured) return 'off'
  if (isZeroTime(state.lastSuccess)) return state.lastError.trim() ? 'failing' : 'never'
  return state.stale ? 'stale' : 'ok'
}

export function epgHealthLabel(h: EPGHealth): string {
  switch (h) {
    case 'off':
      return 'Not configured'
    case 'ok':
      return 'Up to date'
    case 'stale':
      return 'Stale'
    case 'never':
      return 'Never fetched yet'
    case 'failing':
      return 'Failing'
  }
}

/** SiliconDust refusing a download (too many recently for this tuner). */
const HDHR_REFUSED = /\bHTTP 403\b/

/** A source's last error in plain words ("" when there is none). */
export function epgErrorText(key: EPGSourceKey, raw: string): string {
  const err = raw.trim()
  if (!err) return ''
  if (key === 'hdhomerun' && HDHR_REFUSED.test(err)) {
    return (
      'SiliconDust refused this guide download (HTTP 403): it limits how often each HDHomeRun can download the guide. ' +
      'Bowtie waits about a day before trying again, as SiliconDust asks. Only one Bowtie server per tuner should download it.'
    )
  }
  return `The last guide download failed: ${err}`
}

/** The banner above the source cards, or null when all is well. */
export function epgSummaryBanner(status: EPGSourceStatus): string | null {
  const health = epgSources(status)
    .map((s) => epgHealth(s.state))
    .filter((h) => h !== 'off')
  if (health.length === 0) return null
  if (health.every((h) => h === 'never' || h === 'failing')) {
    return 'No guide data has been downloaded yet. The viewer guide stays empty until a download succeeds.'
  }
  if (health.includes('stale')) {
    return 'One or more EPG sources are stale. Viewer guide data may be incomplete.'
  }
  return null
}

/** Clamp a percent for signal bars; null/undefined → 0. */
export function signalPercent(v: number | undefined | null): number {
  if (v == null || !Number.isFinite(v)) return 0
  return Math.max(0, Math.min(100, Math.round(v)))
}

/** Idle tuner: no VCT number/name and zero-ish signal. */
export function isTunerIdle(t: {
  vctNumber?: string
  vctName?: string
  signalStrengthPercent?: number
}): boolean {
  const hasChannel = Boolean(t.vctNumber?.trim() || t.vctName?.trim())
  if (hasChannel) return false
  return signalPercent(t.signalStrengthPercent) === 0
}

export const QUALITY_OPTIONS: { value: string; label: string }[] = [
  { value: '', label: 'Unlimited' },
  { value: 'original', label: 'Original' },
  { value: 'high', label: 'High' },
  { value: 'medium', label: 'Medium' },
  { value: 'low', label: 'Low' },
]

export function qualityLabel(value: string): string {
  const found = QUALITY_OPTIONS.find((o) => o.value === value)
  return found ? found.label : value || 'Unlimited'
}

/** Per-account stream/tuner limits the admin can pick (0 = no limit). */
export const LIMIT_OPTIONS: { value: number; label: string }[] = Array.from({ length: 9 }, (_, n) => ({
  value: n,
  label: limitLabel(n),
}))

export function limitLabel(n: number): string {
  return n === 0 ? 'No limit' : String(n)
}
