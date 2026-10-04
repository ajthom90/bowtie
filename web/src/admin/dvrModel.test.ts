import { describe, expect, it } from 'vitest'
import type { DVRStorage, SettingsDVR } from '../api/client'
import {
  DISK_FULL_WARNING,
  RECORDING_QUALITIES,
  buildPaddingPayload,
  bytesPerHour,
  formatBytes,
  gaugeSegments,
  minutesToSeconds,
  paddingToForm,
  qualityOptionLabel,
  qualitySizeHint,
  secondsToMinutes,
  storageWarning,
} from './dvrModel'

const GB = 1024 ** 3
const TB = 1024 ** 4

function storage(over: Partial<DVRStorage> = {}): DVRStorage {
  return {
    dir: '/recordings',
    usedBytes: 100 * GB,
    freeBytes: 300 * GB,
    totalBytes: 500 * GB,
    floorBytes: 2 * GB,
    minFreeBytes: 0,
    recordings: { ready: 4, scheduled: 2, recording: 1, failed: 0 },
    ...over,
  }
}

describe('formatBytes', () => {
  it('uses GB and TB with one decimal', () => {
    expect(formatBytes(1.25 * GB)).toBe('1.3 GB')
    expect(formatBytes(512 * GB)).toBe('512.0 GB')
    expect(formatBytes(1.5 * TB)).toBe('1.5 TB')
    expect(formatBytes(1024 * GB)).toBe('1.0 TB')
  })

  it('does not round small sizes to 0.0 GB', () => {
    expect(formatBytes(0)).toBe('0 GB')
    expect(formatBytes(300 * 1024 ** 2)).toBe('300 MB')
    expect(formatBytes(1000)).toBe('< 1 MB')
  })

  it('treats bad input as zero', () => {
    expect(formatBytes(-5)).toBe('0 GB')
    expect(formatBytes(Number.NaN)).toBe('0 GB')
  })
})

describe('gaugeSegments', () => {
  it('splits the disk into recordings, other and free', () => {
    const g = gaugeSegments(storage())
    expect(g.recordingsBytes).toBe(100 * GB)
    expect(g.otherBytes).toBe(100 * GB)
    expect(g.freeBytes).toBe(300 * GB)
    expect(g.recordingsPct).toBeCloseTo(20)
    expect(g.otherPct).toBeCloseTo(20)
    expect(g.freePct).toBeCloseTo(60)
  })

  it('clamps inconsistent numbers so the bar never overflows', () => {
    const g = gaugeSegments(storage({ usedBytes: 400 * GB }))
    expect(g.recordingsBytes).toBe(200 * GB)
    expect(g.otherBytes).toBe(0)
    expect(g.recordingsPct + g.otherPct + g.freePct).toBeCloseTo(100)
    const over = gaugeSegments(storage({ freeBytes: 900 * GB }))
    expect(over.freePct).toBe(100)
    expect(over.recordingsPct).toBe(0)
  })

  it('handles an unknown disk size without NaN', () => {
    const g = gaugeSegments(storage({ totalBytes: 0, freeBytes: 0, usedBytes: 0 }))
    expect([g.recordingsPct, g.otherPct, g.freePct]).toEqual([0, 0, 0])
  })
})

describe('storageWarning', () => {
  it('is full below the floor', () => {
    expect(storageWarning(storage({ freeBytes: 1 * GB }))).toBe('full')
    expect(DISK_FULL_WARNING).toBe("Disk almost full — new recordings won't start until space is freed.")
  })

  it('is low below minFreeBytes', () => {
    expect(storageWarning(storage({ freeBytes: 40 * GB, minFreeBytes: 50 * GB }))).toBe('low')
  })

  it('full wins over low', () => {
    expect(storageWarning(storage({ freeBytes: 1 * GB, minFreeBytes: 50 * GB }))).toBe('full')
  })

  it('is quiet with enough space or the sweep off', () => {
    expect(storageWarning(storage())).toBeNull()
    expect(storageWarning(storage({ freeBytes: 10 * GB, minFreeBytes: 0 }))).toBeNull()
  })
})

describe('padding minutes ↔ seconds', () => {
  it('converts both ways', () => {
    expect(secondsToMinutes(180)).toBe(3)
    expect(secondsToMinutes(90)).toBe(1.5)
    expect(secondsToMinutes(0)).toBe(0)
    expect(secondsToMinutes(100)).toBe(1.7)
    expect(minutesToSeconds(3)).toBe(180)
    expect(minutesToSeconds(1.5)).toBe(90)
    expect(minutesToSeconds(0.25)).toBe(15)
  })

  it('seeds the form from settings, defaulting when absent', () => {
    expect(paddingToForm({ padStartSeconds: 60, padEndSeconds: 180, quality: '1080p' })).toEqual({
      start: '1',
      end: '3',
      quality: '1080p',
    })
    expect(paddingToForm(undefined)).toEqual({ start: '1', end: '3', quality: null })
  })

  it('leaves quality out for a server without the setting', () => {
    expect(paddingToForm({ padStartSeconds: 60, padEndSeconds: 180 }).quality).toBeNull()
    expect(buildPaddingPayload({ start: '1', end: '3', quality: null })).toEqual({
      ok: true,
      dvr: { padStartSeconds: 60, padEndSeconds: 180 },
    })
  })

  it('reads an unknown quality as the default', () => {
    const dvr = { padStartSeconds: 60, padEndSeconds: 180, quality: 'original' } as unknown as SettingsDVR
    expect(paddingToForm(dvr).quality).toBe('720p')
  })

  it('builds the payload in seconds', () => {
    expect(buildPaddingPayload({ start: '2', end: '10', quality: '720p' })).toEqual({
      ok: true,
      dvr: { padStartSeconds: 120, padEndSeconds: 600, quality: '720p' },
    })
    expect(buildPaddingPayload({ start: ' 0 ', end: '1.5', quality: '1080p' })).toEqual({
      ok: true,
      dvr: { padStartSeconds: 0, padEndSeconds: 90, quality: '1080p' },
    })
    expect(buildPaddingPayload({ start: '30', end: '60', quality: '720p' })).toEqual({
      ok: true,
      dvr: { padStartSeconds: 1800, padEndSeconds: 3600, quality: '720p' },
    })
  })

  it('rejects bad values', () => {
    expect(buildPaddingPayload({ start: '', end: '3', quality: '720p' })).toEqual({
      ok: false,
      error: 'Start early must be a number of minutes',
    })
    expect(buildPaddingPayload({ start: 'x', end: '3', quality: '720p' }).ok).toBe(false)
    expect(buildPaddingPayload({ start: '-1', end: '3', quality: '720p' })).toEqual({
      ok: false,
      error: 'Start early must be between 0 and 30 minutes',
    })
    expect(buildPaddingPayload({ start: '31', end: '3', quality: '720p' }).ok).toBe(false)
    expect(buildPaddingPayload({ start: '1', end: '61', quality: '720p' })).toEqual({
      ok: false,
      error: 'Keep recording after must be between 0 and 60 minutes',
    })
  })
})

describe('recording quality', () => {
  it('offers 720p (default) then 1080p', () => {
    expect(RECORDING_QUALITIES.map((q) => q.value)).toEqual(['720p', '1080p'])
  })

  it('estimates bytes per hour from the bitrates', () => {
    // (4000 + 160) kb/s × 3600 s ÷ 8 = 1.872 GB (decimal).
    expect(bytesPerHour(4000, 160)).toBe(1_872_000_000)
    expect(bytesPerHour(8000, 160)).toBe(3_672_000_000)
  })

  it('labels each choice with its size', () => {
    expect(RECORDING_QUALITIES.map(qualityOptionLabel)).toEqual([
      '720p — about 1.7 GB/hour',
      'Up to 1080p — up to 3.4 GB/hour',
    ])
  })

  it('explains the size of each choice in one line', () => {
    expect(qualitySizeHint('720p')).toBe('About 1.7 GB per hour of recording.')
    expect(qualitySizeHint('1080p')).toBe(
      'Up to about 3.4 GB per hour — 1080i channels keep full resolution; 720p channels stay 720p.',
    )
  })
})
