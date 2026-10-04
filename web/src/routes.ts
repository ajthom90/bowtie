/**
 * Path ↔ view mapping for the app's main views (no router library).
 *
 *   /                      guide (also any unknown path)
 *   /recordings[/<tab>]    Recordings (upcoming | recorded | missed | shows)
 *   /admin[/<tab>]         Admin (tuners | channels | epg | …)
 *   /account               Account
 *   /link, /multiview      quick sign-in approval, Multiview
 *   /watch/<channelId>     live player (opened in-session only)
 *   /recordings/play/<id>  recording player (opened in-session only)
 */
import type { AdminTab } from './admin/Admin'
import type { RecordingsTab } from './recordings/recordingsModel'

export const RECORDINGS_TAB_IDS: readonly RecordingsTab[] = ['upcoming', 'recorded', 'missed', 'shows']
export const ADMIN_TAB_IDS: readonly AdminTab[] = [
  'tuners',
  'channels',
  'epg',
  'recordings',
  'settings',
  'users',
  'sessions',
]
const DEFAULT_RECORDINGS_TAB: RecordingsTab = 'upcoming'
const DEFAULT_ADMIN_TAB: AdminTab = 'tuners'

export type Route =
  | { view: 'guide' }
  | { view: 'recordings'; tab: RecordingsTab }
  | { view: 'admin'; tab: AdminTab }
  | { view: 'account' }
  | { view: 'link' }
  | { view: 'multiview' }
  | { view: 'watch'; channelId: number }
  | { view: 'playRecording'; recordingId: number }

const GUIDE: Route = { view: 'guide' }

function oneOf<T extends string>(ids: readonly T[], s: string | undefined, fallback: T): T {
  return ids.find((id) => id === s) ?? fallback
}

function idOf(s: string | undefined): number | null {
  return s !== undefined && /^\d+$/.test(s) ? Number(s) : null
}

export function parseRoute(pathname: string): Route {
  const parts = pathname.split('/').filter(Boolean)
  const [head, a, b] = parts
  switch (head) {
    case undefined:
      return GUIDE
    case 'recordings': {
      if (a === 'play' && parts.length === 3) {
        const id = idOf(b)
        if (id !== null) return { view: 'playRecording', recordingId: id }
      }
      if (parts.length > 2) break
      return { view: 'recordings', tab: oneOf(RECORDINGS_TAB_IDS, a, DEFAULT_RECORDINGS_TAB) }
    }
    case 'admin':
      if (parts.length > 2) break
      return { view: 'admin', tab: oneOf(ADMIN_TAB_IDS, a, DEFAULT_ADMIN_TAB) }
    case 'watch': {
      const id = parts.length === 2 ? idOf(a) : null
      return id === null ? GUIDE : { view: 'watch', channelId: id }
    }
    case 'account':
    case 'link':
    case 'multiview':
      return parts.length === 1 ? { view: head } : GUIDE
  }
  // Too-deep Recordings/Admin paths open the section on its default tab.
  if (head === 'recordings') return { view: 'recordings', tab: DEFAULT_RECORDINGS_TAB }
  if (head === 'admin') return { view: 'admin', tab: DEFAULT_ADMIN_TAB }
  return GUIDE
}

export function pathFor(route: Route): string {
  switch (route.view) {
    case 'guide':
      return '/'
    case 'recordings':
      return route.tab === DEFAULT_RECORDINGS_TAB ? '/recordings' : `/recordings/${route.tab}`
    case 'admin':
      return route.tab === DEFAULT_ADMIN_TAB ? '/admin' : `/admin/${route.tab}`
    case 'watch':
      return `/watch/${route.channelId}`
    case 'playRecording':
      return `/recordings/play/${route.recordingId}`
    default:
      return `/${route.view}`
  }
}

/** Player routes only show a player opened in this session (never on refresh). */
export function isPlayerRoute(route: Route): route is Extract<Route, { view: 'watch' | 'playRecording' }> {
  return route.view === 'watch' || route.view === 'playRecording'
}
