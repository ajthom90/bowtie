import { describe, expect, it } from 'vitest'
import { signalStatsLine, weakSignalNote } from './signalModel'

const weak = { strength: 96, quality: 46, symbolQuality: 0, weak: true }
const fine = { strength: 96, quality: 90, symbolQuality: 100, weak: false }

describe('weakSignalNote', () => {
  it('shows the note, with the quality percent, when the server says the signal is weak', () => {
    expect(weakSignalNote(weak)).toBe('Weak signal (46%) — the picture may break up.')
  })

  it('hides it when the signal is fine or unknown', () => {
    expect(weakSignalNote(fine)).toBeNull()
    expect(weakSignalNote(null)).toBeNull()
    expect(weakSignalNote(undefined)).toBeNull()
  })
})

describe('signalStatsLine', () => {
  it('leads with quality, then strength and error-free (symbol quality)', () => {
    expect(signalStatsLine(weak)).toBe('Signal quality 46% · strength 96% · error-free 0%')
    expect(signalStatsLine(fine)).toBe('Signal quality 90% · strength 96% · error-free 100%')
  })

  it('rounds and clamps odd readings', () => {
    expect(signalStatsLine({ strength: 120, quality: 45.6, symbolQuality: -3, weak: false })).toBe(
      'Signal quality 46% · strength 100% · error-free 0%',
    )
  })

  it('is null when the signal is unknown', () => {
    expect(signalStatsLine(null)).toBeNull()
    expect(signalStatsLine(undefined)).toBeNull()
  })
})
