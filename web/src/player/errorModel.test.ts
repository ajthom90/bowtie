import { describe, expect, it } from 'vitest'
import { ApiError } from '../api/client'
import { isParentalBlock, startErrorFrom } from './errorModel'

const parental = () =>
  new ApiError(403, 'Blocked by parental controls (rated TV-MA)', {
    error: 'Blocked by parental controls (rated TV-MA)',
    code: 'parental',
  })

describe('isParentalBlock', () => {
  it('is true for a 403 with code parental', () => {
    expect(isParentalBlock(parental())).toBe(true)
  })
  it('is false for other errors', () => {
    expect(isParentalBlock(new ApiError(403, 'forbidden', { error: 'forbidden' }))).toBe(false)
    expect(isParentalBlock(new ApiError(500, 'x', { code: 'parental' }))).toBe(false)
    expect(isParentalBlock(new Error('x'))).toBe(false)
    expect(isParentalBlock(null)).toBe(false)
  })
})

describe('startErrorFrom', () => {
  it('shows the parental message and offers no retry', () => {
    expect(startErrorFrom(parental())).toEqual({
      message: 'Blocked by parental controls (rated TV-MA)',
      tunerBusy: false,
      retry: false,
    })
  })

  it('falls back to a plain message for a parental block without text', () => {
    const err = new ApiError(403, '', { code: 'parental' })
    expect(startErrorFrom(err).message).toBe('Blocked by parental controls.')
  })

  it('marks tuners-busy errors with the busy message', () => {
    const err = new ApiError(503, 'all tuners in use', { error: 'all tuners in use', sessions: [], otherInUse: 1 })
    const e = startErrorFrom(err)
    expect(e.tunerBusy).toBe(true)
    expect(e.retry).toBe(true)
    expect(e.message).toContain('tuner')
  })

  it('uses the server message for other API errors', () => {
    expect(startErrorFrom(new ApiError(502, 'no signal on this channel'))).toEqual({
      message: 'no signal on this channel',
      tunerBusy: false,
      retry: true,
    })
  })

  it('falls back for unknown errors', () => {
    expect(startErrorFrom(new Error('boom'))).toEqual({
      message: 'Could not start playback.',
      tunerBusy: false,
      retry: true,
    })
  })
})
