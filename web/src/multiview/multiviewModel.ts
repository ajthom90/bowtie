/** Pure state for the Multiview page: tiles, audio focus, layout, saved set. */

export const MAX_TILES = 4

/** Quality the audio tile starts at (same default as the Player). */
export const AUDIO_PROFILE = 'original'
/** Quality muted tiles start at; tiles are not restarted when audio moves. */
export const MUTED_PROFILE = 'low'

export const SAVED_KEY = 'bowtie.multiview'

export const MULTIVIEW_PATH = '/multiview'

export function isMultiviewPath(path: string): boolean {
  return path.replace(/\/+$/, '') === MULTIVIEW_PATH
}

export type TileChannel = {
  channelId: number
  guideNumber: string
  name: string
}

/** One stream on screen. A new key means a new stream (and a remount). */
export type Tile = {
  key: string
  channel: TileChannel
  profile: string
}

export type MultiviewState = {
  tiles: Tile[]
  /** The tile playing sound (null only when there are no tiles). */
  audioKey: string | null
}

export const EMPTY_MULTIVIEW: MultiviewState = { tiles: [], audioKey: null }

export type Layout = {
  columns: number
  rows: number
  /** "Add channel" cells after the tiles. */
  emptySlots: number
}

/** 1 = full, 2 = side by side, 3–4 = 2×2; no tiles = one big Add slot. */
export function layoutFor(count: number): Layout {
  if (count <= 0) return { columns: 1, rows: 1, emptySlots: 1 }
  if (count === 1) return { columns: 1, rows: 1, emptySlots: 0 }
  if (count === 2) return { columns: 2, rows: 1, emptySlots: 0 }
  return { columns: 2, rows: 2, emptySlots: Math.max(0, MAX_TILES - count) }
}

function onScreen(state: MultiviewState, channelId: number, exceptKey?: string): boolean {
  return state.tiles.some((t) => t.channel.channelId === channelId && t.key !== exceptKey)
}

/** Adds a tile (max 4, one per channel). The first tile gets audio. */
export function addTile(state: MultiviewState, channel: TileChannel, key: string): MultiviewState {
  if (state.tiles.length >= MAX_TILES || onScreen(state, channel.channelId)) return state
  const audioKey = state.audioKey ?? key
  const profile = audioKey === key ? AUDIO_PROFILE : MUTED_PROFILE
  return { tiles: [...state.tiles, { key, channel, profile }], audioKey }
}

/** Removes a tile; its audio moves to the tile that takes its place (or the new last). */
export function removeTile(state: MultiviewState, key: string): MultiviewState {
  const i = state.tiles.findIndex((t) => t.key === key)
  if (i < 0) return state
  const tiles = state.tiles.filter((t) => t.key !== key)
  if (tiles.length === 0) return EMPTY_MULTIVIEW
  const audioKey = state.audioKey === key ? tiles[Math.min(i, tiles.length - 1)].key : state.audioKey
  return { tiles, audioKey }
}

/** Puts another channel on a tile (a new stream); audio stays with the tile. */
export function replaceTile(
  state: MultiviewState,
  key: string,
  channel: TileChannel,
  newKey: string,
): MultiviewState {
  const i = state.tiles.findIndex((t) => t.key === key)
  if (i < 0 || onScreen(state, channel.channelId, key)) return state
  const hadAudio = state.audioKey === key
  const tiles = state.tiles.slice()
  tiles[i] = { key: newKey, channel, profile: hadAudio ? AUDIO_PROFILE : MUTED_PROFILE }
  return { tiles, audioKey: hadAudio ? newKey : state.audioKey }
}

export function setAudio(state: MultiviewState, key: string): MultiviewState {
  if (state.audioKey === key || !state.tiles.some((t) => t.key === key)) return state
  return { ...state, audioKey: key }
}

/** Keys 1–4 pick a tile by position; null for anything else. */
export function tileIndexForKey(key: string, count: number): number | null {
  if (!/^[1-4]$/.test(key)) return null
  const i = Number(key) - 1
  return i < count ? i : null
}

/** Tiles for a saved set: the first has audio. */
export function restoreTiles(channels: TileChannel[], nextKey: () => string): MultiviewState {
  return channels.reduce((s, c) => addTile(s, c, nextKey()), EMPTY_MULTIVIEW)
}

export function serializeChannels(channels: TileChannel[]): string {
  return JSON.stringify(
    channels.map(({ channelId, guideNumber, name }) => ({ channelId, guideNumber, name })),
  )
}

function isTileChannel(v: unknown): v is TileChannel {
  if (typeof v !== 'object' || v === null) return false
  const c = v as Record<string, unknown>
  return (
    typeof c.channelId === 'number' &&
    Number.isFinite(c.channelId) &&
    typeof c.guideNumber === 'string' &&
    typeof c.name === 'string'
  )
}

/** The saved set from localStorage; anything unreadable is an empty set. */
export function parseSavedChannels(raw: string | null): TileChannel[] {
  if (!raw) return []
  let data: unknown
  try {
    data = JSON.parse(raw)
  } catch {
    return []
  }
  if (!Array.isArray(data)) return []
  const out: TileChannel[] = []
  for (const v of data) {
    if (out.length >= MAX_TILES) break
    if (!isTileChannel(v) || out.some((c) => c.channelId === v.channelId)) continue
    out.push({ channelId: v.channelId, guideNumber: v.guideNumber, name: v.name })
  }
  return out
}

export function loadSavedChannels(): TileChannel[] {
  try {
    return parseSavedChannels(localStorage.getItem(SAVED_KEY))
  } catch {
    return []
  }
}

export function saveChannels(channels: TileChannel[]): void {
  try {
    localStorage.setItem(SAVED_KEY, serializeChannels(channels))
  } catch {
    // storage full or blocked
  }
}
