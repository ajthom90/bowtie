/**
 * Pure settings form ↔ API payload mapping (v0.4.0 Task 5; v0.5.0 streaming).
 * Server is authority for validation; client hints are advisory only.
 */

export type SettingsSection =
  | 'xmltv'
  | 'schedulesDirect'
  | 'transcode'
  | 'streaming'
  | 'hdhomerun'
  | 'notifications'

/** Admin notification events (GET/PUT notifications.events). */
export interface NotificationEvents {
  recordingFailed: boolean
  diskLow: boolean
  recordingReady: boolean
  guideFailed: boolean
}

/** POST /api/v1/admin/notifications/test result. */
export interface NotificationTestResult {
  target: 'ntfy' | 'discord' | 'webhook'
  ok: boolean
  status?: number
  error?: string
}

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
  /** One shared multi-quality transcode per channel (absent on older servers). */
  adaptive?: boolean
}

/** GET /api/v1/admin/settings response shape. */
export interface SettingsResponse {
  xmltv: SettingsXMLTV
  schedulesDirect: SettingsSchedulesDirect
  transcode: SettingsTranscode
  streaming: SettingsStreaming
  /** Free HDHomeRun guide (absent on older servers). */
  hdhomerun?: { enabled: boolean }
  /** Recording padding (absent on older servers; edited on the Recordings tab). */
  dvr?: { padStartSeconds: number; padEndSeconds: number }
  /** Admin notifications (absent on older servers). */
  notifications?: { url: string; events: NotificationEvents }
}

export interface SDLineupSummary {
  lineupId: string
  name: string
  location: string
  transport: string
}

/** Editable form state (password is write-only; never returned by GET). */
export interface SettingsFormState {
  xmltv: {
    source: string
    refreshHours: string
  }
  schedulesDirect: {
    username: string
    /** Empty = leave stored password unchanged on save. */
    password: string
    lineupId: string
    passwordConfigured: boolean
  }
  transcode: {
    encoder: string
    allowHevc: boolean
    available: string[]
  }
  streaming: {
    bufferMinutes: string
    adaptive: boolean
  }
  /** null when the server has no HDHomeRun guide setting (toggle hidden). */
  hdhomerun: { enabled: boolean } | null
  /** null when the server has no notifications (card hidden). */
  notifications: { url: string; events: NotificationEvents } | null
}

export type PutSettingsRequest = {
  xmltv?: { source: string; refreshHours: number }
  schedulesDirect?: { username: string; password?: string; lineupId: string }
  transcode?: { encoder: string; allowHevc: boolean }
  streaming?: { bufferMinutes: number; adaptive: boolean }
  hdhomerun?: { enabled: boolean }
  notifications?: { url: string; events: NotificationEvents }
}

/** Seed form state from a GET response. Password field starts empty. */
export function settingsToForm(s: SettingsResponse): SettingsFormState {
  return {
    xmltv: {
      source: s.xmltv.source ?? '',
      refreshHours: String(s.xmltv.refreshHours ?? 12),
    },
    schedulesDirect: {
      username: s.schedulesDirect.username ?? '',
      password: '',
      lineupId: s.schedulesDirect.lineupId ?? '',
      passwordConfigured: Boolean(s.schedulesDirect.passwordConfigured),
    },
    transcode: {
      encoder: s.transcode.encoder || 'auto',
      allowHevc: Boolean(s.transcode.allowHevc),
      available: s.transcode.available ? [...s.transcode.available] : [],
    },
    streaming: {
      bufferMinutes: String(s.streaming?.bufferMinutes ?? 15),
      adaptive: Boolean(s.streaming?.adaptive),
    },
    hdhomerun: s.hdhomerun ? { enabled: Boolean(s.hdhomerun.enabled) } : null,
    notifications: s.notifications
      ? {
          url: s.notifications.url ?? '',
          events: { ...DEFAULT_NOTIFICATION_EVENTS, ...s.notifications.events },
        }
      : null,
  }
}

/**
 * Build a section-merge PUT body for the given section only.
 * Other sections are omitted (server leaves them untouched).
 */
export function buildSectionPayload(
  section: SettingsSection,
  form: SettingsFormState,
): PutSettingsRequest {
  switch (section) {
    case 'xmltv':
      return buildXmltvPayload(form)
    case 'schedulesDirect':
      return buildSchedulesDirectPayload(form)
    case 'transcode':
      return buildTranscodePayload(form)
    case 'streaming':
      return buildStreamingPayload(form)
    case 'hdhomerun':
      return { hdhomerun: { enabled: form.hdhomerun?.enabled ?? true } }
    case 'notifications':
      return buildNotificationsPayload(form)
  }
}

/** Server defaults: failures, low disk and stuck guide on; ready recordings off. */
export const DEFAULT_NOTIFICATION_EVENTS: NotificationEvents = {
  recordingFailed: true,
  diskLow: true,
  recordingReady: false,
  guideFailed: true,
}

/** The event checkboxes, in display order. */
export const NOTIFICATION_EVENT_OPTIONS: { key: keyof NotificationEvents; label: string }[] = [
  { key: 'recordingFailed', label: 'A recording failed' },
  { key: 'diskLow', label: 'Disk space is low' },
  { key: 'guideFailed', label: "Guide data hasn't updated for a day" },
  { key: 'recordingReady', label: 'A recording is ready to watch' },
]

export const NOTIFICATIONS_PLACEHOLDER = 'https://ntfy.sh/your-topic'

export const NOTIFICATIONS_HINT =
  'Works with ntfy (free phone app), Discord webhooks, or any URL that accepts a JSON POST.'

export function buildNotificationsPayload(form: SettingsFormState): PutSettingsRequest {
  const n = form.notifications ?? { url: '', events: DEFAULT_NOTIFICATION_EVENTS }
  return { notifications: { url: n.url.trim(), events: { ...n.events } } }
}

/** Client-side hint: empty (off) or an http(s) URL with a host. */
export function validateNotificationsHint(url: string): string | null {
  const u = url.trim()
  if (u === '') return null
  let parsed: URL
  try {
    parsed = new URL(u)
  } catch {
    return 'Notification URL must be an http(s) URL'
  }
  if ((parsed.protocol !== 'http:' && parsed.protocol !== 'https:') || parsed.hostname === '') {
    return 'Notification URL must be an http(s) URL'
  }
  return null
}

/** How the server will format messages for this URL (mirrors the server's rule). */
export function notificationTarget(url: string): 'ntfy' | 'discord' | 'webhook' | null {
  const u = url.trim()
  if (u === '') return null
  let parsed: URL
  try {
    parsed = new URL(u)
  } catch {
    return null
  }
  const host = parsed.hostname.toLowerCase()
  if (host.includes('ntfy')) return 'ntfy'
  const discord = ['discord.com', 'discordapp.com'].some((d) => host === d || host.endsWith(`.${d}`))
  if (discord && /^\/api\/(v\d+\/)?webhooks\//.test(parsed.pathname)) return 'discord'
  return 'webhook'
}

/** Label for the detected target, shown under the URL field. */
export function notificationTargetLabel(url: string): string | null {
  switch (notificationTarget(url)) {
    case 'ntfy':
      return 'Sends as an ntfy notification.'
    case 'discord':
      return 'Sends as a Discord message.'
    case 'webhook':
      return 'Sends a JSON POST (event, title, message, recordingId, time).'
    default:
      return null
  }
}

/** One line describing a test delivery's outcome. */
export function describeTestResult(r: NotificationTestResult): string {
  if (r.ok) return 'Test sent.'
  const why = r.error?.trim() || (r.status ? `HTTP ${r.status}` : 'no answer')
  return `Test failed: ${why}`
}

export function buildXmltvPayload(form: SettingsFormState): PutSettingsRequest {
  const hours = parseRefreshHours(form.xmltv.refreshHours)
  return {
    xmltv: {
      source: form.xmltv.source.trim(),
      refreshHours: hours ?? 12,
    },
  }
}

/**
 * Password rules:
 * - empty password → omit field (server keeps existing)
 * - non-empty → include (replace)
 * Clear-SD: empty username sends section with empty username + lineupId
 * (server clears username, password, and lineupId).
 */
export function buildSchedulesDirectPayload(form: SettingsFormState): PutSettingsRequest {
  const username = form.schedulesDirect.username.trim()
  const lineupId = username === '' ? '' : form.schedulesDirect.lineupId.trim()
  const section: { username: string; password?: string; lineupId: string } = {
    username,
    lineupId,
  }
  const pw = form.schedulesDirect.password
  if (pw !== '') {
    section.password = pw
  }
  return { schedulesDirect: section }
}

export function buildTranscodePayload(form: SettingsFormState): PutSettingsRequest {
  return {
    transcode: {
      encoder: form.transcode.encoder || 'auto',
      allowHevc: form.transcode.allowHevc,
    },
  }
}

export function buildStreamingPayload(form: SettingsFormState): PutSettingsRequest {
  const mins = parseBufferMinutes(form.streaming.bufferMinutes)
  return {
    streaming: {
      bufferMinutes: mins ?? 15,
      adaptive: form.streaming.adaptive,
    },
  }
}

/** Parse refreshHours input; returns null if not a finite integer. */
export function parseRefreshHours(raw: string): number | null {
  const n = Number(raw.trim())
  if (!Number.isFinite(n) || !Number.isInteger(n)) return null
  return n
}

/** Parse bufferMinutes input; returns null if not a finite integer. */
export function parseBufferMinutes(raw: string): number | null {
  const n = Number(raw.trim())
  if (!Number.isFinite(n) || !Number.isInteger(n)) return null
  return n
}

/** Client-side hint only; server enforces 1–168 and source shape. */
export function validateXmltvHint(source: string, refreshHoursRaw: string): string | null {
  const hours = parseRefreshHours(refreshHoursRaw)
  if (hours === null) return 'Refresh hours must be a whole number'
  if (hours < 1 || hours > 168) return 'Refresh hours must be between 1 and 168'
  const s = source.trim()
  if (s === '') return null
  if (s.startsWith('http://') || s.startsWith('https://')) return null
  if (s.startsWith('/')) return null
  return 'Source must be empty, an http(s) URL, or an absolute path'
}

/** Client-side hint: encoder must be "auto" or in available. */
export function validateTranscodeHint(encoder: string, available: string[]): string | null {
  const e = encoder || 'auto'
  if (e === 'auto') return null
  if (available.includes(e)) return null
  return `Encoder must be "auto" or one of: ${available.join(', ') || '(none probed)'}`
}

/** Client-side hint: bufferMinutes 2–60. */
export function validateStreamingHint(bufferMinutesRaw: string): string | null {
  const mins = parseBufferMinutes(bufferMinutesRaw)
  if (mins === null) return 'Buffer minutes must be a whole number'
  if (mins < 2 || mins > 60) return 'Buffer minutes must be between 2 and 60'
  return null
}

/** Encoder dropdown options: always "auto" plus probed backends. */
export function encoderOptions(available: string[]): { value: string; label: string }[] {
  const opts = [{ value: 'auto', label: 'Auto' }]
  for (const b of available) {
    if (b && b !== 'auto' && !opts.some((o) => o.value === b)) {
      opts.push({ value: b, label: b })
    }
  }
  return opts
}

/** Human label for a Schedules Direct lineup option. */
export function lineupOptionLabel(lu: SDLineupSummary): string {
  const parts = [lu.name, lu.location, lu.transport].filter((p) => p && p.trim())
  if (parts.length === 0) return lu.lineupId
  return `${parts.join(' · ')} (${lu.lineupId})`
}

/** Checks a Schedules Direct lineup search (country code + ZIP/postal code). */
export function validateLineupSearch(
  country: string,
  postalCode: string,
): { country: string; postalCode: string } | { hint: string } {
  const c = country.trim().toUpperCase()
  const p = postalCode.trim()
  if (!/^[A-Z]{3}$/.test(c)) return { hint: 'Use a 3-letter country code, e.g. USA or CAN.' }
  if (!p) return { hint: 'Enter a ZIP or postal code.' }
  return { country: c, postalCode: p }
}

/** Password field placeholder when a password is already stored. */
export const PASSWORD_PLACEHOLDER_CONFIGURED = 'unchanged'

export const SAVE_FEEDBACK = 'Saved.'

/** Hint copy for tmpfs sizing relative to buffer length. */
export const STREAMING_TMPFS_HINT =
  '~60 MB per minute per session at top profile. Ensure tmpfs has headroom (e.g. size=4g for multi-session). Applies to new sessions only.'
