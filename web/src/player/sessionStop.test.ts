import { afterEach, describe, expect, it, vi } from 'vitest'
import { bestEffortDelete } from './sessionStop'

describe('bestEffortDelete', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
  })

  it('sends exactly one keepalive DELETE and no beacon', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(null, { status: 204 }))
    vi.stubGlobal('fetch', fetchMock)
    const beacon = vi.fn()
    Object.defineProperty(navigator, 'sendBeacon', { value: beacon, configurable: true })

    await bestEffortDelete('v 1', 'access', 'tok')

    expect(fetchMock).toHaveBeenCalledTimes(1)
    const [url, init] = fetchMock.mock.calls[0]
    expect(url).toBe('/api/v1/sessions/v%201?token=tok')
    expect(init).toMatchObject({ method: 'DELETE', keepalive: true })
    expect(init.headers).toEqual({ Authorization: 'Bearer access' })
    expect(beacon).not.toHaveBeenCalled()
  })

  it('resolves even when the request fails', async () => {
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('offline')))
    await expect(bestEffortDelete('v1', null, null)).resolves.toBeUndefined()
  })
})
