import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiClient, ApiError } from './client'

describe('ApiClient request 401 retry', () => {
  let accessToken: string | null
  let refreshToken: string | null
  let authFailCount: number
  let client: ApiClient

  beforeEach(() => {
    accessToken = 'access-old'
    refreshToken = 'refresh-old'
    authFailCount = 0
    client = new ApiClient(
      () => accessToken,
      () => {
        authFailCount++
        accessToken = null
        refreshToken = null
      },
      {
        getRefreshToken: () => refreshToken,
        setTokens: (a, r) => {
          accessToken = a
          refreshToken = r
        },
      },
    )
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
  })

  it('retries once after refresh on 401 then succeeds', async () => {
    const fetchMock = vi
      .fn()
      // first GET /me → 401
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ error: 'unauthorized' }), { status: 401 }),
      )
      // POST /auth/refresh → 200
      .mockResolvedValueOnce(
        new Response(
          JSON.stringify({
            accessToken: 'access-new',
            refreshToken: 'refresh-new',
            user: { id: 1, username: 'admin', role: 'admin', maxQuality: '' },
          }),
          { status: 200, headers: { 'Content-Type': 'application/json' } },
        ),
      )
      // retry GET /me → 200
      .mockResolvedValueOnce(
        new Response(
          JSON.stringify({ id: 1, username: 'admin', role: 'admin', maxQuality: '' }),
          { status: 200, headers: { 'Content-Type': 'application/json' } },
        ),
      )

    vi.stubGlobal('fetch', fetchMock)

    const user = await client.request<{ id: number; username: string }>('GET', '/api/v1/me')
    expect(user.username).toBe('admin')
    expect(accessToken).toBe('access-new')
    expect(refreshToken).toBe('refresh-new')
    expect(authFailCount).toBe(0)
    expect(fetchMock).toHaveBeenCalledTimes(3)

    // Second call used the new access token.
    const retryCall = fetchMock.mock.calls[2]
    const retryHeaders = retryCall[1]?.headers as Record<string, string>
    expect(retryHeaders['Authorization']).toBe('Bearer access-new')
  })

  it('calls onAuthFail when refresh succeeds but retry still 401', async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(new Response(JSON.stringify({ error: 'unauthorized' }), { status: 401 }))
      .mockResolvedValueOnce(
        new Response(
          JSON.stringify({
            accessToken: 'access-new',
            refreshToken: 'refresh-new',
            user: { id: 1, username: 'admin', role: 'admin', maxQuality: '' },
          }),
          { status: 200 },
        ),
      )
      .mockResolvedValueOnce(new Response(JSON.stringify({ error: 'unauthorized' }), { status: 401 }))

    vi.stubGlobal('fetch', fetchMock)

    await expect(client.request('GET', '/api/v1/me')).rejects.toBeInstanceOf(ApiError)
    expect(authFailCount).toBe(1)
    expect(fetchMock).toHaveBeenCalledTimes(3)
  })

  it('calls onAuthFail when refresh fails (no second protected retry after failed refresh)', async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(new Response(JSON.stringify({ error: 'unauthorized' }), { status: 401 }))
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ error: 'invalid or expired refresh token' }), { status: 401 }),
      )

    vi.stubGlobal('fetch', fetchMock)

    await expect(client.request('GET', '/api/v1/me')).rejects.toBeInstanceOf(ApiError)
    expect(authFailCount).toBe(1)
    // only original request + refresh — no third call
    expect(fetchMock).toHaveBeenCalledTimes(2)
  })

  it('calls onAuthFail when no refresh token is available', async () => {
    refreshToken = null
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(new Response(JSON.stringify({ error: 'unauthorized' }), { status: 401 }))

    vi.stubGlobal('fetch', fetchMock)

    await expect(client.request('GET', '/api/v1/me')).rejects.toBeInstanceOf(ApiError)
    expect(authFailCount).toBe(1)
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })
})

describe('ApiClient.heartbeat', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
  })

  it('POSTs with stream token query and no Authorization header', async () => {
    const fetchMock = vi.fn().mockResolvedValueOnce(new Response(null, { status: 204 }))
    vi.stubGlobal('fetch', fetchMock)

    const client = new ApiClient(
      () => 'access-should-not-be-sent',
      () => {},
    )
    await client.heartbeat('viewer-abc', 'stream-tok-xyz')

    expect(fetchMock).toHaveBeenCalledTimes(1)
    const [url, init] = fetchMock.mock.calls[0]
    expect(url).toBe('/api/v1/sessions/viewer-abc/heartbeat?token=stream-tok-xyz')
    expect(init?.method).toBe('POST')
    const headers = init?.headers as Record<string, string> | undefined
    expect(headers?.Authorization).toBeUndefined()
  })

  it('throws ApiError on non-204', async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(new Response(JSON.stringify({ error: 'not found' }), { status: 404 }))
    vi.stubGlobal('fetch', fetchMock)

    const client = new ApiClient(() => null, () => {})
    await expect(client.heartbeat('gone', 'tok')).rejects.toMatchObject({
      name: 'ApiError',
      status: 404,
    })
  })
})
