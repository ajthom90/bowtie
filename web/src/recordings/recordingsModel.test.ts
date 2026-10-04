import { afterEach, describe, expect, it, vi } from 'vitest'
import type { Recording } from '../api/client'
import {
  RECORDINGS_TABS,
  conflictHeading,
  conflictLine,
  createPositionSaver,
  failureText,
  formatClock,
  formatDuration,
  formatSize,
  formatWhen,
  guideMarkText,
  guideRecLabel,
  programRecordAction,
  recordingBadges,
  removeConfirmText,
  resumeDecision,
  rowActions,
  tabQuery,
} from './recordingsModel'

function rec(over: Partial<Recording> = {}): Recording {
  return {
    id: 7,
    title: 'Jeopardy!',
    subtitle: '',
    description: '',
    category: '',
    channelId: 3,
    channelName: '5.1 KSTP',
    start: '2026-10-05T00:00:00Z',
    stop: '2026-10-05T00:30:00Z',
    state: 'scheduled',
    partial: false,
    failure: '',
    failureDetail: '',
    durationSec: 0,
    sizeBytes: 0,
    protected: false,
    positionSec: 0,
    scheduledBy: 'andrew',
    canManage: true,
    ...over,
  }
}

describe('tabs', () => {
  it('lists Upcoming, Recorded, Missed in order', () => {
    expect(RECORDINGS_TABS.map((t) => t.label)).toEqual(['Upcoming', 'Recorded', 'Missed'])
  })

  it('maps each tab to the server state filter (Missed = failed)', () => {
    expect(tabQuery('upcoming')).toBe('upcoming')
    expect(tabQuery('recorded')).toBe('recorded')
    expect(tabQuery('missed')).toBe('failed')
  })
})

describe('recordingBadges', () => {
  it('labels each in-flight state', () => {
    expect(recordingBadges(rec({ state: 'scheduled' })).map((b) => b.label)).toEqual(['Scheduled'])
    expect(recordingBadges(rec({ state: 'waiting' })).map((b) => b.label)).toEqual([
      'Waiting for a tuner',
    ])
    expect(recordingBadges(rec({ state: 'recording' })).map((b) => b.label)).toEqual([
      '● Recording',
    ])
    expect(recordingBadges(rec({ state: 'converting' })).map((b) => b.label)).toEqual([
      'Converting',
    ])
  })

  it('marks recording as live and waiting as a warning', () => {
    expect(recordingBadges(rec({ state: 'recording' }))[0].tone).toBe('live')
    expect(recordingBadges(rec({ state: 'waiting' }))[0].tone).toBe('warn')
  })

  it('shows no badge for a complete ready recording', () => {
    expect(recordingBadges(rec({ state: 'ready' }))).toEqual([])
  })

  it('adds Partial when part of the show is missing', () => {
    expect(recordingBadges(rec({ state: 'ready', partial: true })).map((b) => b.label)).toEqual([
      'Partial',
    ])
    expect(
      recordingBadges(rec({ state: 'converting', partial: true })).map((b) => b.label),
    ).toEqual(['Converting', 'Partial'])
  })

  it('shows no state badge for failed rows (the failure text says why)', () => {
    expect(recordingBadges(rec({ state: 'failed', failure: 'noTuner' }))).toEqual([])
  })
})

describe('failureText', () => {
  it('uses plain words for each failure', () => {
    expect(failureText('noTuner', '')).toBe('No tuner was free')
    expect(failureText('noSignal', '')).toBe('No signal')
    expect(failureText('diskFull', '')).toBe('Disk full')
  })

  it('shows the detail for a generic error', () => {
    expect(failureText('error', 'ffmpeg exited 1')).toBe('ffmpeg exited 1')
  })

  it('falls back when an error has no detail', () => {
    expect(failureText('error', '')).toBe('Something went wrong')
    expect(failureText('error', '   ')).toBe('Something went wrong')
  })

  it('is empty when there is no failure', () => {
    expect(failureText('', '')).toBe('')
  })
})

describe('formatDuration', () => {
  it('dashes an unknown duration', () => {
    expect(formatDuration(0)).toBe('—')
    expect(formatDuration(-5)).toBe('—')
  })

  it('formats minutes and hours', () => {
    expect(formatDuration(30)).toBe('<1 min')
    expect(formatDuration(1980)).toBe('33 min')
    expect(formatDuration(3600)).toBe('1 h')
    expect(formatDuration(5400)).toBe('1 h 30 min')
    expect(formatDuration(7260)).toBe('2 h 1 min')
  })
})

describe('formatSize', () => {
  it('dashes zero', () => {
    expect(formatSize(0)).toBe('—')
  })

  it('uses decimal units with one decimal below 10', () => {
    expect(formatSize(512)).toBe('512 B')
    expect(formatSize(1_500)).toBe('1.5 KB')
    expect(formatSize(3_800_000_000)).toBe('3.8 GB')
    expect(formatSize(512_000_000)).toBe('512 MB')
    expect(formatSize(12_400_000_000)).toBe('12 GB')
    expect(formatSize(1_200_000_000_000)).toBe('1.2 TB')
  })
})

describe('formatClock', () => {
  it('formats m:ss under an hour', () => {
    expect(formatClock(0)).toBe('0:00')
    expect(formatClock(5)).toBe('0:05')
    expect(formatClock(754)).toBe('12:34')
  })

  it('formats h:mm:ss from an hour', () => {
    expect(formatClock(3723)).toBe('1:02:03')
  })

  it('floors fractions and clamps negatives', () => {
    expect(formatClock(59.9)).toBe('0:59')
    expect(formatClock(-3)).toBe('0:00')
  })
})

describe('resumeDecision', () => {
  it('starts from the beginning at or under 10 s', () => {
    expect(resumeDecision(0, 1800)).toEqual({ kind: 'start' })
    expect(resumeDecision(10, 1800)).toEqual({ kind: 'start' })
  })

  it('asks just past 10 s', () => {
    expect(resumeDecision(11, 1800)).toEqual({ kind: 'ask', positionSec: 11 })
  })

  it('asks in the middle', () => {
    expect(resumeDecision(754, 1800)).toEqual({ kind: 'ask', positionSec: 754 })
  })

  it('starts over in the last 30 s', () => {
    expect(resumeDecision(1769, 1800)).toEqual({ kind: 'ask', positionSec: 1769 })
    expect(resumeDecision(1770, 1800)).toEqual({ kind: 'start' })
    expect(resumeDecision(1800, 1800)).toEqual({ kind: 'start' })
  })

  it('asks past 10 s when the duration is unknown', () => {
    expect(resumeDecision(400, 0)).toEqual({ kind: 'ask', positionSec: 400 })
    expect(resumeDecision(5, 0)).toEqual({ kind: 'start' })
  })
})

describe('guideRecLabel', () => {
  it('is null without a recording', () => {
    expect(guideRecLabel(undefined)).toBeNull()
  })

  it('shows REC only while recording', () => {
    expect(guideRecLabel({ id: 1, state: 'recording' })).toBe('● REC')
    expect(guideRecLabel({ id: 1, state: 'scheduled' })).toBe('●')
    expect(guideRecLabel({ id: 1, state: 'waiting' })).toBe('●')
    expect(guideRecLabel({ id: 1, state: 'ready' })).toBe('●')
  })
})

describe('guideMarkText', () => {
  it('describes a guide mark in words', () => {
    expect(guideMarkText('scheduled')).toBe('Recording scheduled')
    expect(guideMarkText('waiting')).toBe('Waiting for a tuner')
    expect(guideMarkText('recording')).toBe('Recording now')
    expect(guideMarkText('converting')).toBe('Recorded')
    expect(guideMarkText('ready')).toBe('Recorded')
    expect(guideMarkText('something')).toBe('')
  })
})

describe('programRecordAction', () => {
  const now = new Date('2026-10-05T00:10:00Z')
  const prog = (start: string, stop: string, recording?: { id: number; state: string }) => ({
    start,
    stop,
    recording,
  })

  it('offers Record for a program that has not ended', () => {
    expect(programRecordAction(prog('2026-10-05T01:00:00Z', '2026-10-05T01:30:00Z'), now)).toBe(
      'record',
    )
    expect(programRecordAction(prog('2026-10-05T00:00:00Z', '2026-10-05T00:30:00Z'), now)).toBe(
      'record',
    )
  })

  it('offers nothing for a program that has ended', () => {
    expect(
      programRecordAction(prog('2026-10-04T23:00:00Z', '2026-10-05T00:00:00Z'), now),
    ).toBeNull()
  })

  it('offers Cancel for scheduled or waiting', () => {
    expect(
      programRecordAction(
        prog('2026-10-05T01:00:00Z', '2026-10-05T01:30:00Z', { id: 2, state: 'scheduled' }),
        now,
      ),
    ).toBe('cancel')
    expect(
      programRecordAction(
        prog('2026-10-05T00:00:00Z', '2026-10-05T00:30:00Z', { id: 2, state: 'waiting' }),
        now,
      ),
    ).toBe('cancel')
  })

  it('offers Stop while recording', () => {
    expect(
      programRecordAction(
        prog('2026-10-05T00:00:00Z', '2026-10-05T00:30:00Z', { id: 2, state: 'recording' }),
        now,
      ),
    ).toBe('stop')
  })

  it('says recorded once it is converting or ready, even after it ended', () => {
    expect(
      programRecordAction(
        prog('2026-10-04T23:00:00Z', '2026-10-05T00:00:00Z', { id: 2, state: 'ready' }),
        now,
      ),
    ).toBe('recorded')
    expect(
      programRecordAction(
        prog('2026-10-04T23:00:00Z', '2026-10-05T00:00:00Z', { id: 2, state: 'converting' }),
        now,
      ),
    ).toBe('recorded')
  })
})

describe('conflicts', () => {
  it('builds the heading from the tuner count', () => {
    expect(conflictHeading(2)).toBe('Only 2 tuners — these recordings already need them:')
    expect(conflictHeading(1)).toBe('Only 1 tuner — these recordings already need them:')
  })

  it('describes a conflicting recording by title, channel and local time', () => {
    const start = new Date(2026, 9, 4, 19, 0)
    const stop = new Date(2026, 9, 4, 19, 30)
    const line = conflictLine(
      rec({ title: 'News', channelName: '9.1 FOX 9', start: start.toISOString(), stop: stop.toISOString() }),
    )
    expect(line).toBe('News · 9.1 FOX 9 · 19:00–19:30')
  })
})

describe('formatWhen', () => {
  const now = new Date(2026, 9, 4, 12, 0)

  it('says Today, Tomorrow and Yesterday', () => {
    expect(
      formatWhen(new Date(2026, 9, 4, 19, 0).toISOString(), new Date(2026, 9, 4, 19, 30).toISOString(), now),
    ).toBe('Today 19:00–19:30')
    expect(
      formatWhen(new Date(2026, 9, 5, 0, 0).toISOString(), new Date(2026, 9, 5, 0, 30).toISOString(), now),
    ).toBe('Tomorrow 00:00–00:30')
    expect(
      formatWhen(new Date(2026, 9, 3, 22, 0).toISOString(), new Date(2026, 9, 3, 23, 0).toISOString(), now),
    ).toBe('Yesterday 22:00–23:00')
  })

  it('uses a date further away and still shows the time range', () => {
    const s = formatWhen(
      new Date(2026, 9, 10, 8, 0).toISOString(),
      new Date(2026, 9, 10, 9, 0).toISOString(),
      now,
    )
    expect(s).toMatch(/10/)
    expect(s.endsWith(' 08:00–09:00')).toBe(true)
    expect(s.startsWith('Today')).toBe(false)
  })
})

describe('rowActions', () => {
  it('plays only ready recordings, for anyone', () => {
    expect(rowActions(rec({ state: 'ready', canManage: false })).play).toBe(true)
    expect(rowActions(rec({ state: 'converting' })).play).toBe(false)
    expect(rowActions(rec({ state: 'failed' })).play).toBe(false)
  })

  it('hides every management action without canManage', () => {
    for (const state of ['scheduled', 'waiting', 'recording', 'converting', 'ready', 'failed'] as const) {
      const a = rowActions(rec({ state, canManage: false }))
      expect(a.stop).toBe(false)
      expect(a.remove).toBeNull()
      expect(a.keep).toBe(false)
    }
  })

  it('stops only while recording', () => {
    expect(rowActions(rec({ state: 'recording' })).stop).toBe(true)
    expect(rowActions(rec({ state: 'waiting' })).stop).toBe(false)
    expect(rowActions(rec({ state: 'ready' })).stop).toBe(false)
  })

  it('cancels upcoming and deletes everything else', () => {
    expect(rowActions(rec({ state: 'scheduled' })).remove).toBe('cancel')
    expect(rowActions(rec({ state: 'waiting' })).remove).toBe('cancel')
    expect(rowActions(rec({ state: 'recording' })).remove).toBe('delete')
    expect(rowActions(rec({ state: 'ready' })).remove).toBe('delete')
    expect(rowActions(rec({ state: 'failed' })).remove).toBe('delete')
  })

  it('offers Keep for recorded shows', () => {
    expect(rowActions(rec({ state: 'ready' })).keep).toBe(true)
    expect(rowActions(rec({ state: 'converting' })).keep).toBe(true)
    expect(rowActions(rec({ state: 'scheduled' })).keep).toBe(false)
    expect(rowActions(rec({ state: 'failed' })).keep).toBe(false)
  })
})

describe('removeConfirmText', () => {
  it('names the recording', () => {
    expect(removeConfirmText(rec({ title: 'News' }), 'cancel')).toBe('Cancel recording “News”?')
    expect(removeConfirmText(rec({ title: 'News' }), 'delete')).toBe(
      'Delete “News”? This removes the recording for everyone.',
    )
  })
})

describe('createPositionSaver', () => {
  afterEach(() => {
    vi.useRealTimers()
  })

  it('saves every interval only when the position changed', () => {
    vi.useFakeTimers()
    let pos: number | null = 100.4
    const save = vi.fn()
    const saver = createPositionSaver({ save, getPosition: () => pos, intervalMs: 15_000 })
    saver.start()
    expect(save).not.toHaveBeenCalled()

    vi.advanceTimersByTime(15_000)
    expect(save).toHaveBeenLastCalledWith(100)
    expect(save).toHaveBeenCalledTimes(1)

    vi.advanceTimersByTime(15_000) // unchanged
    expect(save).toHaveBeenCalledTimes(1)

    pos = 130.9
    vi.advanceTimersByTime(15_000)
    expect(save).toHaveBeenLastCalledWith(130)
    expect(save).toHaveBeenCalledTimes(2)

    saver.stop()
    pos = 200
    vi.advanceTimersByTime(60_000)
    expect(save).toHaveBeenCalledTimes(2)
  })

  it('flush saves the latest position at once and skips unknown positions', async () => {
    let pos: number | null = null
    const save = vi.fn().mockResolvedValue(undefined)
    const saver = createPositionSaver({ save, getPosition: () => pos })
    await saver.flush()
    expect(save).not.toHaveBeenCalled()

    pos = 42
    await saver.flush()
    expect(save).toHaveBeenCalledWith(42)
    await saver.flush()
    expect(save).toHaveBeenCalledTimes(1)
  })

  it('swallows save errors', async () => {
    const save = vi.fn().mockRejectedValue(new Error('offline'))
    const saver = createPositionSaver({ save, getPosition: () => 12 })
    await expect(saver.flush()).resolves.toBeUndefined()
  })
})
