import { describe, expect, it } from 'vitest'
import {
  GUIDE_FILTERS,
  channelMatchesFilter,
  filterEmptyCopy,
  filterLabel,
  loadGuideFilter,
  parseGuideFilter,
  programBuckets,
  programMatchesFilter,
  saveGuideFilter,
  type GuideBucket,
} from './guideFilterModel'
import type { GuideProgram } from './guideModel'

function prog(over: Partial<GuideProgram> = {}): GuideProgram {
  return {
    start: '2026-10-04T18:00:00Z',
    stop: '2026-10-04T19:00:00Z',
    title: 'Show',
    subtitle: '',
    description: '',
    category: '',
    ...over,
  }
}

function buckets(category: string, over: Partial<GuideProgram> = {}): GuideBucket[] {
  return [...programBuckets(prog({ category, ...over }))].sort()
}

describe('programBuckets — category mapping', () => {
  it.each([
    'Sports',
    'Sports event',
    'Sports non-event',
    'Sports talk',
    'SPORTS EVENT',
    'Football',
    'College football',
    'Basketball',
    'Baseball',
    'Soccer',
    'Hockey',
    'Golf',
    'Tennis',
    'Boxing',
    'Pro wrestling',
    'Auto racing',
    'Motorsports',
    'Figure skating',
    'Track/field',
    'Olympics',
    'Mixed martial arts',
  ])('%s → sports', (c) => {
    expect(buckets(c)).toEqual(['sports'])
  })

  it.each(['Movie', 'Movies', 'movie', 'Feature Film', 'Film', 'TV Movie', 'Made-for-TV movie'])(
    '%s → movies',
    (c) => {
      expect(buckets(c)).toEqual(['movies'])
    },
  )

  it.each(['News', 'Newsmagazine', 'News magazine', 'Weather', 'Local news', 'Newscast'])(
    '%s → news',
    (c) => {
      expect(buckets(c)).toEqual(['news'])
    },
  )

  it.each([
    'Children',
    "Children's",
    'Children-music',
    'Children-special',
    'Kids',
    'Animated',
    'Animation',
    'Cartoon',
    'Educational',
  ])('%s → kids', (c) => {
    expect(buckets(c)).toEqual(['kids'])
  })

  it.each([
    '',
    'Family',
    'Drama',
    'Sitcom',
    'Comedy',
    'Movie review',
    'Martial arts',
    'Transportation',
    'Talk',
    'Series',
    'Reality',
  ])('%s → no bucket', (c) => {
    expect(buckets(c)).toEqual([])
  })

  it('trims and ignores case', () => {
    expect(buckets('  sPoRtS eVeNt  ')).toEqual(['sports'])
  })

  it('a joined category string can land in several buckets', () => {
    expect(buckets('Sports talk; News')).toEqual(['news', 'sports'])
    expect(buckets('Children, Animated')).toEqual(['kids'])
    expect(buckets('Movie | Animated')).toEqual(['kids', 'movies'])
  })

  it('Schedules Direct program ID prefixes mark movies and sports events', () => {
    expect(buckets('Action', { programId: 'MV000111220000' })).toEqual(['movies'])
    expect(buckets('', { programId: 'SP012345670123' })).toEqual(['sports'])
    expect(buckets('Drama', { programId: 'EP012345670012' })).toEqual([])
    expect(buckets('', { programId: 'MVP' })).toEqual([])
  })

  it('a kids movie is in both buckets', () => {
    expect(buckets('Children', { programId: 'MV000111220000' })).toEqual(['kids', 'movies'])
  })

  it('isNew adds the new bucket', () => {
    expect(buckets('Sitcom', { isNew: true })).toEqual(['new'])
    expect(buckets('Sports event', { isNew: true })).toEqual(['new', 'sports'])
    expect(buckets('Sitcom', { isNew: false })).toEqual([])
  })

  it('mature ratings keep a program out of kids (adult animation)', () => {
    expect(buckets('Animated', { rating: 'TV-14' })).toEqual([])
    expect(buckets('Animated', { rating: 'TV-MA' })).toEqual([])
    expect(buckets('Animated', { rating: 'R' })).toEqual([])
    expect(buckets('Animated', { rating: 'TV-PG' })).toEqual(['kids'])
    expect(buckets('Children', { rating: 'TV-Y7' })).toEqual(['kids'])
  })
})

describe('programMatchesFilter', () => {
  it('all matches everything, including uncategorized programs', () => {
    expect(programMatchesFilter(prog(), 'all')).toBe(true)
  })

  it('a bucket matches only its programs', () => {
    expect(programMatchesFilter(prog({ category: 'Football' }), 'sports')).toBe(true)
    expect(programMatchesFilter(prog({ category: 'Football' }), 'movies')).toBe(false)
    expect(programMatchesFilter(prog({ isNew: true }), 'new')).toBe(true)
  })
})

describe('channelMatchesFilter', () => {
  const start = new Date('2026-10-04T18:00:00Z')
  const stop = new Date('2026-10-04T22:00:00Z')

  it('all keeps every channel, even without guide data', () => {
    expect(channelMatchesFilter({ programs: [] }, 'all', start, stop)).toBe(true)
  })

  it('hides channels without guide data under a filter', () => {
    expect(channelMatchesFilter({ programs: [] }, 'sports', start, stop)).toBe(false)
  })

  it('keeps a channel with a matching program in the window', () => {
    const programs = [
      prog({ category: 'News' }),
      prog({ start: '2026-10-04T20:00:00Z', stop: '2026-10-04T23:00:00Z', category: 'Football' }),
    ]
    expect(channelMatchesFilter({ programs }, 'sports', start, stop)).toBe(true)
    expect(channelMatchesFilter({ programs }, 'news', start, stop)).toBe(true)
    expect(channelMatchesFilter({ programs }, 'movies', start, stop)).toBe(false)
  })

  it('ignores matches outside the window (stop is exclusive)', () => {
    const before = prog({ start: '2026-10-04T16:00:00Z', stop: '2026-10-04T18:00:00Z', category: 'Golf' })
    const after = prog({ start: '2026-10-04T22:00:00Z', stop: '2026-10-04T23:00:00Z', category: 'Golf' })
    expect(channelMatchesFilter({ programs: [before, after] }, 'sports', start, stop)).toBe(false)
  })

  it('counts a match that started before the window and is still on', () => {
    const running = prog({ start: '2026-10-04T17:00:00Z', stop: '2026-10-04T18:30:00Z', category: 'Golf' })
    expect(channelMatchesFilter({ programs: [running] }, 'sports', start, stop)).toBe(true)
  })
})

describe('filter labels and copy', () => {
  it('chips are All, Sports, Movies, News, Kids, New in order', () => {
    expect(GUIDE_FILTERS.map(filterLabel)).toEqual(['All', 'Sports', 'Movies', 'News', 'Kids', 'New'])
  })

  it('empty copy names the bucket', () => {
    expect(filterEmptyCopy('sports')).toBe('No sports on in this time window')
    expect(filterEmptyCopy('movies')).toBe('No movies on in this time window')
    expect(filterEmptyCopy('news')).toBe('No news on in this time window')
    expect(filterEmptyCopy('kids')).toBe("No kids' shows on in this time window")
    expect(filterEmptyCopy('new')).toBe('No new episodes on in this time window')
  })
})

describe('persistence', () => {
  function memoryStorage(initial: Record<string, string> = {}) {
    const data = { ...initial }
    return {
      data,
      getItem: (k: string) => (k in data ? data[k] : null),
      setItem: (k: string, v: string) => {
        data[k] = v
      },
    }
  }

  it('parses known values and falls back to all', () => {
    expect(parseGuideFilter('sports')).toBe('sports')
    expect(parseGuideFilter('bogus')).toBe('all')
    expect(parseGuideFilter(null)).toBe('all')
  })

  it('round-trips through storage', () => {
    const s = memoryStorage()
    saveGuideFilter('kids', s)
    expect(loadGuideFilter(s)).toBe('kids')
  })

  it('survives storage that throws', () => {
    const broken = {
      getItem: () => {
        throw new Error('blocked')
      },
      setItem: () => {
        throw new Error('blocked')
      },
    }
    expect(loadGuideFilter(broken)).toBe('all')
    expect(() => saveGuideFilter('news', broken)).not.toThrow()
  })

  it('survives a missing storage', () => {
    expect(loadGuideFilter(null)).toBe('all')
    expect(() => saveGuideFilter('news', null)).not.toThrow()
  })
})
