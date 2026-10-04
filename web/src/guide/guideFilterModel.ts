import type { GuideProgram } from './guideModel'

/**
 * Guide category filters (All · Sports · Movies · News · Kids · New).
 *
 * The guide carries one raw category string per program (XMLTV <category>,
 * Schedules Direct genre, SiliconDust free guide). These rules map it to
 * coarse buckets; the same rules (and test vectors) live in BowtieKit
 * (GuideFilter.swift) and Android :core (GuideFilter.kt) — keep them in step.
 */
export type GuideBucket = 'sports' | 'movies' | 'news' | 'kids' | 'new'
export type GuideFilter = 'all' | GuideBucket

/** Chip order. */
export const GUIDE_FILTERS: readonly GuideFilter[] = ['all', 'sports', 'movies', 'news', 'kids', 'new']

/** Sport words / phrases matched as whole words ("Sports talk", "College football"). */
const SPORTS_PHRASES = [
  'sport', 'sports', 'motorsport', 'motorsports', 'esports',
  'football', 'basketball', 'baseball', 'soccer', 'hockey', 'golf', 'tennis',
  'boxing', 'wrestling', 'racing', 'volleyball', 'softball', 'lacrosse', 'rugby',
  'cricket', 'bowling', 'skiing', 'snowboarding', 'skating', 'gymnastics',
  'swimming', 'cycling', 'track field', 'athletics', 'olympics',
  'mixed martial arts', 'mma', 'rodeo', 'curling', 'billiards', 'darts',
  'surfing', 'triathlon', 'polo', 'handball', 'badminton', 'equestrian',
]

/** Whole category piece must equal one of these ("Movie review" is not a movie). */
const MOVIE_PIECES = [
  'movie', 'movies', 'film', 'films', 'feature film', 'tv movie', 'made for tv movie',
  'motion picture',
]

const NEWS_PHRASES = ['news', 'newsmagazine', 'newscast', 'weather']

/**
 * "Family" is deliberately absent: family sitcoms and dramas are
 * general-audience prime time, not children's programming.
 */
const KIDS_PHRASES = [
  'children', 'childrens', 'kids', 'animated', 'animation', 'cartoon', 'cartoons',
  'educational', 'preschool',
]

/** Ratings that keep a program out of Kids (adult animation). Letters/digits only. */
const MATURE_RATINGS = ['tv14', 'tvma', 'r', 'nc17', 'x']

/** Schedules Direct program IDs: MV… movies, SP… sports events. */
const SD_MOVIE = /^MV\d{8}/
const SD_SPORTS = /^SP\d{8}/

/** " word word " for whole-word phrase checks. */
function words(piece: string): string {
  const ws = piece.toLowerCase().split(/[^a-z0-9]+/).filter((w) => w !== '')
  return ws.length === 0 ? '' : ` ${ws.join(' ')} `
}

function hasAny(padded: string, phrases: readonly string[]): boolean {
  return phrases.some((p) => padded.includes(` ${p} `))
}

/** Buckets for one raw category string (no program ID / rating / isNew). */
function categoryBuckets(category: string): Set<GuideBucket> {
  const out = new Set<GuideBucket>()
  for (const piece of category.split(/[,;|]/)) {
    const w = words(piece)
    if (w === '') continue
    if (hasAny(w, SPORTS_PHRASES)) out.add('sports')
    if (MOVIE_PIECES.includes(w.trim())) out.add('movies')
    if (hasAny(w, NEWS_PHRASES)) out.add('news')
    if (hasAny(w, KIDS_PHRASES)) out.add('kids')
  }
  return out
}

/** Every bucket a program belongs to (may be several, or none). */
export function programBuckets(
  program: Pick<GuideProgram, 'category' | 'isNew' | 'rating' | 'programId'>,
): Set<GuideBucket> {
  const out = categoryBuckets(program.category ?? '')
  const pid = program.programId ?? ''
  if (SD_MOVIE.test(pid)) out.add('movies')
  if (SD_SPORTS.test(pid)) out.add('sports')
  const rating = (program.rating ?? '').toLowerCase().replace(/[^a-z0-9]/g, '')
  if (MATURE_RATINGS.includes(rating)) out.delete('kids')
  if (program.isNew === true) out.add('new')
  return out
}

export function programMatchesFilter(
  program: Pick<GuideProgram, 'category' | 'isNew' | 'rating' | 'programId'>,
  filter: GuideFilter,
): boolean {
  return filter === 'all' || programBuckets(program).has(filter)
}

/**
 * Keep the channel under `filter`: some program overlapping
 * [windowStart, windowStop) matches. "All" keeps every channel.
 */
export function channelMatchesFilter(
  channel: { programs: GuideProgram[] },
  filter: GuideFilter,
  windowStart: Date,
  windowStop: Date,
): boolean {
  if (filter === 'all') return true
  const a = windowStart.getTime()
  const b = windowStop.getTime()
  return channel.programs.some(
    (p) => Date.parse(p.start) < b && Date.parse(p.stop) > a && programMatchesFilter(p, filter),
  )
}

const LABELS: Record<GuideFilter, string> = {
  all: 'All',
  sports: 'Sports',
  movies: 'Movies',
  news: 'News',
  kids: 'Kids',
  new: 'New',
}

const EMPTY_NOUN: Record<GuideBucket, string> = {
  sports: 'sports',
  movies: 'movies',
  news: 'news',
  kids: "kids' shows",
  new: 'new episodes',
}

export function filterLabel(filter: GuideFilter): string {
  return LABELS[filter]
}

/** "No sports on in this time window". */
export function filterEmptyCopy(filter: GuideBucket): string {
  return `No ${EMPTY_NOUN[filter]} on in this time window`
}

// ── Per-device memory ─────────────────────────────────────────────────────

export const GUIDE_FILTER_KEY = 'bowtie.guide.filter'

type KV = Pick<Storage, 'getItem' | 'setItem'>

export function parseGuideFilter(raw: string | null | undefined): GuideFilter {
  return GUIDE_FILTERS.find((f) => f === raw) ?? 'all'
}

function defaultStorage(): KV | null {
  try {
    return typeof window !== 'undefined' ? window.localStorage : null
  } catch {
    return null
  }
}

/** The chip this browser last picked ("all" when unknown or storage is blocked). */
export function loadGuideFilter(storage: KV | null = defaultStorage()): GuideFilter {
  if (!storage) return 'all'
  try {
    return parseGuideFilter(storage.getItem(GUIDE_FILTER_KEY))
  } catch {
    return 'all'
  }
}

export function saveGuideFilter(filter: GuideFilter, storage: KV | null = defaultStorage()): void {
  if (!storage) return
  try {
    storage.setItem(GUIDE_FILTER_KEY, filter)
  } catch {
    // Private mode / blocked storage: the chip just isn't remembered.
  }
}
