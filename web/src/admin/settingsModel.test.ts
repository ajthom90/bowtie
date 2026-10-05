import { describe, expect, it } from 'vitest'
import {
  DEFAULT_NOTIFICATION_EVENTS,
  NOTIFICATIONS_HINT,
  NOTIFICATIONS_PLACEHOLDER,
  NOTIFICATION_EVENT_OPTIONS,
  buildSectionPayload,
  describeTestResult,
  validateLineupSearch,
  notificationTarget,
  notificationTargetLabel,
  validateNotificationsHint,
  buildSchedulesDirectPayload,
  buildStreamingPayload,
  buildTranscodePayload,
  buildXmltvPayload,
  encoderOptions,
  lineupOptionLabel,
  parseBufferMinutes,
  parseRefreshHours,
  settingsToForm,
  validateStreamingHint,
  validateTranscodeHint,
  validateXmltvHint,
  type SettingsFormState,
  type SettingsResponse,
} from './settingsModel'

function sampleSettings(overrides: Partial<SettingsResponse> = {}): SettingsResponse {
  return {
    xmltv: { source: 'https://example.com/guide.xml', refreshHours: 12 },
    schedulesDirect: {
      username: 'sduser',
      passwordConfigured: true,
      lineupId: 'USA-CA12345-X',
    },
    transcode: {
      encoder: 'auto',
      allowHevc: false,
      available: ['software', 'videotoolbox'],
      hevcCapable: { software: true, videotoolbox: true },
    },
    streaming: { bufferMinutes: 15 },
    ...overrides,
  }
}

function formFrom(s: SettingsResponse = sampleSettings()): SettingsFormState {
  return settingsToForm(s)
}

describe('settingsToForm', () => {
  it('maps GET response; password field starts empty', () => {
    const form = formFrom()
    expect(form.xmltv.source).toBe('https://example.com/guide.xml')
    expect(form.xmltv.refreshHours).toBe('12')
    expect(form.schedulesDirect.username).toBe('sduser')
    expect(form.schedulesDirect.password).toBe('')
    expect(form.schedulesDirect.passwordConfigured).toBe(true)
    expect(form.schedulesDirect.lineupId).toBe('USA-CA12345-X')
    expect(form.transcode.encoder).toBe('auto')
    expect(form.transcode.allowHevc).toBe(false)
    expect(form.transcode.available).toEqual(['software', 'videotoolbox'])
    expect(form.streaming.bufferMinutes).toBe('15')
  })
})

describe('buildSectionPayload — per-section merge', () => {
  it('xmltv payload includes only xmltv', () => {
    const form = formFrom()
    form.xmltv.source = '  /data/guide.xml  '
    form.xmltv.refreshHours = '6'
    const body = buildSectionPayload('xmltv', form)
    expect(body).toEqual({
      xmltv: { source: '/data/guide.xml', refreshHours: 6 },
    })
    expect(body.schedulesDirect).toBeUndefined()
    expect(body.transcode).toBeUndefined()
    expect(body.streaming).toBeUndefined()
  })

  it('transcode payload includes only transcode', () => {
    const form = formFrom()
    form.transcode.encoder = 'software'
    form.transcode.allowHevc = true
    const body = buildTranscodePayload(form)
    expect(body).toEqual({
      transcode: { encoder: 'software', allowHevc: true },
    })
    expect(Object.keys(body)).toEqual(['transcode'])
  })

  it('schedulesDirect payload includes only schedulesDirect', () => {
    const form = formFrom()
    const body = buildSectionPayload('schedulesDirect', form)
    expect(body.xmltv).toBeUndefined()
    expect(body.transcode).toBeUndefined()
    expect(body.streaming).toBeUndefined()
    expect(body.schedulesDirect).toBeDefined()
  })

  it('streaming payload includes only streaming (full section peer)', () => {
    const form = formFrom()
    form.streaming.bufferMinutes = '30'
    const body = buildSectionPayload('streaming', form)
    expect(body).toEqual({
      streaming: { bufferMinutes: 30, adaptive: false },
    })
    expect(body.xmltv).toBeUndefined()
    expect(body.schedulesDirect).toBeUndefined()
    expect(body.transcode).toBeUndefined()
    expect(Object.keys(body)).toEqual(['streaming'])
  })
})

describe('password omit / include', () => {
  it('omits password when the field is empty (keep stored)', () => {
    const form = formFrom()
    form.schedulesDirect.password = ''
    const body = buildSchedulesDirectPayload(form)
    expect(body.schedulesDirect).toEqual({
      username: 'sduser',
      lineupId: 'USA-CA12345-X',
    })
    expect(body.schedulesDirect).not.toHaveProperty('password')
  })

  it('includes password when non-empty (replace)', () => {
    const form = formFrom()
    form.schedulesDirect.password = 'new-secret'
    const body = buildSchedulesDirectPayload(form)
    expect(body.schedulesDirect).toEqual({
      username: 'sduser',
      password: 'new-secret',
      lineupId: 'USA-CA12345-X',
    })
  })
})

describe('clear-SD path', () => {
  it('empty username sends section with empty strings (clears trio server-side)', () => {
    const form = formFrom()
    form.schedulesDirect.username = ''
    form.schedulesDirect.lineupId = 'USA-CA12345-X'
    form.schedulesDirect.password = ''
    const body = buildSchedulesDirectPayload(form)
    expect(body).toEqual({
      schedulesDirect: {
        username: '',
        lineupId: '',
      },
    })
    expect(body.schedulesDirect).not.toHaveProperty('password')
  })

  it('empty username with leftover password still clears (username drives clear)', () => {
    const form = formFrom()
    form.schedulesDirect.username = '   '
    form.schedulesDirect.password = 'ignored-on-clear'
    form.schedulesDirect.lineupId = 'X'
    const body = buildSchedulesDirectPayload(form)
    // password is non-empty so it is included, but server clears trio on empty username
    expect(body.schedulesDirect?.username).toBe('')
    expect(body.schedulesDirect?.lineupId).toBe('')
  })
})

describe('buildXmltvPayload', () => {
  it('trims source and parses refresh hours', () => {
    const form = formFrom()
    form.xmltv.source = ''
    form.xmltv.refreshHours = '24'
    expect(buildXmltvPayload(form)).toEqual({
      xmltv: { source: '', refreshHours: 24 },
    })
  })
})

describe('parseRefreshHours / validation hints', () => {
  it('parseRefreshHours accepts integers only', () => {
    expect(parseRefreshHours('12')).toBe(12)
    expect(parseRefreshHours(' 3 ')).toBe(3)
    expect(parseRefreshHours('1.5')).toBeNull()
    expect(parseRefreshHours('abc')).toBeNull()
  })

  it('validateXmltvHint covers range and source shape', () => {
    expect(validateXmltvHint('', '12')).toBeNull()
    expect(validateXmltvHint('https://x.test/g.xml', '12')).toBeNull()
    expect(validateXmltvHint('/abs/path', '1')).toBeNull()
    expect(validateXmltvHint('relative', '12')).toMatch(/empty|http|absolute/i)
    expect(validateXmltvHint('', '0')).toMatch(/1 and 168/)
    expect(validateXmltvHint('', '200')).toMatch(/1 and 168/)
  })

  it('validateTranscodeHint requires auto or probed backend', () => {
    expect(validateTranscodeHint('auto', ['software'])).toBeNull()
    expect(validateTranscodeHint('software', ['software'])).toBeNull()
    expect(validateTranscodeHint('nvenc', ['software'])).toMatch(/auto/)
  })
})

describe('streaming section payload + validation', () => {
  it('buildStreamingPayload parses buffer minutes', () => {
    const form = formFrom()
    form.streaming.bufferMinutes = ' 45 '
    expect(buildStreamingPayload(form)).toEqual({
      streaming: { bufferMinutes: 45, adaptive: false },
    })
  })

  it('parseBufferMinutes accepts integers only', () => {
    expect(parseBufferMinutes('15')).toBe(15)
    expect(parseBufferMinutes(' 2 ')).toBe(2)
    expect(parseBufferMinutes('1.5')).toBeNull()
    expect(parseBufferMinutes('abc')).toBeNull()
  })

  it('validateStreamingHint enforces 2–60', () => {
    expect(validateStreamingHint('15')).toBeNull()
    expect(validateStreamingHint('2')).toBeNull()
    expect(validateStreamingHint('60')).toBeNull()
    expect(validateStreamingHint('1')).toMatch(/2 and 60/)
    expect(validateStreamingHint('61')).toMatch(/2 and 60/)
    expect(validateStreamingHint('abc')).toMatch(/whole number/)
  })
})

describe('encoderOptions / lineupOptionLabel', () => {
  it('encoderOptions always leads with auto', () => {
    expect(encoderOptions(['software', 'videotoolbox']).map((o) => o.value)).toEqual([
      'auto',
      'software',
      'videotoolbox',
    ])
  })

  it('lineupOptionLabel joins name/location/transport with id', () => {
    expect(
      lineupOptionLabel({
        lineupId: 'USA-1',
        name: 'Local',
        location: 'LA',
        transport: 'Antenna',
      }),
    ).toBe('Local · LA · Antenna (USA-1)')
    expect(
      lineupOptionLabel({ lineupId: 'USA-2', name: '', location: '', transport: '' }),
    ).toBe('USA-2')
  })
})

describe('streaming.adaptive', () => {
  it('maps GET → form and form → PUT', () => {
    const form = formFrom(sampleSettings({ streaming: { bufferMinutes: 15, adaptive: true } }))
    expect(form.streaming.adaptive).toBe(true)
    form.streaming.adaptive = false
    expect(buildStreamingPayload(form)).toEqual({ streaming: { bufferMinutes: 15, adaptive: false } })
  })
  it('defaults to false when an older server omits it', () => {
    const form = formFrom(sampleSettings({ streaming: { bufferMinutes: 15 } as never }))
    expect(form.streaming.adaptive).toBe(false)
  })
})

describe('HDHomeRun free guide setting', () => {
  it('seeds the toggle from the server', () => {
    expect(formFrom(sampleSettings({ hdhomerun: { enabled: true } })).hdhomerun).toEqual({ enabled: true })
    expect(formFrom(sampleSettings({ hdhomerun: { enabled: false } })).hdhomerun).toEqual({ enabled: false })
  })

  it('is null on servers without the setting (toggle hidden)', () => {
    expect(formFrom(sampleSettings()).hdhomerun).toBeNull()
  })

  it('saves only the hdhomerun section', () => {
    const form = formFrom(sampleSettings({ hdhomerun: { enabled: true } }))
    form.hdhomerun = { enabled: false }
    expect(buildSectionPayload('hdhomerun', form)).toEqual({ hdhomerun: { enabled: false } })
  })
})

describe('notifications', () => {
  const withNotifications = (url: string, events = DEFAULT_NOTIFICATION_EVENTS) =>
    formFrom(sampleSettings({ notifications: { url, events } }))

  it('form is null on servers without notifications', () => {
    expect(formFrom().notifications).toBeNull()
  })

  it('maps the section and fills missing events with the defaults', () => {
    const form = formFrom(
      sampleSettings({
        notifications: {
          url: 'https://ntfy.sh/bowtie',
          events: { recordingReady: true } as unknown as typeof DEFAULT_NOTIFICATION_EVENTS,
        },
      }),
    )
    expect(form.notifications).toEqual({
      url: 'https://ntfy.sh/bowtie',
      username: '',
      password: '',
      passwordConfigured: false,
      clearPassword: false,
      events: { recordingFailed: true, diskLow: true, recordingReady: true, guideFailed: true },
    })
  })

  it('payload sends only notifications, trimmed url and every event', () => {
    const form = withNotifications('  https://ntfy.sh/bowtie  ', {
      recordingFailed: false,
      diskLow: true,
      recordingReady: true,
      guideFailed: false,
    })
    expect(buildSectionPayload('notifications', form)).toEqual({
      notifications: {
        url: 'https://ntfy.sh/bowtie',
        username: '',
        events: { recordingFailed: false, diskLow: true, recordingReady: true, guideFailed: false },
      },
    })
  })

  it('maps the username and whether a password is saved (never the password)', () => {
    const form = formFrom(
      sampleSettings({
        notifications: {
          url: 'https://ntfy.example.com/alerts',
          username: 'alice',
          passwordConfigured: true,
          events: DEFAULT_NOTIFICATION_EVENTS,
        },
      }),
    )
    expect(form.notifications?.username).toBe('alice')
    expect(form.notifications?.passwordConfigured).toBe(true)
    expect(form.notifications?.password).toBe('')
  })

  it('payload sends a typed password, trims the username, and omits an empty password', () => {
    const form = withNotifications('https://ntfy.example.com/alerts')
    form.notifications = { ...form.notifications!, username: ' alice ', password: 'p@ss:w/rd' }
    expect(buildSectionPayload('notifications', form).notifications).toMatchObject({
      username: 'alice',
      password: 'p@ss:w/rd',
    })
    form.notifications = { ...form.notifications!, password: '' }
    const n = buildSectionPayload('notifications', form).notifications!
    expect('password' in n).toBe(false)
    expect('clearPassword' in n).toBe(false)
  })

  it('payload asks to remove the saved password only when no new one is typed', () => {
    const form = withNotifications('https://ntfy.example.com/alerts')
    form.notifications = { ...form.notifications!, passwordConfigured: true, clearPassword: true }
    expect(buildSectionPayload('notifications', form).notifications?.clearPassword).toBe(true)
    form.notifications = { ...form.notifications!, password: 'new' }
    const n = buildSectionPayload('notifications', form).notifications!
    expect(n.password).toBe('new')
    expect('clearPassword' in n).toBe(false)
  })

  it('empty url is allowed (turns notifications off)', () => {
    expect(validateNotificationsHint('')).toBeNull()
    expect(validateNotificationsHint('   ')).toBeNull()
    expect(buildSectionPayload('notifications', withNotifications('')).notifications?.url).toBe('')
  })

  it('url must be http(s)', () => {
    expect(validateNotificationsHint('https://ntfy.sh/x')).toBeNull()
    expect(validateNotificationsHint('http://10.0.0.5:8080/hook')).toBeNull()
    expect(validateNotificationsHint('https://u:p@ntfy.example.com/t')).toBeNull()
    for (const bad of ['ntfy.sh/x', 'ftp://ntfy.sh/x', '/var/hook', 'javascript:alert(1)', 'https://']) {
      expect(validateNotificationsHint(bad)).toBe('Notification URL must be an http(s) URL')
    }
  })

  it('detects the target like the server', () => {
    expect(notificationTarget('https://discord.com/api/v10/webhooks/1/tok')).toBe('discord')
    expect(notificationTarget('https://ntfy.sh/topic')).toBe('ntfy')
    expect(notificationTarget('https://user:pass@ntfy.example.com/t')).toBe('ntfy')
    expect(notificationTarget('https://discord.com/api/webhooks/1/abc')).toBe('discord')
    expect(notificationTarget('https://canary.discordapp.com/api/webhooks/1/abc')).toBe('discord')
    expect(notificationTarget('https://discord.com/channels/1')).toBe('webhook')
    expect(notificationTarget('https://notdiscord.com/api/webhooks/1')).toBe('webhook')
    expect(notificationTarget('https://hooks.example.com/x')).toBe('webhook')
    expect(notificationTarget('')).toBeNull()
    expect(notificationTarget('not a url')).toBeNull()
    expect(notificationTargetLabel('https://ntfy.sh/t')).toBe('Sends as an ntfy notification.')
    expect(notificationTargetLabel('')).toBeNull()
  })

  it('describes test results', () => {
    expect(describeTestResult({ target: 'ntfy', ok: true, status: 200 })).toBe('Test sent.')
    expect(
      describeTestResult({ target: 'webhook', ok: false, status: 404, error: 'h answered 404 Not Found' }),
    ).toBe('Test failed: h answered 404 Not Found')
    expect(describeTestResult({ target: 'webhook', ok: false, status: 500 })).toBe('Test failed: HTTP 500')
    expect(describeTestResult({ target: 'webhook', ok: false })).toBe('Test failed: no answer')
  })

  it('event options cover every event once, with the hint copy', () => {
    expect(NOTIFICATION_EVENT_OPTIONS.map((o) => o.key).sort()).toEqual(
      Object.keys(DEFAULT_NOTIFICATION_EVENTS).sort(),
    )
    expect(NOTIFICATIONS_PLACEHOLDER).toBe('https://ntfy.sh/your-topic')
    expect(NOTIFICATIONS_HINT).toBe(
      'Works with ntfy (free phone app), Discord webhooks, or any URL that accepts a JSON POST.',
    )
  })
})

describe('validateLineupSearch', () => {
  it('accepts a 3-letter country and a postal code, normalizing both', () => {
    expect(validateLineupSearch(' usa ', ' 56071 ')).toEqual({ country: 'USA', postalCode: '56071' })
  })
  it('explains a missing postal code', () => {
    expect(validateLineupSearch('USA', '  ')).toEqual({ hint: 'Enter a ZIP or postal code.' })
  })
  it('explains a country that is not a 3-letter code', () => {
    expect(validateLineupSearch('US', '56071')).toEqual({
      hint: 'Use a 3-letter country code, e.g. USA or CAN.',
    })
  })
})
