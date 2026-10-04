import { afterAll, describe, expect, it } from 'vitest'
import {
  MAX_HOURS,
  buildManualRequest,
  buildWindow,
  defaultStartTime,
  defaultTitle,
  localDateValue,
  plusOneHour,
  validateWindow,
} from './manualRecordModel'

const H = 3_600_000

// A zone with DST, set before any Date below is built (describe bodies run
// at collection time, ahead of beforeAll).
const savedTZ = process.env.TZ
process.env.TZ = 'America/New_York'

afterAll(() => {
  if (savedTZ === undefined) delete process.env.TZ
  else process.env.TZ = savedTZ
})

describe('localDateValue', () => {
  it('uses the local calendar day, not UTC', () => {
    // 22:30 local on Oct 4 is already Oct 5 in UTC.
    expect(localDateValue(new Date(2026, 9, 4, 22, 30))).toBe('2026-10-04')
  })
  it('pads month and day', () => {
    expect(localDateValue(new Date(2026, 0, 5, 9, 0))).toBe('2026-01-05')
  })
})

describe('defaultStartTime', () => {
  it('rounds up to the next quarter hour', () => {
    expect(defaultStartTime(new Date(2026, 9, 4, 20, 7))).toBe('20:15')
    expect(defaultStartTime(new Date(2026, 9, 4, 20, 46))).toBe('21:00')
  })
  it('keeps an exact quarter hour', () => {
    expect(defaultStartTime(new Date(2026, 9, 4, 20, 30))).toBe('20:30')
  })
  it('wraps past midnight', () => {
    expect(defaultStartTime(new Date(2026, 9, 4, 23, 50))).toBe('00:00')
  })
})

describe('plusOneHour', () => {
  it('adds an hour of wall-clock time', () => {
    expect(plusOneHour('20:15')).toBe('21:15')
  })
  it('wraps to the next day', () => {
    expect(plusOneHour('23:30')).toBe('00:30')
  })
  it('passes through junk', () => {
    expect(plusOneHour('')).toBe('')
  })
})

describe('buildWindow', () => {
  it('builds a same-day window from local fields', () => {
    const w = buildWindow('2026-10-04', '20:00', '21:30')!
    expect(w.start).toEqual(new Date(2026, 9, 4, 20, 0))
    expect(w.stop).toEqual(new Date(2026, 9, 4, 21, 30))
  })
  it('an end before the start means the next day', () => {
    const w = buildWindow('2026-10-04', '23:00', '01:00')!
    expect(w.stop).toEqual(new Date(2026, 9, 5, 1, 0))
    expect(w.stop.getTime() - w.start.getTime()).toBe(2 * H)
  })
  it('crosses a month and year boundary', () => {
    const w = buildWindow('2026-12-31', '23:30', '00:30')!
    expect(w.stop).toEqual(new Date(2027, 0, 1, 0, 30))
  })
  it('the same start and end is a full day later', () => {
    const w = buildWindow('2026-10-04', '20:00', '20:00')!
    expect(w.stop.getTime() - w.start.getTime()).toBe(24 * H)
  })
  it('fall back: midnight to 3 AM is four real hours', () => {
    const w = buildWindow('2026-11-01', '00:00', '03:00')!
    expect(w.stop.getTime() - w.start.getTime()).toBe(4 * H)
  })
  it('fall back: 23:00 to 01:00 across the change keeps the wall clock', () => {
    const w = buildWindow('2026-10-31', '23:00', '01:00')!
    expect(w.stop.getDate()).toBe(1)
    expect(w.stop.getHours()).toBe(1)
    expect(w.stop.getMinutes()).toBe(0)
  })
  it('spring forward: 1 AM to 4 AM is two real hours', () => {
    const w = buildWindow('2026-03-08', '01:00', '04:00')!
    expect(w.stop.getTime() - w.start.getTime()).toBe(2 * H)
  })
  it('rejects missing or malformed fields', () => {
    expect(buildWindow('', '20:00', '21:00')).toBeNull()
    expect(buildWindow('2026-10-04', '', '21:00')).toBeNull()
    expect(buildWindow('2026-10-04', '20:00', '')).toBeNull()
    expect(buildWindow('2026-13-04', '20:00', '21:00')).toBeNull()
    expect(buildWindow('2026-10-04', '25:00', '21:00')).toBeNull()
    expect(buildWindow('nope', 'x', 'y')).toBeNull()
  })
})

describe('validateWindow', () => {
  const now = new Date(2026, 9, 4, 19, 0)

  it('accepts a normal future window', () => {
    expect(validateWindow(buildWindow('2026-10-04', '20:00', '21:00'), now)).toBeNull()
  })
  it('accepts a window that is already under way', () => {
    expect(validateWindow(buildWindow('2026-10-04', '18:30', '19:30'), now)).toBeNull()
  })
  it('needs every field', () => {
    expect(validateWindow(null, now)).toBe('Enter a date, start time and end time.')
  })
  it('rejects a window entirely in the past', () => {
    expect(validateWindow(buildWindow('2026-10-04', '17:00', '18:00'), now)).toBe(
      'That time has already passed.',
    )
    expect(validateWindow(buildWindow('2026-10-04', '18:00', '19:00'), now)).toBe(
      'That time has already passed.',
    )
  })
  it('allows exactly 12 hours', () => {
    expect(validateWindow(buildWindow('2026-10-04', '20:00', '08:00'), now)).toBeNull()
  })
  it('rejects more than 12 hours', () => {
    expect(validateWindow(buildWindow('2026-10-04', '20:00', '08:15'), now)).toBe(
      `A recording can be at most ${MAX_HOURS} hours.`,
    )
  })
  it('the same start and end is too long', () => {
    expect(validateWindow(buildWindow('2026-10-04', '20:00', '20:00'), now)).toBe(
      `A recording can be at most ${MAX_HOURS} hours.`,
    )
  })
  it('counts real time: 23:00 to 11:00 on fall-back night is 13 hours', () => {
    const early = new Date(2026, 9, 31, 12, 0)
    expect(validateWindow(buildWindow('2026-10-31', '23:00', '11:00'), early)).toBe(
      `A recording can be at most ${MAX_HOURS} hours.`,
    )
  })
})

describe('defaultTitle', () => {
  it('is channel name, date and start time', () => {
    expect(defaultTitle('KMSP', new Date(2026, 9, 4, 20, 0), 'en-US')).toBe('KMSP Oct 4 8:00 PM')
  })
  it('falls back without a channel', () => {
    expect(defaultTitle('', new Date(2026, 9, 4, 9, 5), 'en-US')).toBe('Recording Oct 4 9:05 AM')
  })
})

describe('buildManualRequest', () => {
  const now = new Date(2026, 9, 4, 19, 0)
  const base = { channelId: 3, date: '2026-10-04', start: '23:00', end: '01:00', title: 'Late show' }

  it('builds the POST body with RFC3339 times', () => {
    const res = buildManualRequest(base, now)
    expect(res).toEqual({
      ok: true,
      body: {
        channelId: 3,
        start: new Date(2026, 9, 4, 23, 0).toISOString(),
        stop: new Date(2026, 9, 5, 1, 0).toISOString(),
        title: 'Late show',
      },
    })
  })
  it('trims the title', () => {
    const res = buildManualRequest({ ...base, title: '  Late show  ' }, now)
    expect(res.ok && res.body.title).toBe('Late show')
  })
  it('needs a title', () => {
    expect(buildManualRequest({ ...base, title: '  ' }, now)).toEqual({
      ok: false,
      error: 'Enter a title.',
    })
  })
  it('needs a channel', () => {
    expect(buildManualRequest({ ...base, channelId: null }, now)).toEqual({
      ok: false,
      error: 'Choose a channel.',
    })
  })
  it('passes window errors through', () => {
    expect(buildManualRequest({ ...base, start: '17:00', end: '18:00' }, now)).toEqual({
      ok: false,
      error: 'That time has already passed.',
    })
  })
})
