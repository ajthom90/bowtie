import { describe, expect, it, vi } from 'vitest'
import { ApiError } from '../api/client'
import { CANT_REACH_SERVER } from '../api/errorText'
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
    const noSignal =
      "This channel isn't coming in right now — your antenna isn't getting a picture from it. Try again later or pick another channel."
    expect(startErrorFrom(new ApiError(502, noSignal))).toEqual({
      message: noSignal,
      tunerBusy: false,
      retry: true,
    })
  })

  it("says the server can't be reached when the network fails", () => {
    vi.spyOn(console, 'warn').mockImplementation(() => {})
    expect(startErrorFrom(new TypeError('Failed to fetch')).message).toBe(CANT_REACH_SERVER)
    // A proxy's 503 page is not the server's tuners-busy answer.
    const proxy = startErrorFrom(new ApiError(503, 'Service Unavailable', undefined, true))
    expect(proxy).toEqual({ message: CANT_REACH_SERVER, tunerBusy: false, retry: true })
  })

  it('hides technical text', () => {
    vi.spyOn(console, 'warn').mockImplementation(() => {})
    expect(startErrorFrom(new ApiError(500, 'Internal Server Error', undefined, true)).message).toBe(
      'Could not start playback.',
    )
  })

  it('falls back for unknown errors', () => {
    expect(startErrorFrom(new Error('boom'))).toEqual({
      message: 'Could not start playback.',
      tunerBusy: false,
      retry: true,
    })
  })
})
