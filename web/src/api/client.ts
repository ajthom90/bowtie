export interface User {
  id: number
  username: string
  role: 'admin' | 'viewer'
  maxQuality: string
}

export interface LoginResponse {
  accessToken: string
  refreshToken: string
  user: User
}

export interface ViewerChannel {
  id: number
  guideNumber: string
  name: string
  logoUrl: string
}

export interface GuideProgram {
  start: string
  stop: string
  title: string
  subtitle: string
  description: string
  category: string
}

export interface GuideChannel {
  channelId: number
  guideNumber: string
  name: string
  logoUrl: string
  programs: GuideProgram[]
}

export interface ClientCapsPayload {
  videoCodecs: string[]
  audioCodecs: string[]
  maxHeight: number
  profile: string
}

export interface SessionMeta {
  videoCodec?: string
  profile?: string
  backend?: string
  channelName?: string
}

/** POST /api/v1/sessions — session meta may be absent on older servers. */
export interface CreateSessionResponse {
  viewerId: string
  playlistUrl: string
  session?: SessionMeta
}

// ── Admin types (OpenAPI schemas) ──────────────────────────────────────────

export interface Device {
  deviceId: string
  ip: string
  model: string
  tunerCount: number
  manual: boolean
  lastSeen: string
  streamPort: number
}

export interface TunerStatus {
  resource?: string
  vctNumber?: string
  vctName?: string
  frequency?: number
  signalStrengthPercent?: number
  signalQualityPercent?: number
  symbolQualityPercent?: number
  targetIp?: string
}

export interface DeviceStatus {
  device: Device
  reachable: boolean
  tuners: TunerStatus[]
}

export interface AdminChannel {
  id: number
  deviceId: string
  guideNumber: string
  name: string
  enabled: boolean
  epgChannelId: string
}

export interface EPGSourceState {
  configured: boolean
  lastSuccess: string
  lastError: string
  stale: boolean
}

export interface EPGSourceStatus {
  xmltv: EPGSourceState
  sd: EPGSourceState
}

export interface EPGChannel {
  id: string
  displayName: string
  callsign: string
  iconUrl: string
  source: 'xmltv' | 'sd'
}

export interface TranscodeStatus {
  available: string[]
  hevc: Record<string, boolean>
  ffmpegVersion: string
  selected: string
}

export interface ViewerInfo {
  id: string
  username: string
  lastSeen: string
}

export interface SessionInfo {
  id: string
  channelId: number
  channelName: string
  key: string
  videoCodec: string
  profile: string
  backend: string
  viewers: ViewerInfo[]
  startedAt: string
}

export type UserRole = 'admin' | 'viewer'

export interface CreateUserRequest {
  username: string
  password: string
  role: UserRole
  maxQuality?: string
}

export interface PatchUserRequest {
  role?: UserRole
  maxQuality?: string
  password?: string
}

export interface PatchChannelRequest {
  enabled?: boolean
  epgChannelId?: string
}

// ── Admin settings (v0.4.0) ────────────────────────────────────────────────

export interface SettingsXMLTV {
  source: string
  refreshHours: number
}

export interface SettingsSchedulesDirect {
  username: string
  passwordConfigured: boolean
  lineupId: string
}

export interface SettingsTranscode {
  encoder: string
  allowHevc: boolean
  available: string[]
  hevcCapable: Record<string, boolean>
}

export interface SettingsStreaming {
  bufferMinutes: number
}

/** GET /api/v1/admin/settings */
export interface Settings {
  xmltv: SettingsXMLTV
  schedulesDirect: SettingsSchedulesDirect
  transcode: SettingsTranscode
  streaming: SettingsStreaming
}

/** PUT /api/v1/admin/settings — section merge; omit sections to leave untouched. */
export interface PutSettingsRequest {
  xmltv?: { source: string; refreshHours: number }
  schedulesDirect?: { username: string; password?: string; lineupId: string }
  transcode?: { encoder: string; allowHevc: boolean }
  streaming?: { bufferMinutes: number }
}

export interface SDLineupSummary {
  lineupId: string
  name: string
  location: string
  transport: string
}

export type TokenHooks = {
  /** Return the current refresh token (used for silent refresh on 401). */
  getRefreshToken: () => string | null
  /** Persist rotated tokens after a successful refresh. */
  setTokens: (accessToken: string, refreshToken: string) => void
}

/**
 * Typed fetch wrapper for the Bowtie API.
 *
 * Attaches Bearer access token; on 401, performs one refresh + retry, then
 * calls onAuthFail if still unauthorized.
 */
export class ApiClient {
  private getToken: () => string | null
  private onAuthFail: () => void
  private hooks: TokenHooks
  /** In-flight refresh so concurrent 401s share a single rotate call. */
  private refreshPromise: Promise<boolean> | null = null

  constructor(
    getToken: () => string | null,
    onAuthFail: () => void,
    hooks: TokenHooks = {
      getRefreshToken: () => null,
      setTokens: () => {},
    },
  ) {
    this.getToken = getToken
    this.onAuthFail = onAuthFail
    this.hooks = hooks
  }

  async login(username: string, password: string): Promise<LoginResponse> {
    return this.postJSON<LoginResponse>('/api/v1/auth/login', { username, password }, false)
  }

  async refresh(refreshToken: string): Promise<LoginResponse> {
    return this.postJSON<LoginResponse>('/api/v1/auth/refresh', { refreshToken }, false)
  }

  async me(): Promise<User> {
    return this.request<User>('GET', '/api/v1/me')
  }

  async logout(refreshToken: string): Promise<void> {
    const res = await fetch('/api/v1/auth/logout', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ refreshToken }),
    })
    if (!res.ok && res.status !== 204) {
      // Logout is best-effort; ignore body errors.
    }
  }

  async getGuide(start: Date, stop: Date): Promise<GuideChannel[]> {
    const q = new URLSearchParams({
      start: start.toISOString(),
      stop: stop.toISOString(),
    })
    return this.request<GuideChannel[]>('GET', `/api/v1/guide?${q}`)
  }

  async getChannels(): Promise<ViewerChannel[]> {
    return this.request<ViewerChannel[]>('GET', '/api/v1/channels')
  }

  async createSession(
    channelId: number,
    caps: ClientCapsPayload,
  ): Promise<CreateSessionResponse> {
    return this.request<CreateSessionResponse>('POST', '/api/v1/sessions', {
      channelId,
      caps,
    })
  }

  async deleteSession(viewerId: string): Promise<void> {
    await this.request<void>('DELETE', `/api/v1/sessions/${encodeURIComponent(viewerId)}`)
  }

  // ── Admin endpoints ──────────────────────────────────────────────────────

  async getAdminTuners(): Promise<DeviceStatus[]> {
    return this.request<DeviceStatus[]>('GET', '/api/v1/admin/tuners')
  }

  async addDevice(ip: string): Promise<Device> {
    return this.request<Device>('POST', '/api/v1/admin/devices', { ip })
  }

  async deleteDevice(deviceId: string): Promise<void> {
    await this.request<void>('DELETE', `/api/v1/admin/devices/${encodeURIComponent(deviceId)}`)
  }

  async syncChannels(): Promise<void> {
    await this.request<void>('POST', '/api/v1/admin/channels/sync')
  }

  async getAdminChannels(): Promise<AdminChannel[]> {
    return this.request<AdminChannel[]>('GET', '/api/v1/admin/channels')
  }

  async patchChannel(id: number, body: PatchChannelRequest): Promise<AdminChannel> {
    return this.request<AdminChannel>('PATCH', `/api/v1/admin/channels/${id}`, body)
  }

  async getEPGStatus(): Promise<EPGSourceStatus> {
    return this.request<EPGSourceStatus>('GET', '/api/v1/admin/epg/status')
  }

  async refreshEPG(): Promise<void> {
    await this.request<void>('POST', '/api/v1/admin/epg/refresh')
  }

  async getEPGChannels(): Promise<EPGChannel[]> {
    return this.request<EPGChannel[]>('GET', '/api/v1/admin/epg/channels')
  }

  async getTranscodeStatus(): Promise<TranscodeStatus> {
    return this.request<TranscodeStatus>('GET', '/api/v1/admin/transcode')
  }

  async getSettings(): Promise<Settings> {
    return this.request<Settings>('GET', '/api/v1/admin/settings')
  }

  async putSettings(body: PutSettingsRequest): Promise<Settings> {
    return this.request<Settings>('PUT', '/api/v1/admin/settings', body)
  }

  async getEPGLineups(): Promise<SDLineupSummary[]> {
    return this.request<SDLineupSummary[]>('GET', '/api/v1/admin/epg/lineups')
  }

  async getAdminUsers(): Promise<User[]> {
    return this.request<User[]>('GET', '/api/v1/admin/users')
  }

  async createUser(body: CreateUserRequest): Promise<User> {
    return this.request<User>('POST', '/api/v1/admin/users', body)
  }

  async patchUser(id: number, body: PatchUserRequest): Promise<User> {
    return this.request<User>('PATCH', `/api/v1/admin/users/${id}`, body)
  }

  async deleteUser(id: number): Promise<void> {
    await this.request<void>('DELETE', `/api/v1/admin/users/${id}`)
  }

  async getAdminSessions(): Promise<SessionInfo[]> {
    return this.request<SessionInfo[]>('GET', '/api/v1/admin/sessions')
  }

  async terminateSession(sessionId: string): Promise<void> {
    await this.request<void>(
      'DELETE',
      `/api/v1/admin/sessions/${encodeURIComponent(sessionId)}`,
    )
  }

  /**
   * Authenticated request. Attaches Bearer token; on 401, refreshes once and
   * retries. If the retry still fails with 401 (or refresh fails), calls
   * onAuthFail and throws.
   */
  async request<T>(method: string, path: string, body?: unknown): Promise<T> {
    const doFetch = async (): Promise<Response> => {
      const headers: Record<string, string> = {}
      const token = this.getToken()
      if (token) {
        headers['Authorization'] = `Bearer ${token}`
      }
      let initBody: string | undefined
      if (body !== undefined) {
        headers['Content-Type'] = 'application/json'
        initBody = JSON.stringify(body)
      }
      return fetch(path, { method, headers, body: initBody })
    }

    let res = await doFetch()
    if (res.status === 401) {
      const ok = await this.tryRefreshOnce()
      if (ok) {
        res = await doFetch()
      }
      if (res.status === 401 || !ok) {
        this.onAuthFail()
        throw new ApiError(401, 'unauthorized')
      }
    }

    return this.parseJSON<T>(res)
  }

  private async tryRefreshOnce(): Promise<boolean> {
    if (this.refreshPromise) {
      return this.refreshPromise
    }
    this.refreshPromise = (async () => {
      const rt = this.hooks.getRefreshToken()
      if (!rt) {
        return false
      }
      try {
        const data = await this.refresh(rt)
        this.hooks.setTokens(data.accessToken, data.refreshToken)
        return true
      } catch {
        return false
      }
    })()
    try {
      return await this.refreshPromise
    } finally {
      this.refreshPromise = null
    }
  }

  private async postJSON<T>(path: string, body: unknown, auth: boolean): Promise<T> {
    const headers: Record<string, string> = { 'Content-Type': 'application/json' }
    if (auth) {
      const token = this.getToken()
      if (token) {
        headers['Authorization'] = `Bearer ${token}`
      }
    }
    const res = await fetch(path, {
      method: 'POST',
      headers,
      body: JSON.stringify(body),
    })
    return this.parseJSON<T>(res)
  }

  private async parseJSON<T>(res: Response): Promise<T> {
    if (res.status === 204) {
      return undefined as T
    }
    let data: unknown = null
    const text = await res.text()
    if (text) {
      try {
        data = JSON.parse(text)
      } catch {
        if (!res.ok) {
          throw new ApiError(res.status, text || res.statusText)
        }
        throw new ApiError(res.status, 'invalid JSON response')
      }
    }
    if (!res.ok) {
      const msg =
        data && typeof data === 'object' && data !== null && 'error' in data
          ? String((data as { error: unknown }).error)
          : res.statusText
      throw new ApiError(res.status, msg)
    }
    return data as T
  }
}

export class ApiError extends Error {
  status: number

  constructor(status: number, message: string) {
    super(message)
    this.name = 'ApiError'
    this.status = status
  }
}
