import { afterEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from './client'
import { CANT_REACH_SERVER, viewerErrorText } from './errorText'

describe('viewerErrorText', () => {
  afterEach(() => {
    vi.restoreAllMocks()
  })

  it("keeps the server's own message", () => {
    expect(viewerErrorText(new ApiError(409, 'That show is already recording.'), 'x')).toBe(
      'That show is already recording.',
    )
  })

  it("says the server can't be reached when the network or a proxy fails", () => {
    vi.spyOn(console, 'warn').mockImplementation(() => {})
    expect(viewerErrorText(new TypeError('Failed to fetch'), 'x')).toBe(CANT_REACH_SERVER)
    for (const status of [502, 503, 504]) {
      const err = new ApiError(status, '<html>Bad Gateway</html>', undefined, true)
      expect(viewerErrorText(err, 'x')).toBe(CANT_REACH_SERVER)
    }
  })

  it('falls back for other technical errors and logs the detail', () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    expect(viewerErrorText(new ApiError(500, 'Internal Server Error', undefined, true), 'Fallback.')).toBe(
      'Fallback.',
    )
    expect(viewerErrorText(new ApiError(200, 'invalid JSON response', undefined, true), 'Fallback.')).toBe(
      'Fallback.',
    )
    expect(viewerErrorText(new Error('decode failed: EOF'), 'Fallback.')).toBe('Fallback.')
    expect(viewerErrorText(new ApiError(500, ''), 'Fallback.')).toBe('Fallback.')
    expect(warn).toHaveBeenCalled()
  })
})
