import { describe, expect, it } from 'vitest'
import { FEED_APPS, FEED_COPY, feedButtons } from './feedModel'

describe('FEED_APPS', () => {
  it('covers Kodi, VLC, TiviMate, Plex and Jellyfin in that order', () => {
    expect(FEED_APPS.map((a) => a.name)).toEqual(['Kodi', 'VLC', 'TiviMate', 'Plex', 'Jellyfin'])
  })

  it('gives every app at least one step', () => {
    for (const app of FEED_APPS) expect(app.steps.length).toBeGreaterThan(0)
  })

  it('points Kodi at IPTV Simple Client and mentions Xbox', () => {
    const kodi = FEED_APPS[0]
    expect(kodi.note).toContain('Xbox')
    expect(kodi.steps.join(' ')).toContain('IPTV Simple Client')
  })

  it('tells Plex users it needs a bridge for M3U', () => {
    const plex = FEED_APPS.find((a) => a.name === 'Plex')!
    expect(plex.steps.join(' ')).toContain('Threadfin')
  })
})

describe('feedButtons', () => {
  it('offers Create link before a link is shown', () => {
    expect(feedButtons(false)).toEqual({ create: 'Create link', rotate: false })
  })
  it('offers New link once a link is shown', () => {
    expect(feedButtons(true)).toEqual({ create: null, rotate: true })
  })
})

describe('FEED_COPY', () => {
  it('warns that the link is a password', () => {
    expect(FEED_COPY.warning).toContain('Anyone with these links can watch as you')
  })
})
