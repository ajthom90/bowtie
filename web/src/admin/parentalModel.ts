import type { AdminChannel, User } from '../api/client'
import { compareGuideNumbers } from './adminModel'

/** Max rating choices (the server's ladder; movie ratings map onto it). */
export const RATING_OPTIONS: { value: string; label: string }[] = [
  { value: '', label: 'No limit' },
  { value: 'TV-Y', label: 'TV-Y' },
  { value: 'TV-Y7', label: 'TV-Y7' },
  { value: 'TV-G', label: 'TV-G' },
  { value: 'TV-PG', label: 'TV-PG' },
  { value: 'TV-14', label: 'TV-14' },
  { value: 'TV-MA', label: 'TV-MA' },
]

export function ratingLabel(value: string | undefined): string {
  const v = value ?? ''
  return RATING_OPTIONS.find((o) => o.value === v)?.label ?? v
}

/** "All channels", "No channels", "3 of 12 channels". */
export function channelsSummary(allowed: number[] | null | undefined, total: number): string {
  if (allowed == null) return 'All channels'
  const n = allowed.length
  if (n === 0) return 'No channels'
  if (total > 0) return `${n} of ${total} channels`
  return `${n} ${n === 1 ? 'channel' : 'channels'}`
}

/** Adds or removes one channel id; the result is sorted. */
export function toggleChannel(allowed: number[], id: number): number[] {
  const next = allowed.includes(id) ? allowed.filter((x) => x !== id) : [...allowed, id]
  return next.sort((a, b) => a - b)
}

/**
 * Channels offered in the picker: enabled ones, plus any disabled channel
 * that is already allowed (so it can be unticked), in guide-number order.
 */
export function pickerChannels(channels: AdminChannel[], allowed: number[] | null | undefined): AdminChannel[] {
  const keep = new Set(allowed ?? [])
  return channels
    .filter((c) => c.enabled || keep.has(c.id))
    .sort((a, b) => compareGuideNumbers(a.guideNumber, b.guideNumber))
}

/**
 * Ticked channels when the picker opens: the allowed list, or every listed
 * channel when all are allowed (so unticking "All channels" blocks nothing yet).
 */
export function initialSelection(allowed: number[] | null | undefined, listed: AdminChannel[]): number[] {
  const ids = allowed ?? listed.map((c) => c.id)
  return [...ids].sort((a, b) => a - b)
}

/** Admins are never restricted, so their parental controls are read-only. */
export function parentalEditable(user: Pick<User, 'role'>): boolean {
  return user.role !== 'admin'
}

export const RATINGS_NOTE =
  'Ratings come from the guide; the free HDHomeRun guide has no ratings, so use Schedules Direct to limit by rating.'
