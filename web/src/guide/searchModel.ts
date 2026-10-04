import type { GuideSearchResult } from '../api/client'
import { guideMarkText } from '../recordings/recordingsModel'

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

/** One line under the search box: progress, error, no matches, or a count. */
export function searchStatusText(s: {
  query: string | null
  loading: boolean
  error: string | null
  count: number
}): string {
  if (!s.query) return ''
  if (s.loading) return 'Searching…'
  if (s.error) return s.error
  if (s.count === 0) return `No upcoming programs match “${s.query}”.`
  return `${s.count} ${s.count === 1 ? 'program' : 'programs'}`
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
