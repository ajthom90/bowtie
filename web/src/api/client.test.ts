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

  it('keeps the error body (parental 403 code) on failure', async () => {
    const body = { error: 'Blocked by parental controls (rated TV-MA)', code: 'parental' }
    vi.stubGlobal('fetch', vi.fn().mockResolvedValueOnce(new Response(JSON.stringify(body), { status: 403 })))
    const client = new ApiClient(() => null, () => {})
    const err = await client.heartbeat('v', 'tok').catch((e: unknown) => e)
    expect(err).toBeInstanceOf(ApiError)
    expect((err as ApiError).status).toBe(403)
    expect((err as ApiError).message).toBe(body.error)
    expect((err as ApiError).body).toEqual(body)
  })
})

describe('ApiClient favorites and recents', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
  })

  function lastCall(fetchMock: ReturnType<typeof vi.fn>): [string, RequestInit | undefined] {
    return fetchMock.mock.calls[fetchMock.mock.calls.length - 1] as [string, RequestInit | undefined]
  }

  it('addFavorite PUTs /me/favorites/{id}', async () => {
    const fetchMock = vi.fn().mockResolvedValueOnce(new Response(null, { status: 204 }))
    vi.stubGlobal('fetch', fetchMock)
    const client = new ApiClient(() => 'tok', () => {})

    await expect(client.addFavorite(7)).resolves.toBeUndefined()
    const [url, init] = lastCall(fetchMock)
    expect(url).toBe('/api/v1/me/favorites/7')
    expect(init?.method).toBe('PUT')
    expect((init?.headers as Record<string, string>).Authorization).toBe('Bearer tok')
  })

  it('removeFavorite DELETEs /me/favorites/{id}', async () => {
    const fetchMock = vi.fn().mockResolvedValueOnce(new Response(null, { status: 204 }))
    vi.stubGlobal('fetch', fetchMock)
    const client = new ApiClient(() => 'tok', () => {})

    await client.removeFavorite(7)
    const [url, init] = lastCall(fetchMock)
    expect(url).toBe('/api/v1/me/favorites/7')
    expect(init?.method).toBe('DELETE')
  })

  it('addFavorite surfaces a 404 as ApiError', async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ error: 'channel not found' }), { status: 404 }),
      )
    vi.stubGlobal('fetch', fetchMock)
    const client = new ApiClient(() => 'tok', () => {})

    await expect(client.addFavorite(99)).rejects.toMatchObject({
      name: 'ApiError',
      status: 404,
      message: 'channel not found',
    })
  })

  it('getRecents GETs /me/recents with the limit', async () => {
    const body = [
      {
        channelId: 7,
        guideNumber: '9.1',
        name: 'FOX9',
        logoUrl: '',
        watchedAt: '2026-10-03T19:42:10Z',
      },
    ]
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(new Response(JSON.stringify(body), { status: 200 }))
    vi.stubGlobal('fetch', fetchMock)
    const client = new ApiClient(() => 'tok', () => {})

    await expect(client.getRecents(5)).resolves.toEqual(body)
    const [url, init] = lastCall(fetchMock)
    expect(url).toBe('/api/v1/me/recents?limit=5')
    expect(init?.method).toBe('GET')
  })

  it('getRecents defaults the limit to 8', async () => {
    const fetchMock = vi.fn().mockResolvedValueOnce(new Response('[]', { status: 200 }))
    vi.stubGlobal('fetch', fetchMock)
    const client = new ApiClient(() => 'tok', () => {})

    await client.getRecents()
    expect(lastCall(fetchMock)[0]).toBe('/api/v1/me/recents?limit=8')
  })

  it('getRecents throws a 404 ApiError on an older server', async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(new Response('404 page not found', { status: 404 }))
    vi.stubGlobal('fetch', fetchMock)
    const client = new ApiClient(() => 'tok', () => {})

    await expect(client.getRecents()).rejects.toMatchObject({ name: 'ApiError', status: 404 })
  })

  it('clearRecents DELETEs /me/recents', async () => {
    const fetchMock = vi.fn().mockResolvedValueOnce(new Response(null, { status: 204 }))
    vi.stubGlobal('fetch', fetchMock)
    const client = new ApiClient(() => 'tok', () => {})

    await client.clearRecents()
    const [url, init] = lastCall(fetchMock)
    expect(url).toBe('/api/v1/me/recents')
    expect(init?.method).toBe('DELETE')
  })
})

describe('ApiClient recordings', () => {
  let client: ApiClient
  let fetchMock: ReturnType<typeof vi.fn>

  beforeEach(() => {
    client = new ApiClient(
      () => 'tok',
      () => {},
    )
    fetchMock = vi.fn()
    vi.stubGlobal('fetch', fetchMock)
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  const json = (body: unknown, status = 200) =>
    new Response(JSON.stringify(body), {
      status,
      headers: { 'Content-Type': 'application/json' },
    })

  const call = (i = 0) => {
    const [path, init] = fetchMock.mock.calls[i] as [string, RequestInit]
    return {
      path,
      method: init.method,
      body: init.body ? JSON.parse(String(init.body)) : undefined,
      auth: (init.headers as Record<string, string>).Authorization,
    }
  }

  it('lists recordings with a state filter', async () => {
    fetchMock.mockResolvedValueOnce(json([]))
    await client.listRecordings('failed')
    expect(call()).toMatchObject({
      path: '/api/v1/recordings?state=failed',
      method: 'GET',
      auth: 'Bearer tok',
    })
  })

  it('schedules a guide program', async () => {
    fetchMock.mockResolvedValueOnce(json({ recording: { id: 1 }, warnings: [] }, 201))
    const res = await client.createRecording({ channelId: 3, programStart: '2026-10-05T00:00:00Z' })
    expect(res.recording.id).toBe(1)
    expect(call()).toMatchObject({
      path: '/api/v1/recordings',
      method: 'POST',
      body: { channelId: 3, programStart: '2026-10-05T00:00:00Z' },
    })
  })

  it('passes a 409 conflict body through on ApiError', async () => {
    const body = { error: 'tuner conflict', tunerCount: 2, conflicts: [{ id: 4, title: 'News' }] }
    fetchMock.mockResolvedValueOnce(json(body, 409))
    const err = await client
      .createRecording({ channelId: 3, programStart: '2026-10-05T00:00:00Z' })
      .catch((e: unknown) => e)
    expect(err).toBeInstanceOf(ApiError)
    expect((err as ApiError).status).toBe(409)
    expect((err as ApiError).body).toEqual(body)
  })

  it('protects, stops, deletes and saves positions', async () => {
    fetchMock
      .mockResolvedValueOnce(json({ id: 7, protected: true }))
      .mockResolvedValueOnce(new Response(null, { status: 204 }))
      .mockResolvedValueOnce(new Response(null, { status: 204 }))
      .mockResolvedValueOnce(new Response(null, { status: 204 }))
    await client.patchRecording(7, { protected: true })
    await client.stopRecording(7)
    await client.deleteRecording(7)
    await client.setRecordingPosition(7, 412)
    expect(call(0)).toMatchObject({
      path: '/api/v1/recordings/7',
      method: 'PATCH',
      body: { protected: true },
    })
    expect(call(1)).toMatchObject({ path: '/api/v1/recordings/7/stop', method: 'POST' })
    expect(call(2)).toMatchObject({ path: '/api/v1/recordings/7', method: 'DELETE' })
    expect(call(3)).toMatchObject({
      path: '/api/v1/recordings/7/position',
      method: 'PUT',
      body: { positionSec: 412 },
    })
  })

  it('gets a playback URL', async () => {
    fetchMock.mockResolvedValueOnce(
      json({
        playlistUrl: '/api/v1/recordings/7/hls/index.m3u8?token=x',
        positionSec: 412,
        durationSec: 1980,
      }),
    )
    const res = await client.playRecording(7)
    expect(res.positionSec).toBe(412)
    expect(call()).toMatchObject({ path: '/api/v1/recordings/7/play', method: 'POST' })
  })
})

describe('ApiClient search, series rules, feed and quick sign-in', () => {
  let client: ApiClient
  let fetchMock: ReturnType<typeof vi.fn>

  beforeEach(() => {
    client = new ApiClient(
      () => 'tok',
      () => {},
    )
    fetchMock = vi.fn()
    vi.stubGlobal('fetch', fetchMock)
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  const json = (body: unknown, status = 200) =>
    new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
  const noContent = () => new Response(null, { status: 204 })

  const call = (i = 0) => {
    const [path, init] = fetchMock.mock.calls[i] as [string, RequestInit]
    return {
      path,
      method: init.method,
      body: init.body ? JSON.parse(String(init.body)) : undefined,
      auth: (init.headers as Record<string, string>).Authorization,
    }
  }

  it('searches the guide with an encoded query and limit', async () => {
    fetchMock.mockResolvedValueOnce(json([]))
    await client.searchGuide('law & order', 25)
    expect(call()).toMatchObject({
      path: '/api/v1/guide/search?q=law+%26+order&limit=25',
      method: 'GET',
      auth: 'Bearer tok',
    })
  })

  it('lists, creates and deletes recording rules', async () => {
    fetchMock
      .mockResolvedValueOnce(json([]))
      .mockResolvedValueOnce(json({ rule: { id: 3 }, scheduled: 4 }, 201))
      .mockResolvedValueOnce(noContent())
    await client.listRecordingRules()
    const res = await client.createRecordingRule({
      channelId: 2,
      programStart: '2026-10-04T19:00:00Z',
      anyChannel: false,
      newOnly: true,
      keepLatest: 5,
    })
    await client.deleteRecordingRule(3)
    expect(res.scheduled).toBe(4)
    expect(call(0)).toMatchObject({ path: '/api/v1/recording-rules', method: 'GET' })
    expect(call(1)).toMatchObject({
      path: '/api/v1/recording-rules',
      method: 'POST',
      body: {
        channelId: 2,
        programStart: '2026-10-04T19:00:00Z',
        anyChannel: false,
        newOnly: true,
        keepLatest: 5,
      },
    })
    expect(call(2)).toMatchObject({ path: '/api/v1/recording-rules/3', method: 'DELETE' })
  })

  it('creates and turns off the IPTV feed', async () => {
    fetchMock
      .mockResolvedValueOnce(json({ key: 'k', m3uUrl: 'http://h/a.m3u', xmltvUrl: 'http://h/g.xml' }))
      .mockResolvedValueOnce(noContent())
    const feed = await client.createFeed()
    await client.deleteFeed()
    expect(feed.m3uUrl).toBe('http://h/a.m3u')
    expect(call(0)).toMatchObject({ path: '/api/v1/me/feed', method: 'POST' })
    expect(call(1)).toMatchObject({ path: '/api/v1/me/feed', method: 'DELETE' })
  })

  it('looks up and approves a quick sign-in code', async () => {
    fetchMock
      .mockResolvedValueOnce(json({ deviceName: 'Living room' }))
      .mockResolvedValueOnce(noContent())
    const d = await client.lookupDevice('BCDF2345')
    await client.approveDevice('BCDF2345')
    expect(d.deviceName).toBe('Living room')
    expect(call(0)).toMatchObject({ path: '/api/v1/auth/device/BCDF2345', method: 'GET', auth: 'Bearer tok' })
    expect(call(1)).toMatchObject({
      path: '/api/v1/auth/device/approve',
      method: 'POST',
      body: { userCode: 'BCDF2345' },
    })
  })
})

describe('ApiClient DVR admin', () => {
  let client: ApiClient
  let fetchMock: ReturnType<typeof vi.fn>

  beforeEach(() => {
    client = new ApiClient(
      () => 'tok',
      () => {},
    )
    fetchMock = vi.fn()
    vi.stubGlobal('fetch', fetchMock)
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  const json = (body: unknown, status = 200) =>
    new Response(JSON.stringify(body), {
      status,
      headers: { 'Content-Type': 'application/json' },
    })

  it('reads recording storage', async () => {
    const storage = {
      dir: '/var/lib/bowtie/recordings',
      usedBytes: 10,
      freeBytes: 20,
      totalBytes: 40,
      floorBytes: 2,
      minFreeBytes: 0,
      recordings: { ready: 1, scheduled: 2, recording: 0, failed: 3 },
    }
    fetchMock.mockResolvedValueOnce(json(storage))
    await expect(client.getDVRStorage()).resolves.toEqual(storage)
    const [path, init] = fetchMock.mock.calls[0] as [string, RequestInit]
    expect(path).toBe('/api/v1/admin/dvr/storage')
    expect(init.method).toBe('GET')
  })

  it('surfaces 503 when recording is unavailable', async () => {
    fetchMock.mockResolvedValueOnce(json({ error: 'recording is not available' }, 503))
    const err = await client.getDVRStorage().catch((e: unknown) => e)
    expect(err).toBeInstanceOf(ApiError)
    expect((err as ApiError).status).toBe(503)
  })

  it('asks the server to find commercials again', async () => {
    fetchMock.mockResolvedValueOnce(new Response(null, { status: 202 }))
    await client.redetectCommercials(42)
    const [path, init] = fetchMock.mock.calls[0] as [string, RequestInit]
    expect(path).toBe('/api/v1/recordings/42/commercials/detect')
    expect(init.method).toBe('POST')
  })

  it('downloads a database backup with its file name', async () => {
    fetchMock.mockResolvedValueOnce(
      new Response(new Uint8Array([83, 81, 76]), {
        status: 200,
        headers: { 'Content-Disposition': 'attachment; filename="bowtie-backup-20261004-0230.db"' },
      }),
    )
    const b = await client.downloadBackup()
    expect(b.filename).toBe('bowtie-backup-20261004-0230.db')
    expect(b.blob.size).toBe(3)
    const [path, init] = fetchMock.mock.calls[0] as [string, RequestInit]
    expect(path).toBe('/api/v1/admin/backup')
    expect((init.headers as Record<string, string>).Authorization).toBe('Bearer tok')
  })

  it('surfaces backup errors', async () => {
    fetchMock.mockResolvedValueOnce(json({ error: 'backup failed' }, 500))
    const err = await client.downloadBackup().catch((e: unknown) => e)
    expect(err).toBeInstanceOf(ApiError)
    expect((err as ApiError).message).toBe('backup failed')
  })

  it('saves the dvr settings section', async () => {
    fetchMock.mockResolvedValueOnce(json({ dvr: { padStartSeconds: 120, padEndSeconds: 600 } }))
    const res = await client.putSettings({ dvr: { padStartSeconds: 120, padEndSeconds: 600 } })
    expect(res.dvr).toEqual({ padStartSeconds: 120, padEndSeconds: 600 })
    const [path, init] = fetchMock.mock.calls[0] as [string, RequestInit]
    expect(path).toBe('/api/v1/admin/settings')
    expect(init.method).toBe('PUT')
    expect(JSON.parse(String(init.body))).toEqual({
      dvr: { padStartSeconds: 120, padEndSeconds: 600 },
    })
  })
})
