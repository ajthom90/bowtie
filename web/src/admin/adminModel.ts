import type { AdminChannel } from '../api/client'

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
