import { describe, expect, it } from 'vitest'
import { isPlayerRoute, parseRoute, pathFor, type Route } from './routes'

describe('parseRoute', () => {
  it('maps the root to the guide', () => {
    expect(parseRoute('/')).toEqual({ view: 'guide' })
    expect(parseRoute('')).toEqual({ view: 'guide' })
  })

  it('sends unknown paths to the guide', () => {
    expect(parseRoute('/nope')).toEqual({ view: 'guide' })
    expect(parseRoute('/guide/extra')).toEqual({ view: 'guide' })
    expect(parseRoute('/account/x')).toEqual({ view: 'guide' })
  })

  it('reads Recordings and its tabs (unknown tab = default)', () => {
    expect(parseRoute('/recordings')).toEqual({ view: 'recordings', tab: 'upcoming' })
    expect(parseRoute('/recordings/recorded')).toEqual({ view: 'recordings', tab: 'recorded' })
    expect(parseRoute('/recordings/missed')).toEqual({ view: 'recordings', tab: 'missed' })
    expect(parseRoute('/recordings/shows')).toEqual({ view: 'recordings', tab: 'shows' })
    expect(parseRoute('/recordings/bogus')).toEqual({ view: 'recordings', tab: 'upcoming' })
  })

  it('reads Admin and its tabs (unknown tab = default)', () => {
    expect(parseRoute('/admin')).toEqual({ view: 'admin', tab: 'tuners' })
    expect(parseRoute('/admin/epg')).toEqual({ view: 'admin', tab: 'epg' })
    expect(parseRoute('/admin/sessions')).toEqual({ view: 'admin', tab: 'sessions' })
    expect(parseRoute('/admin/bogus')).toEqual({ view: 'admin', tab: 'tuners' })
  })

  it('reads Account, Link and Multiview', () => {
    expect(parseRoute('/account')).toEqual({ view: 'account' })
    expect(parseRoute('/link')).toEqual({ view: 'link' })
    expect(parseRoute('/multiview')).toEqual({ view: 'multiview' })
  })

  it('ignores trailing and doubled slashes', () => {
    expect(parseRoute('/recordings/')).toEqual({ view: 'recordings', tab: 'upcoming' })
    expect(parseRoute('/admin/epg/')).toEqual({ view: 'admin', tab: 'epg' })
    expect(parseRoute('/account//')).toEqual({ view: 'account' })
    expect(parseRoute('/multiview/')).toEqual({ view: 'multiview' })
    expect(parseRoute('//recordings//shows')).toEqual({ view: 'recordings', tab: 'shows' })
  })

  it('reads player paths with numeric ids only', () => {
    expect(parseRoute('/watch/12')).toEqual({ view: 'watch', channelId: 12 })
    expect(parseRoute('/watch/12/')).toEqual({ view: 'watch', channelId: 12 })
    expect(parseRoute('/recordings/play/7')).toEqual({ view: 'playRecording', recordingId: 7 })
    expect(parseRoute('/watch/abc')).toEqual({ view: 'guide' })
    expect(parseRoute('/watch')).toEqual({ view: 'guide' })
    expect(parseRoute('/watch/1.5')).toEqual({ view: 'guide' })
    expect(parseRoute('/recordings/play/x')).toEqual({ view: 'recordings', tab: 'upcoming' })
    expect(parseRoute('/recordings/play')).toEqual({ view: 'recordings', tab: 'upcoming' })
  })
})

describe('pathFor', () => {
  it('formats each view', () => {
    expect(pathFor({ view: 'guide' })).toBe('/')
    expect(pathFor({ view: 'recordings', tab: 'upcoming' })).toBe('/recordings')
    expect(pathFor({ view: 'recordings', tab: 'missed' })).toBe('/recordings/missed')
    expect(pathFor({ view: 'admin', tab: 'tuners' })).toBe('/admin')
    expect(pathFor({ view: 'admin', tab: 'epg' })).toBe('/admin/epg')
    expect(pathFor({ view: 'account' })).toBe('/account')
    expect(pathFor({ view: 'link' })).toBe('/link')
    expect(pathFor({ view: 'multiview' })).toBe('/multiview')
    expect(pathFor({ view: 'watch', channelId: 3 })).toBe('/watch/3')
    expect(pathFor({ view: 'playRecording', recordingId: 44 })).toBe('/recordings/play/44')
  })

  it('round-trips through parseRoute', () => {
    const routes: Route[] = [
      { view: 'guide' },
      { view: 'recordings', tab: 'upcoming' },
      { view: 'recordings', tab: 'recorded' },
      { view: 'recordings', tab: 'missed' },
      { view: 'recordings', tab: 'shows' },
      { view: 'admin', tab: 'tuners' },
      { view: 'admin', tab: 'channels' },
      { view: 'admin', tab: 'epg' },
      { view: 'admin', tab: 'recordings' },
      { view: 'admin', tab: 'settings' },
      { view: 'admin', tab: 'users' },
      { view: 'admin', tab: 'sessions' },
      { view: 'account' },
      { view: 'link' },
      { view: 'multiview' },
      { view: 'watch', channelId: 9 },
      { view: 'playRecording', recordingId: 1 },
    ]
    for (const r of routes) expect(parseRoute(pathFor(r))).toEqual(r)
  })
})

describe('isPlayerRoute', () => {
  it('is true only for the live and recording players', () => {
    expect(isPlayerRoute({ view: 'watch', channelId: 1 })).toBe(true)
    expect(isPlayerRoute({ view: 'playRecording', recordingId: 1 })).toBe(true)
    expect(isPlayerRoute({ view: 'guide' })).toBe(false)
    expect(isPlayerRoute({ view: 'recordings', tab: 'upcoming' })).toBe(false)
  })
})
