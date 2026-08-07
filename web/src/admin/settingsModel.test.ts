import { describe, expect, it } from 'vitest'
import {
  buildSectionPayload,
  buildSchedulesDirectPayload,
  buildTranscodePayload,
  buildXmltvPayload,
  encoderOptions,
  lineupOptionLabel,
  parseRefreshHours,
  settingsToForm,
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
    expect(body.schedulesDirect).toBeDefined()
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
