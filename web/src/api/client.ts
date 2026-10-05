export interface User {
  id: number
  username: string
  role: 'admin' | 'viewer'
  maxQuality: string
  /** Concurrent streams allowed; 0 = no limit. */
  maxStreams: number
  /** Tuners the account may use alone; 0 = no limit. */
  maxTuners: number
  /** Parental controls: channels this account may see (null = every channel). */
  allowedChannelIds?: number[] | null
  /** Parental controls: highest rating that plays ("" = no limit). */
  maxRating?: string
  /** Parental controls: also block programs without a rating. */
  blockUnrated?: boolean
}

export interface LoginResponse {
  accessToken: string
  refreshToken: string
  user: User
}

/** Last tune outcome, learned by the server from real tunes. */
export type Reception = 'ok' | 'noSignal' | 'unknown'

export interface ViewerChannel {
  id: number
  guideNumber: string
  name: string
  logoUrl: string
  reception: Reception
  receptionCheckedAt?: string
  /** Starred by the caller. Absent on servers older than Favorites/Recents. */
  favorite?: boolean
}

export interface GuideProgram {
  start: string
  stop: string
  title: string
  subtitle: string
  description: string
  category: string
  /** Guide program ID of the episode (when the source has one). */
  programId?: string
  /** Series ID of the show (when the source has one). */
  seriesId?: string
  /** First airing. */
  isNew?: boolean
  /** Rating from the guide source (e.g. TV-14; "" = not rated). */
  rating?: string
  /** Parental controls block this program for the caller (description is hidden). */
  locked?: boolean
  /** Present when this program is scheduled, recording or recorded. */
  recording?: GuideRecordingMark
}

/** GET /api/v1/guide/search item: an on-now or upcoming program. */
export interface GuideSearchResult {
  channelId: number
  guideNumber: string
  channelName: string
  logoUrl: string
  start: string
  stop: string
  title: string
  subtitle: string
  description: string
  category: string
  rating?: string
  locked?: boolean
  recording?: GuideRecordingMark
}

// ── DVR (OpenAPI tag dvr) ──────────────────────────────────────────────────

/** GuideProgram.recording: the recording that covers this program. */
export interface GuideRecordingMark {
  id: number
  state: string
}

export type RecordingState =
  | 'scheduled'
  | 'waiting'
  | 'recording'
  | 'converting'
  | 'ready'
  | 'failed'

export type RecordingFailure = '' | 'noTuner' | 'noSignal' | 'diskFull' | 'error' | 'skipped'

export interface Recording {
  id: number
  title: string
  subtitle: string
  description: string
  category: string
  channelId: number
  channelName: string
  start: string
  stop: string
  /** waiting = inside its window, retrying for a free tuner. */
  state: RecordingState
  /** More than a minute is missing. */
  partial: boolean
  failure: RecordingFailure
  failureDetail: string
  durationSec: number
  sizeBytes: number
  /** Never deleted automatically when space runs low. */
  protected: boolean
  /** The caller's resume position. */
  positionSec: number
  /** When the caller last saved a position (RFC 3339); absent if never (or an older server). */
  positionUpdatedAt?: string
  scheduledBy: string
  /** The caller may stop, delete or protect it (scheduler or admin). */
  canManage: boolean
  /** The program's rating when scheduled ("" = not rated). */
  rating?: string
  /** Series rule that scheduled it (0 = one-off). */
  ruleId?: number
  /** Parental controls block it for the caller (no description; play is 403). */
  locked?: boolean
  /**
   * Commercial breaks found by the server's commercial detection, in seconds
   * on the playback timeline (sorted, non-overlapping). Absent when none were
   * found, detection hasn't run, or the server doesn't have it.
   */
  commercials?: CommercialBreak[]
}

/** One commercial break in a recording (seconds from the start of playback). */
export interface CommercialBreak {
  start: number
  end: number
}

/** A series recording rule (GET/POST /recording-rules). */
export interface RecordingRule {
  id: number
  title: string
  seriesId: string
  /** 0 = any channel. */
  channelId: number
  channelName: string
  newOnly: boolean
  /** Keep only this many recordings (0 = all). */
  keepLatest: number
  scheduledBy: string
  canManage: boolean
  createdAt: string
}

export interface CreateRecordingRuleRequest {
  channelId: number
  programStart: string
  anyChannel: boolean
  newOnly: boolean
  keepLatest: number
}

export interface CreateRecordingRuleResponse {
  rule: RecordingRule
  /** Upcoming airings scheduled now. */
  scheduled: number
}

/** POST /me/feed: IPTV links for other apps. */
export interface FeedResponse {
  key: string
  m3uUrl: string
  xmltvUrl: string
}

/** GET /auth/device/{code}: the device asking to sign in. */
export interface DeviceLookup {
  deviceName: string
}

/** GET /recordings?state= filter. */
export type RecordingsFilter = 'upcoming' | 'recorded' | 'failed'

/** POST /recordings: a guide program, or a manual window. */
export type CreateRecordingRequest =
  | { channelId: number; programStart: string; force?: boolean }
  | { channelId: number; start: string; stop: string; title?: string; force?: boolean }

export interface RecordingWarning {
  code: 'usesAllTuners' | string
  message: string
}

export interface CreateRecordingResponse {
  recording: Recording
  warnings: RecordingWarning[]
}

/** 409 body from POST /recordings (on ApiError.body). */
export interface RecordingConflict {
  error: string
  tunerCount: number
  conflicts: Recording[]
}

export interface PlayRecordingResponse {
  playlistUrl: string
  positionSec: number
  durationSec: number
}

export interface GuideChannel {
  channelId: number
  guideNumber: string
  name: string
  logoUrl: string
  reception: Reception
  receptionCheckedAt?: string
  /** Starred by the caller. Absent on servers older than Favorites/Recents. */
  favorite?: boolean
  programs: GuideProgram[]
}

/** GET /api/v1/me/recents item, newest first. */
export interface RecentChannel {
  channelId: number
  guideNumber: string
  name: string
  logoUrl: string
  watchedAt: string
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

/** GET /admin/tuners envelope (devices + ingest channel IDs). */
export interface AdminTunersResponse {
  devices: DeviceStatus[]
  ingestChannels: number[]
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
  /** Free HDHomeRun guide (absent on older servers). */
  hdhomerun?: EPGSourceState
}

export interface EPGChannel {
  id: string
  displayName: string
  callsign: string
  iconUrl: string
  source: 'xmltv' | 'sd' | 'hdhomerun'
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
  maxStreams?: number
  maxTuners?: number
}

export interface PatchUserRequest {
  role?: UserRole
  maxQuality?: string
  maxStreams?: number
  maxTuners?: number
  password?: string
  /** null = every channel; omit to keep. */
  allowedChannelIds?: number[] | null
  maxRating?: string
  blockUnrated?: boolean
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
  adaptive?: boolean
}

export interface SettingsHDHomeRun {
  /** Fetch the free guide from SiliconDust's HDHomeRun XMLTV API. */
  enabled: boolean
}

export interface SettingsDVR {
  /** Start recording this many seconds early (0–1800). */
  padStartSeconds: number
  /** Keep recording this many seconds after (0–3600). */
  padEndSeconds: number
  /**
   * Resolution recordings are converted to (applies to ones converted
   * afterwards). Absent on servers older than the setting; optional in PUT.
   */
  quality?: RecordingQuality
}

/** Which admin notifications are sent. */
export interface NotificationEvents {
  recordingFailed: boolean
  diskLow: boolean
  recordingReady: boolean
  guideFailed: boolean
}

/** Admin notifications: ntfy, a Discord webhook, or any JSON-accepting URL. */
export interface SettingsNotifications {
  /** Empty = off. */
  url: string
  events: NotificationEvents
}

/** POST /api/v1/admin/notifications/test result (200 even when delivery failed). */
export interface NotificationTestResult {
  target: 'ntfy' | 'discord' | 'webhook'
  ok: boolean
  /** HTTP status the target answered (absent when it didn't answer). */
  status?: number
  error?: string
}

/** dvr.quality: 720p for every channel, or up to 1080p (1080i deinterlaced). */
export type RecordingQuality = '720p' | '1080p'

/** GET /api/v1/admin/settings */
export interface Settings {
  xmltv: SettingsXMLTV
  schedulesDirect: SettingsSchedulesDirect
  transcode: SettingsTranscode
  streaming: SettingsStreaming
  /** Absent on servers older than the HDHomeRun guide. */
  hdhomerun?: SettingsHDHomeRun
  /** Absent on servers older than recording padding settings. */
  dvr?: SettingsDVR
  /** Absent on servers older than notifications. */
  notifications?: SettingsNotifications
}

/** PUT /api/v1/admin/settings — section merge; omit sections to leave untouched. */
export interface PutSettingsRequest {
  xmltv?: { source: string; refreshHours: number }
  schedulesDirect?: { username: string; password?: string; lineupId: string }
  transcode?: { encoder: string; allowHevc: boolean }
  streaming?: { bufferMinutes: number; adaptive?: boolean }
  hdhomerun?: SettingsHDHomeRun
  dvr?: SettingsDVR
  /** url is required; events (and each event) are optional. */
  notifications?: { url: string; events?: Partial<NotificationEvents> }
}

/** GET /api/v1/admin/dvr/storage (503 when recording isn't available). */
export interface DVRStorage {
  dir: string
  /** Ready recordings plus files of in-progress ones. */
  usedBytes: number
  freeBytes: number
  totalBytes: number
  /** New captures don't start below this much free space. */
  floorBytes: number
  /** The retention sweep deletes old recordings below this (0 = off). */
  minFreeBytes: number
  recordings: { ready: number; scheduled: number; recording: number; failed: number }
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

  /** On-now and upcoming programs whose title, episode or description matches q. */
  async searchGuide(q: string, limit = 50): Promise<GuideSearchResult[]> {
    const params = new URLSearchParams({ q, limit: String(limit) })
    return this.request<GuideSearchResult[]>('GET', `/api/v1/guide/search?${params}`)
  }

  async getChannels(): Promise<ViewerChannel[]> {
    return this.request<ViewerChannel[]>('GET', '/api/v1/channels')
  }

  // ── IPTV feed (per user) ─────────────────────────────────────────────────

  /** Creates or rotates the caller's feed key; the old links stop working. */
  async createFeed(): Promise<FeedResponse> {
    return this.request<FeedResponse>('POST', '/api/v1/me/feed')
  }

  async deleteFeed(): Promise<void> {
    await this.request<void>('DELETE', '/api/v1/me/feed')
  }

  // ── Quick sign-in (approve a TV) ─────────────────────────────────────────

  /** 404 when the code expired or doesn't exist. */
  async lookupDevice(userCode: string): Promise<DeviceLookup> {
    return this.request<DeviceLookup>('GET', `/api/v1/auth/device/${encodeURIComponent(userCode)}`)
  }

  async approveDevice(userCode: string): Promise<void> {
    await this.request<void>('POST', '/api/v1/auth/device/approve', { userCode })
  }

  // ── Favorites / recents (per user) ───────────────────────────────────────

  async addFavorite(channelId: number): Promise<void> {
    await this.request<void>('PUT', `/api/v1/me/favorites/${channelId}`)
  }

  async removeFavorite(channelId: number): Promise<void> {
    await this.request<void>('DELETE', `/api/v1/me/favorites/${channelId}`)
  }

  /** Newest first. Throws ApiError 404 on servers without recents. */
  async getRecents(limit = 8): Promise<RecentChannel[]> {
    const q = new URLSearchParams({ limit: String(limit) })
    return this.request<RecentChannel[]>('GET', `/api/v1/me/recents?${q}`)
  }

  async clearRecents(): Promise<void> {
    await this.request<void>('DELETE', '/api/v1/me/recents')
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

  /**
   * POST /api/v1/sessions/{viewerId}/heartbeat with the stream token query param.
   * Prefer token over Bearer so mid-session access-token refresh cannot race the beat.
   * Best-effort: non-204 responses throw ApiError; callers may ignore.
   */
  async heartbeat(viewerId: string, streamToken: string): Promise<void> {
    const q = new URLSearchParams({ token: streamToken })
    const path = `/api/v1/sessions/${encodeURIComponent(viewerId)}/heartbeat?${q}`
    // No Authorization header — stream token alone authorizes (spec C).
    const res = await fetch(path, { method: 'POST' })
    if (res.status === 204) {
      return
    }
    let msg = res.statusText
    let body: unknown
    try {
      const text = await res.text()
      if (text) {
        const data = JSON.parse(text) as { error?: string }
        body = data
        if (data.error) msg = data.error
      }
    } catch {
      // ignore parse errors
    }
    throw new ApiError(res.status, msg || 'heartbeat failed', body)
  }

  // ── DVR ──────────────────────────────────────────────────────────────────

  async listRecordingRules(): Promise<RecordingRule[]> {
    return this.request<RecordingRule[]>('GET', '/api/v1/recording-rules')
  }

  /** Records a show from one of its guide programs; schedules upcoming airings now. */
  async createRecordingRule(body: CreateRecordingRuleRequest): Promise<CreateRecordingRuleResponse> {
    return this.request<CreateRecordingRuleResponse>('POST', '/api/v1/recording-rules', body)
  }

  /** Stops recording a show: upcoming recordings are cancelled, recorded ones stay. */
  async deleteRecordingRule(id: number): Promise<void> {
    await this.request<void>('DELETE', `/api/v1/recording-rules/${id}`)
  }

  async listRecordings(state?: RecordingsFilter): Promise<Recording[]> {
    const q = state ? `?${new URLSearchParams({ state })}` : ''
    return this.request<Recording[]>('GET', `/api/v1/recordings${q}`)
  }

  /** 409 → ApiError whose body is a RecordingConflict; resend with force. */
  async createRecording(body: CreateRecordingRequest): Promise<CreateRecordingResponse> {
    return this.request<CreateRecordingResponse>('POST', '/api/v1/recordings', body)
  }

  async patchRecording(id: number, body: { protected?: boolean }): Promise<Recording> {
    return this.request<Recording>('PATCH', `/api/v1/recordings/${id}`, body)
  }

  /** Cancels a scheduled recording, or deletes a recording and its files. */
  async deleteRecording(id: number): Promise<void> {
    await this.request<void>('DELETE', `/api/v1/recordings/${id}`)
  }

  /** Stops a recording now and keeps what was recorded. */
  async stopRecording(id: number): Promise<void> {
    await this.request<void>('POST', `/api/v1/recordings/${id}/stop`)
  }

  async playRecording(id: number): Promise<PlayRecordingResponse> {
    return this.request<PlayRecordingResponse>('POST', `/api/v1/recordings/${id}/play`)
  }

  async setRecordingPosition(id: number, positionSec: number): Promise<void> {
    await this.request<void>('PUT', `/api/v1/recordings/${id}/position`, {
      positionSec: Math.max(0, Math.floor(positionSec)),
    })
  }

  // ── Admin endpoints ──────────────────────────────────────────────────────

  async getAdminTuners(): Promise<DeviceStatus[]> {
    const body = await this.request<AdminTunersResponse>('GET', '/api/v1/admin/tuners')
    return body.devices ?? []
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

  /**
   * Admin: send a test notification to `url` (with `username`/`password`; an
   * empty password uses the saved one), or to the saved destination when
   * omitted. Resolves with the delivery result (ok=false when it failed).
   */
  async testNotification(
    url?: string,
    creds?: { username: string; password: string },
  ): Promise<NotificationTestResult> {
    return this.request<NotificationTestResult>(
      'POST',
      '/api/v1/admin/notifications/test',
      url ? { url, ...(creds ?? {}) } : {},
    )
  }

  /** Admin: run commercial detection on a recording again. */
  async redetectCommercials(id: number): Promise<void> {
    await this.request<void>('POST', `/api/v1/recordings/${id}/commercials/detect`)
  }

  async getDVRStorage(): Promise<DVRStorage> {
    return this.request<DVRStorage>('GET', '/api/v1/admin/dvr/storage')
  }

  async getEPGLineups(): Promise<SDLineupSummary[]> {
    return this.request<SDLineupSummary[]>('GET', '/api/v1/admin/epg/lineups')
  }

  /** Admin: lineups available in a postal code (antenna first). */
  async searchEPGLineups(country: string, postalCode: string): Promise<SDLineupSummary[]> {
    const q = new URLSearchParams({ country, postalcode: postalCode })
    return this.request<SDLineupSummary[]>('GET', `/api/v1/admin/epg/headends?${q}`)
  }

  /** Admin: add a lineup to the Schedules Direct account. */
  async addEPGLineup(lineupId: string): Promise<void> {
    await this.request<void>('POST', '/api/v1/admin/epg/lineups', { lineupId })
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
    return this.parseJSON<T>(await this.authedFetch(method, path, body))
  }

  /** Database backup (admin): the SQLite file and its suggested name. */
  async downloadBackup(): Promise<{ blob: Blob; filename: string }> {
    const res = await this.authedFetch('GET', '/api/v1/admin/backup')
    if (!res.ok) {
      await this.parseJSON<never>(res)
    }
    const m = /filename="([^"]+)"/.exec(res.headers.get('Content-Disposition') ?? '')
    return { blob: await res.blob(), filename: m ? m[1] : 'bowtie-backup.db' }
  }

  private async authedFetch(method: string, path: string, body?: unknown): Promise<Response> {
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
    return res
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
      throw new ApiError(res.status, msg, data)
    }
    return data as T
  }
}

export class ApiError extends Error {
  status: number
  /** Parsed JSON error body, when the server sent one. */
  body?: unknown

  constructor(status: number, message: string, body?: unknown) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.body = body
  }
}
