import { afterEach, describe, expect, it, vi } from 'vitest'
import { canPlayNativeHls, detectCaps } from './caps'

describe('detectCaps', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
  })

  it('always includes h264 and aac', () => {
    vi.stubGlobal('MediaSource', {
      isTypeSupported: () => false,
    })
    mockVideoCanPlay(() => '')

    const caps = detectCaps()
    expect(caps.videoCodecs).toEqual(['h264'])
    expect(caps.audioCodecs).toEqual(['aac'])
    expect(caps.profile).toBe('')
  })

  it('adds hevc when MediaSource supports hvc1', () => {
    vi.stubGlobal('MediaSource', {
      isTypeSupported: (type: string) => type.includes('hvc1'),
    })
    mockVideoCanPlay(() => '')

    const caps = detectCaps('high')
    expect(caps.videoCodecs).toContain('h264')
    expect(caps.videoCodecs).toContain('hevc')
    expect(caps.profile).toBe('high')
  })

  it('adds hevc via Safari canPlayType when MSE lacks it', () => {
    vi.stubGlobal('MediaSource', {
      isTypeSupported: () => false,
    })
    mockVideoCanPlay((type) => (type.includes('hvc1') ? 'probably' : ''))

    const caps = detectCaps()
    expect(caps.videoCodecs).toContain('hevc')
  })

  it('adds ac3 when MediaSource supports ac-3', () => {
    vi.stubGlobal('MediaSource', {
      isTypeSupported: (type: string) => type.includes('ac-3'),
    })
    mockVideoCanPlay(() => '')

    const caps = detectCaps()
    expect(caps.audioCodecs).toContain('aac')
    expect(caps.audioCodecs).toContain('ac3')
  })

  it('sets maxHeight from screen dimensions', () => {
    vi.stubGlobal('MediaSource', { isTypeSupported: () => false })
    Object.defineProperty(window, 'screen', {
      configurable: true,
      value: { height: 1080 },
    })
    Object.defineProperty(window, 'devicePixelRatio', {
      configurable: true,
      value: 2,
    })
    mockVideoCanPlay(() => '')

    const caps = detectCaps()
    expect(caps.maxHeight).toBe(2160)
  })
})

describe('canPlayNativeHls', () => {
  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('returns true when video can play apple mpegurl', () => {
    mockVideoCanPlay((type) =>
      type === 'application/vnd.apple.mpegurl' ? 'maybe' : '',
    )
    expect(canPlayNativeHls()).toBe(true)
  })

  it('returns false when unsupported', () => {
    mockVideoCanPlay(() => '')
    expect(canPlayNativeHls()).toBe(false)
  })
})

function mockVideoCanPlay(fn: (type: string) => CanPlayTypeResult) {
  const createEl = document.createElement.bind(document)
  vi.spyOn(document, 'createElement').mockImplementation((tag: string) => {
    const el = createEl(tag)
    if (tag === 'video') {
      ;(el as HTMLVideoElement).canPlayType = fn
    }
    return el
  })
}
