import type { GuideSearchResult } from '../api/client'
import { guideMarkText } from '../recordings/recordingsModel'
import { compareGuideNumber } from './guideModel'

/** Wait after the last keystroke before searching. */
export const SEARCH_DEBOUNCE_MS = 300

/** Results asked of the server per search. */
export const SEARCH_LIMIT = 50

/** Trimmed, single-spaced query, or null when too short to search. */
export function normalizeQuery(raw: string): string | null {
  const q = raw.trim().replace(/\s+/g, ' ')
  return q.length >= 2 ? q : null
}

/** On air at `now`: start ≤ now < stop. */
export function isOnNow(p: { start: string; stop: string }, now: Date): boolean {
  const n = now.getTime()
  return n >= Date.parse(p.start) && n < Date.parse(p.stop)
}

export type ResultActions = {
  /** Watch the channel (the program is on now). */
  watch: boolean
  /** Record this airing (not already scheduled or recorded). */
  record: boolean
  /** Record every new episode (program hasn't ended). */
  series: boolean
  /** The existing recording's state in words ("" for none). */
  markText: string
}

/** What a search result offers. Results never include ended programs. */
export function resultActions(r: GuideSearchResult, now: Date): ResultActions {
  const notEnded = Date.parse(r.stop) > now.getTime()
  const markText = r.recording ? guideMarkText(r.recording.state) : ''
  return {
    watch: isOnNow(r, now),
    record: notEnded && !r.recording,
    series: notEnded,
    markText,
  }
}

/** Lock badge for a program blocked by parental controls. */
export function lockText(rating: string | undefined): string {
  return `🔒 ${rating?.trim() || 'Not rated'}`
}

/** Channel hits shown above the program results. */
export const CHANNEL_MATCH_LIMIT = 5

type ChannelLike = { channelId: number; guideNumber: string; name: string }

/**
 * Channels whose name contains the query, or whose number starts with it
 * ("FOX", "9.1"), exact number first, then in channel order.
 */
export function matchChannels<T extends ChannelLike>(
  channels: readonly T[],
  query: string,
  limit: number = CHANNEL_MATCH_LIMIT,
): T[] {
  const q = query.trim().toLowerCase()
  if (!q) return []
  const hits = channels.filter(
    (c) => c.guideNumber.toLowerCase().startsWith(q) || c.name.toLowerCase().includes(q),
  )
  const exact = (c: T) => (c.guideNumber.toLowerCase() === q ? 0 : 1)
  return hits
    .sort((a, b) => exact(a) - exact(b) || compareGuideNumber(a.guideNumber, b.guideNumber))
    .slice(0, limit)
}

const plural = (n: number, one: string, many: string) => `${n} ${n === 1 ? one : many}`

/** One line under the search box: progress, error, no matches, or a count. */
export function searchStatusText(s: {
  query: string | null
  loading: boolean
  error: string | null
  count: number
  /** Channels matched by name or number (shown above programs). */
  channelCount?: number
}): string {
  if (!s.query) return ''
  if (s.loading) return 'Searching…'
  if (s.error) return s.error
  const channels = s.channelCount ?? 0
  if (s.count === 0 && channels === 0) return `No upcoming programs match “${s.query}”.`
  const parts: string[] = []
  if (channels > 0) parts.push(plural(channels, 'channel', 'channels'))
  if (s.count > 0) parts.push(plural(s.count, 'program', 'programs'))
  return parts.join(' · ')
}

/** Calls `fn` with the latest value once `waitMs` passes without another call. */
export function createDebouncer<T>(fn: (value: T) => void, waitMs: number = SEARCH_DEBOUNCE_MS) {
  let timer: ReturnType<typeof setTimeout> | null = null
  return {
    call(value: T) {
      if (timer != null) clearTimeout(timer)
      timer = setTimeout(() => {
        timer = null
        fn(value)
      }, waitMs)
    },
    cancel() {
      if (timer != null) clearTimeout(timer)
      timer = null
    },
  }
}
