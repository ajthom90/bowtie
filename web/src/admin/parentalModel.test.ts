import { describe, expect, it } from 'vitest'
import type { AdminChannel } from '../api/client'
import {
  RATINGS_NOTE,
  RATING_OPTIONS,
  channelsSummary,
  initialSelection,
  parentalEditable,
  pickerChannels,
  ratingLabel,
  toggleChannel,
} from './parentalModel'

function ch(id: number, guideNumber: string, enabled = true): AdminChannel {
  return { id, deviceId: 'D', guideNumber, name: `CH${id}`, enabled, epgChannelId: '' }
}

describe('RATING_OPTIONS', () => {
  it('is none then the TV ladder from youngest to oldest', () => {
    expect(RATING_OPTIONS.map((o) => o.value)).toEqual(['', 'TV-Y', 'TV-Y7', 'TV-G', 'TV-PG', 'TV-14', 'TV-MA'])
    expect(RATING_OPTIONS[0].label).toBe('No limit')
  })
})

describe('ratingLabel', () => {
  it('labels known values and keeps unknown ones', () => {
    expect(ratingLabel('')).toBe('No limit')
    expect(ratingLabel(undefined)).toBe('No limit')
    expect(ratingLabel('TV-PG')).toBe('TV-PG')
    expect(ratingLabel('PG-13')).toBe('PG-13')
  })
})

describe('channelsSummary', () => {
  it('reads All channels for null or undefined', () => {
    expect(channelsSummary(null, 12)).toBe('All channels')
    expect(channelsSummary(undefined, 12)).toBe('All channels')
  })
  it('counts the allowed channels', () => {
    expect(channelsSummary([], 12)).toBe('No channels')
    expect(channelsSummary([3], 12)).toBe('1 of 12 channels')
    expect(channelsSummary([3, 4], 12)).toBe('2 of 12 channels')
  })
  it('drops the total when unknown', () => {
    expect(channelsSummary([3, 4], 0)).toBe('2 channels')
    expect(channelsSummary([3], 0)).toBe('1 channel')
  })
})

describe('toggleChannel', () => {
  it('adds a missing id and removes a present one, keeping order stable', () => {
    expect(toggleChannel([3, 5], 4)).toEqual([3, 4, 5])
    expect(toggleChannel([3, 4, 5], 4)).toEqual([3, 5])
  })
})

describe('pickerChannels', () => {
  const all = [ch(1, '11.1'), ch(2, '5.1'), ch(3, '9.1', false), ch(4, '9.2', false)]

  it('lists enabled channels in guide-number order', () => {
    expect(pickerChannels(all, null).map((c) => c.id)).toEqual([2, 1])
  })

  it('keeps disabled channels that are already allowed', () => {
    expect(pickerChannels(all, [3]).map((c) => c.id)).toEqual([2, 3, 1])
  })
})

describe('parentalEditable', () => {
  it('is false for admins (never restricted)', () => {
    expect(parentalEditable({ role: 'admin' })).toBe(false)
    expect(parentalEditable({ role: 'viewer' })).toBe(true)
  })
})

describe('RATINGS_NOTE', () => {
  it('explains where ratings come from', () => {
    expect(RATINGS_NOTE).toBe(
      'Ratings come from the guide; the free HDHomeRun guide has no ratings, so use Schedules Direct to limit by rating.',
    )
  })
})

describe('initialSelection', () => {
  const list = [ch(2, '5.1'), ch(1, '11.1')]
  it('starts from the allowed list', () => {
    expect(initialSelection([1], list)).toEqual([1])
  })
  it('starts with every listed channel ticked when all are allowed', () => {
    expect(initialSelection(null, list)).toEqual([1, 2])
  })
})
