/**
 * Pure settings form ↔ API payload mapping (v0.4.0 Task 5).
 * Server is authority for validation; client hints are advisory only.
 */

export type SettingsSection = 'xmltv' | 'schedulesDirect' | 'transcode'

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

/** GET /api/v1/admin/settings response shape. */
export interface SettingsResponse {
  xmltv: SettingsXMLTV
  schedulesDirect: SettingsSchedulesDirect
  transcode: SettingsTranscode
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
}

export type PutSettingsRequest = {
  xmltv?: { source: string; refreshHours: number }
  schedulesDirect?: { username: string; password?: string; lineupId: string }
  transcode?: { encoder: string; allowHevc: boolean }
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
  }
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

/** Parse refreshHours input; returns null if not a finite integer. */
export function parseRefreshHours(raw: string): number | null {
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

/** Password field placeholder when a password is already stored. */
export const PASSWORD_PLACEHOLDER_CONFIGURED = 'unchanged'

export const SAVE_FEEDBACK = 'Saved.'
