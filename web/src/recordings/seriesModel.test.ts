import { describe, expect, it } from 'vitest'
import type { RecordingRule } from '../api/client'
import {
  DEFAULT_SERIES_FORM,
  buildRulePayload,
  parseKeepLatest,
  ruleSummary,
  scheduledText,
  seriesAvailable,
  stopShowConfirmText,
} from './seriesModel'

function rule(over: Partial<RecordingRule> = {}): RecordingRule {
  return {
    id: 1,
    title: 'Jeopardy!',
    seriesId: 'SH001',
    channelId: 3,
    channelName: 'KMSP',
    newOnly: true,
    keepLatest: 0,
    scheduledBy: 'andrew',
    canManage: true,
    createdAt: '2026-10-01T12:00:00Z',
    ...over,
  }
}

describe('DEFAULT_SERIES_FORM', () => {
  it('is this channel, new episodes only, keep all', () => {
    expect(DEFAULT_SERIES_FORM).toEqual({ anyChannel: false, newOnly: true, keepLatest: '0' })
  })
})

describe('parseKeepLatest', () => {
  it('accepts whole numbers 0 and up; blank means 0', () => {
    expect(parseKeepLatest('0')).toBe(0)
    expect(parseKeepLatest(' 5 ')).toBe(5)
    expect(parseKeepLatest('')).toBe(0)
  })
  it('rejects negatives, fractions and text', () => {
    expect(parseKeepLatest('-1')).toBeNull()
    expect(parseKeepLatest('2.5')).toBeNull()
    expect(parseKeepLatest('lots')).toBeNull()
  })
})

describe('buildRulePayload', () => {
  const program = { channelId: 3, start: '2026-10-04T19:00:00Z' }

  it('builds the POST body from the form', () => {
    expect(buildRulePayload(program, { anyChannel: true, newOnly: false, keepLatest: '4' })).toEqual({
      ok: true,
      body: {
        channelId: 3,
        programStart: '2026-10-04T19:00:00Z',
        anyChannel: true,
        newOnly: false,
        keepLatest: 4,
      },
    })
  })

  it('reports a bad keep count', () => {
    expect(buildRulePayload(program, { ...DEFAULT_SERIES_FORM, keepLatest: '-2' })).toEqual({
      ok: false,
      error: 'Keep latest must be a whole number (0 keeps all).',
    })
  })
})

describe('scheduledText', () => {
  it('counts episodes', () => {
    expect(scheduledText(3)).toBe('Scheduled 3 episodes')
    expect(scheduledText(1)).toBe('Scheduled 1 episode')
  })
  it('explains zero', () => {
    expect(scheduledText(0)).toBe(
      'Scheduled 0 episodes. New airings are added when they show up in the guide.',
    )
  })
})

describe('ruleSummary', () => {
  it('describes channel, episodes and keep count', () => {
    expect(ruleSummary(rule())).toBe('KMSP · New episodes · Keep all')
    expect(ruleSummary(rule({ channelId: 0, channelName: '', newOnly: false, keepLatest: 5 }))).toBe(
      'Any channel · All episodes · Keep latest 5',
    )
  })
})

describe('seriesAvailable', () => {
  const p = { start: '2026-10-04T19:00:00Z', stop: '2026-10-04T19:30:00Z' }
  it('is offered until the program ends', () => {
    expect(seriesAvailable(p, new Date('2026-10-04T19:10:00Z'))).toBe(true)
    expect(seriesAvailable(p, new Date('2026-10-04T19:30:00Z'))).toBe(false)
  })
})

describe('stopShowConfirmText', () => {
  it('names the show and what happens', () => {
    expect(stopShowConfirmText(rule())).toBe(
      'Stop recording “Jeopardy!”? Upcoming episodes are cancelled; recorded ones stay.',
    )
  })
})
