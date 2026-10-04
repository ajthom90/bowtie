import { describe, expect, it } from 'vitest'
import {
  AUDIO_PROFILE,
  EMPTY_MULTIVIEW,
  MAX_TILES,
  MUTED_PROFILE,
  addTile,
  isMultiviewPath,
  layoutFor,
  parseSavedChannels,
  removeTile,
  replaceTile,
  restoreTiles,
  serializeChannels,
  setAudio,
  tileIndexForKey,
  type MultiviewState,
  type TileChannel,
} from './multiviewModel'

const ch = (id: number): TileChannel => ({ channelId: id, guideNumber: `${id}.1`, name: `CH${id}` })

/** Adds channels with keys k1, k2, … */
function withTiles(...ids: number[]): MultiviewState {
  return ids.reduce((s, id) => addTile(s, ch(id), `k${id}`), EMPTY_MULTIVIEW)
}

describe('layoutFor', () => {
  it('shows one big empty slot with no tiles', () => {
    expect(layoutFor(0)).toEqual({ columns: 1, rows: 1, emptySlots: 1 })
  })
  it('fills the page with one tile', () => {
    expect(layoutFor(1)).toEqual({ columns: 1, rows: 1, emptySlots: 0 })
  })
  it('puts two tiles side by side', () => {
    expect(layoutFor(2)).toEqual({ columns: 2, rows: 1, emptySlots: 0 })
  })
  it('uses a 2×2 grid for three (one empty slot) and four', () => {
    expect(layoutFor(3)).toEqual({ columns: 2, rows: 2, emptySlots: 1 })
    expect(layoutFor(4)).toEqual({ columns: 2, rows: 2, emptySlots: 0 })
  })
})

describe('addTile', () => {
  it('gives the first tile audio and the full-quality profile', () => {
    const s = addTile(EMPTY_MULTIVIEW, ch(1), 'a')
    expect(s.tiles).toEqual([{ key: 'a', channel: ch(1), profile: AUDIO_PROFILE }])
    expect(s.audioKey).toBe('a')
  })
  it('keeps audio on the first tile and starts later tiles at low quality', () => {
    const s = addTile(addTile(EMPTY_MULTIVIEW, ch(1), 'a'), ch(2), 'b')
    expect(s.audioKey).toBe('a')
    expect(s.tiles[1]).toEqual({ key: 'b', channel: ch(2), profile: MUTED_PROFILE })
  })
  it(`stops at ${MAX_TILES} tiles`, () => {
    const s = withTiles(1, 2, 3, 4)
    expect(addTile(s, ch(5), 'k5')).toBe(s)
  })
  it('ignores a channel that is already on screen', () => {
    const s = withTiles(1)
    expect(addTile(s, ch(1), 'again')).toBe(s)
  })
})

describe('removeTile', () => {
  it('moves audio to the next tile when the audio tile goes', () => {
    const s = setAudio(withTiles(1, 2, 3), 'k2')
    const r = removeTile(s, 'k2')
    expect(r.tiles.map((t) => t.key)).toEqual(['k1', 'k3'])
    expect(r.audioKey).toBe('k3')
  })
  it('moves audio to the new last tile when the last tile had it', () => {
    const r = removeTile(setAudio(withTiles(1, 2, 3), 'k3'), 'k3')
    expect(r.audioKey).toBe('k2')
  })
  it('leaves audio alone when another tile goes', () => {
    const r = removeTile(withTiles(1, 2), 'k2')
    expect(r.audioKey).toBe('k1')
  })
  it('clears audio when the last tile goes', () => {
    expect(removeTile(withTiles(1), 'k1')).toEqual(EMPTY_MULTIVIEW)
  })
  it('ignores an unknown key', () => {
    const s = withTiles(1)
    expect(removeTile(s, 'nope')).toBe(s)
  })
})

describe('replaceTile', () => {
  it('swaps the channel in place with a new stream key', () => {
    const r = replaceTile(withTiles(1, 2), 'k2', ch(9), 'new')
    expect(r.tiles.map((t) => [t.key, t.channel.channelId])).toEqual([
      ['k1', 1],
      ['new', 9],
    ])
    expect(r.tiles[1].profile).toBe(MUTED_PROFILE)
  })
  it('keeps audio on a replaced audio tile at full quality', () => {
    const r = replaceTile(withTiles(1, 2), 'k1', ch(9), 'new')
    expect(r.audioKey).toBe('new')
    expect(r.tiles[0].profile).toBe(AUDIO_PROFILE)
  })
  it('ignores a channel already on another tile', () => {
    const s = withTiles(1, 2)
    expect(replaceTile(s, 'k1', ch(2), 'new')).toBe(s)
  })
  it('ignores an unknown key', () => {
    const s = withTiles(1)
    expect(replaceTile(s, 'nope', ch(9), 'new')).toBe(s)
  })
})

describe('setAudio', () => {
  it('moves audio without restarting (profiles unchanged)', () => {
    const s = withTiles(1, 2)
    const r = setAudio(s, 'k2')
    expect(r.audioKey).toBe('k2')
    expect(r.tiles).toBe(s.tiles)
  })
  it('ignores an unknown key', () => {
    const s = withTiles(1)
    expect(setAudio(s, 'nope')).toBe(s)
  })
})

describe('tileIndexForKey', () => {
  it('maps 1–4 to tiles that exist', () => {
    expect(tileIndexForKey('1', 3)).toBe(0)
    expect(tileIndexForKey('3', 3)).toBe(2)
  })
  it('ignores digits past the tile count and other keys', () => {
    expect(tileIndexForKey('4', 3)).toBeNull()
    expect(tileIndexForKey('5', 4)).toBeNull()
    expect(tileIndexForKey('0', 4)).toBeNull()
    expect(tileIndexForKey('a', 4)).toBeNull()
    expect(tileIndexForKey('Enter', 4)).toBeNull()
  })
})

describe('saved channel set', () => {
  it('round-trips', () => {
    const list = [ch(1), ch(2)]
    expect(parseSavedChannels(serializeChannels(list))).toEqual(list)
  })
  it('is empty for missing or bad JSON', () => {
    expect(parseSavedChannels(null)).toEqual([])
    expect(parseSavedChannels('')).toEqual([])
    expect(parseSavedChannels('{not json')).toEqual([])
    expect(parseSavedChannels('{"channelId":1}')).toEqual([])
  })
  it('drops malformed entries and duplicates, and keeps at most four', () => {
    const raw = JSON.stringify([
      ch(1),
      { channelId: 'x', guideNumber: '2.1', name: 'B' },
      null,
      { channelId: 3 },
      ch(1),
      ch(4),
      ch(5),
      ch(6),
      ch(7),
    ])
    expect(parseSavedChannels(raw).map((c) => c.channelId)).toEqual([1, 4, 5, 6])
  })
  it('stores only channel fields', () => {
    const raw = serializeChannels(withTiles(1).tiles.map((t) => t.channel))
    expect(JSON.parse(raw)).toEqual([{ channelId: 1, guideNumber: '1.1', name: 'CH1' }])
  })
})

describe('restoreTiles', () => {
  it('starts the first channel with audio and the rest at low quality', () => {
    let n = 0
    const s = restoreTiles([ch(1), ch(2)], () => `r${++n}`)
    expect(s.tiles.map((t) => [t.key, t.profile])).toEqual([
      ['r1', AUDIO_PROFILE],
      ['r2', MUTED_PROFILE],
    ])
    expect(s.audioKey).toBe('r1')
  })
})

describe('isMultiviewPath', () => {
  it('matches /multiview with or without a trailing slash', () => {
    expect(isMultiviewPath('/multiview')).toBe(true)
    expect(isMultiviewPath('/multiview/')).toBe(true)
    expect(isMultiviewPath('/')).toBe(false)
    expect(isMultiviewPath('/multiviewer')).toBe(false)
  })
})
