export type TrackPrefs = { audioLang: string | null; captions: boolean | null }

const KEY = 'bowtie.trackPrefs'
const EMPTY: TrackPrefs = { audioLang: null, captions: null }

function defaultStorage(): Storage | undefined {
  try {
    return window.localStorage
  } catch {
    return undefined
  }
}

export function loadTrackPrefs(storage: Storage | undefined = defaultStorage()): TrackPrefs {
  try {
    const raw = storage?.getItem(KEY)
    if (!raw) return { ...EMPTY }
    const p = JSON.parse(raw) as Partial<TrackPrefs>
    return {
      audioLang: typeof p.audioLang === 'string' ? p.audioLang : null,
      captions: typeof p.captions === 'boolean' ? p.captions : null,
    }
  } catch {
    return { ...EMPTY }
  }
}

export function saveTrackPrefs(p: TrackPrefs, storage: Storage | undefined = defaultStorage()): void {
  try {
    storage?.setItem(KEY, JSON.stringify(p))
  } catch {
    /* private mode / quota: the preference just isn't remembered */
  }
}

const primary = (tag: string) => tag.toLowerCase().split('-')[0]

/** Index of the track matching the preferred language, or -1 to keep the default. */
export function pickAudioIndex(tracks: { lang?: string }[], preferred: string | null): number {
  if (!preferred) return -1
  const want = primary(preferred)
  return tracks.findIndex((t) => t.lang !== undefined && primary(t.lang) === want)
}

export function audioTrackLabel(t: { name?: string; lang?: string }, i: number): string {
  return t.name || t.lang || `Audio ${i + 1}`
}
