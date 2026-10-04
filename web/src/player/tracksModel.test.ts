import { describe, expect, it } from 'vitest'
import { audioTrackLabel, loadTrackPrefs, pickAudioIndex, saveTrackPrefs } from './tracksModel'

function memStorage(): Storage {
  const m = new Map<string, string>()
  return {
    get length() { return m.size },
    clear: () => m.clear(),
    getItem: (k) => m.get(k) ?? null,
    key: (i) => [...m.keys()][i] ?? null,
    removeItem: (k) => void m.delete(k),
    setItem: (k, v) => void m.set(k, v),
  }
}

describe('tracksModel', () => {
  it('picks the preferred language by primary subtag', () => {
    const tracks = [{ lang: 'en' }, { lang: 'es' }]
    expect(pickAudioIndex(tracks, 'es')).toBe(1)
    expect(pickAudioIndex(tracks, 'es-MX')).toBe(1)
    expect(pickAudioIndex(tracks, 'fr')).toBe(-1)
    expect(pickAudioIndex(tracks, null)).toBe(-1)
  })
  it('labels tracks by name, then language, then number', () => {
    expect(audioTrackLabel({ name: 'Español', lang: 'es' }, 1)).toBe('Español')
    expect(audioTrackLabel({ lang: 'es' }, 1)).toBe('es')
    expect(audioTrackLabel({}, 1)).toBe('Audio 2')
  })
  it('round-trips prefs and survives broken storage', () => {
    const s = memStorage()
    expect(loadTrackPrefs(s)).toEqual({ audioLang: null, captions: null })
    saveTrackPrefs({ audioLang: 'es', captions: true }, s)
    expect(loadTrackPrefs(s)).toEqual({ audioLang: 'es', captions: true })
    s.setItem('bowtie.trackPrefs', '{bad json')
    expect(loadTrackPrefs(s)).toEqual({ audioLang: null, captions: null })
    const throwing = { getItem: () => { throw new Error('denied') }, setItem: () => { throw new Error('denied') } } as unknown as Storage
    expect(loadTrackPrefs(throwing)).toEqual({ audioLang: null, captions: null })
    expect(() => saveTrackPrefs({ audioLang: 'en', captions: false }, throwing)).not.toThrow()
  })
})
