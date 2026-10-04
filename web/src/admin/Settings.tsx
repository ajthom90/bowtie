import { useCallback, useEffect, useState, type FormEvent } from 'react'
import { ApiError, type SDLineupSummary } from '../api/client'
import { useAuth } from '../auth/AuthContext'
import {
  PASSWORD_PLACEHOLDER_CONFIGURED,
  SAVE_FEEDBACK,
  STREAMING_TMPFS_HINT,
  buildSectionPayload,
  encoderOptions,
  lineupOptionLabel,
  settingsToForm,
  NOTIFICATIONS_HINT,
  NOTIFICATIONS_PLACEHOLDER,
  NOTIFICATION_EVENT_OPTIONS,
  describeTestResult,
  notificationTargetLabel,
  validateNotificationsHint,
  validateStreamingHint,
  validateTranscodeHint,
  validateXmltvHint,
  type SettingsFormState,
  type SettingsSection,
} from './settingsModel'
import styles from './Admin.module.css'

type SaveFlash = SettingsSection | null

export function Settings() {
  const { client } = useAuth()
  const [form, setForm] = useState<SettingsFormState | null>(null)
  const [lineups, setLineups] = useState<SDLineupSummary[]>([])
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState<SettingsSection | null>(null)
  const [saved, setSaved] = useState<SaveFlash>(null)
  const [lineupBusy, setLineupBusy] = useState(false)
  const [lineupError, setLineupError] = useState<string | null>(null)
  const [hint, setHint] = useState<string | null>(null)
  const [testBusy, setTestBusy] = useState(false)
  const [testResult, setTestResult] = useState<{ ok: boolean; text: string } | null>(null)

  const load = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      const data = await client.getSettings()
      setForm(settingsToForm(data))
    } catch (err) {
      setError(err instanceof ApiError ? err.message || 'Failed to load settings' : 'Failed to load settings')
      setForm(null)
    } finally {
      setLoading(false)
    }
  }, [client])

  useEffect(() => {
    void load()
  }, [load])

  function flashSaved(section: SettingsSection) {
    setSaved(section)
    window.setTimeout(() => {
      setSaved((cur) => (cur === section ? null : cur))
    }, 2000)
  }

  async function saveSection(section: SettingsSection) {
    if (!form) return
    setHint(null)
    setError(null)

    if (section === 'xmltv') {
      const h = validateXmltvHint(form.xmltv.source, form.xmltv.refreshHours)
      if (h) {
        setHint(h)
        return
      }
    }
    if (section === 'transcode') {
      const h = validateTranscodeHint(form.transcode.encoder, form.transcode.available)
      if (h) {
        setHint(h)
        return
      }
    }
    if (section === 'streaming') {
      const h = validateStreamingHint(form.streaming.bufferMinutes)
      if (h) {
        setHint(h)
        return
      }
    }
    if (section === 'notifications' && form.notifications) {
      const h = validateNotificationsHint(form.notifications.url)
      if (h) {
        setHint(h)
        return
      }
    }

    setSaving(section)
    try {
      const body = buildSectionPayload(section, form)
      const updated = await client.putSettings(body)
      setForm(settingsToForm(updated))
      flashSaved(section)
    } catch (err) {
      // Surface API error message verbatim.
      setError(err instanceof ApiError ? err.message || 'Save failed' : 'Save failed')
    } finally {
      setSaving(null)
    }
  }

  async function onLoadLineups() {
    setLineupError(null)
    setLineupBusy(true)
    try {
      const list = await client.getEPGLineups()
      setLineups(list)
    } catch (err) {
      setLineupError(
        err instanceof ApiError ? err.message || 'Failed to load lineups' : 'Failed to load lineups',
      )
    } finally {
      setLineupBusy(false)
    }
  }

  function onXmltvSubmit(e: FormEvent) {
    e.preventDefault()
    void saveSection('xmltv')
  }

  function onSdSubmit(e: FormEvent) {
    e.preventDefault()
    void saveSection('schedulesDirect')
  }

  function onTranscodeSubmit(e: FormEvent) {
    e.preventDefault()
    void saveSection('transcode')
  }

  function onStreamingSubmit(e: FormEvent) {
    e.preventDefault()
    void saveSection('streaming')
  }

  function onHdhomerunSubmit(e: FormEvent) {
    e.preventDefault()
    void saveSection('hdhomerun')
  }

  function onNotificationsSubmit(e: FormEvent) {
    e.preventDefault()
    void saveSection('notifications')
  }

  /** Sends a test to the URL in the field (saved or not). */
  async function onSendTest() {
    if (!form?.notifications) return
    const url = form.notifications.url.trim()
    setTestResult(null)
    const h = validateNotificationsHint(url)
    if (url === '' || h) {
      setTestResult({ ok: false, text: h ?? 'Enter a notification URL first.' })
      return
    }
    setTestBusy(true)
    try {
      const r = await client.testNotification(url)
      setTestResult({ ok: r.ok, text: describeTestResult(r) })
    } catch (err) {
      setTestResult({
        ok: false,
        text: err instanceof ApiError ? err.message || 'Test failed' : 'Test failed',
      })
    } finally {
      setTestBusy(false)
    }
  }

  if (loading && !form) {
    return <p className={styles.status}>Loading settings…</p>
  }

  if (!form) {
    return (
      <div>
        {error ? <p className={styles.statusError}>{error}</p> : null}
        <button type="button" className={styles.btn} onClick={() => void load()}>
          Try again
        </button>
      </div>
    )
  }

  const encoders = encoderOptions(form.transcode.available)
  const sdClearHint =
    form.schedulesDirect.username.trim() === ''
      ? 'Empty username clears Schedules Direct username, password, and lineup.'
      : null

  return (
    <div>
      <div className={styles.sectionHead}>
        <h2 className={styles.sectionTitle}>Settings</h2>
      </div>

      {error ? <p className={styles.statusError}>{error}</p> : null}
      {hint ? <p className={styles.statusError}>{hint}</p> : null}

      {/* HDHomeRun free guide (servers that support it) */}
      {form.hdhomerun ? (
        <form className={styles.settingsCard} onSubmit={onHdhomerunSubmit}>
          <div className={styles.sectionHead}>
            <h3 className={styles.cardTitle}>HDHomeRun guide</h3>
          </div>
          <div style={{ display: 'flex', flexDirection: 'column', gap: '0.5rem', maxWidth: '40rem' }}>
            <label
              className={styles.label}
              style={{ flexDirection: 'row', alignItems: 'center', gap: '0.5rem', fontSize: '0.9rem' }}
            >
              <input
                className={styles.toggle}
                type="checkbox"
                checked={form.hdhomerun.enabled}
                onChange={(e) =>
                  setForm((f) => (f ? { ...f, hdhomerun: { enabled: e.target.checked } } : f))
                }
                disabled={saving === 'hdhomerun'}
              />
              Use the free HDHomeRun guide
            </label>
            <p className={styles.dim} style={{ margin: 0, fontSize: '0.85rem' }}>
              Fetches the TV guide from SiliconDust (no account needed) and matches channels that
              have no guide yet by channel number. Existing matches are never changed. It has no
              ratings, so use Schedules Direct to limit viewers by rating.
            </p>
          </div>
          <div className={styles.settingsFooter}>
            {saved === 'hdhomerun' ? <span className={styles.savedFlash}>{SAVE_FEEDBACK}</span> : null}
            <button
              type="submit"
              className={`${styles.btn} ${styles.btnPrimary} ${styles.settingsSave}`}
              disabled={saving === 'hdhomerun'}
            >
              {saving === 'hdhomerun' ? 'Saving…' : 'Save'}
            </button>
          </div>
        </form>
      ) : null}

      {/* XMLTV */}
      <form className={styles.settingsCard} onSubmit={onXmltvSubmit}>
        <div className={styles.sectionHead}>
          <h3 className={styles.cardTitle}>XMLTV</h3>
        </div>
        <p className={styles.dim} style={{ margin: '0 0 0.75rem', fontSize: '0.85rem' }}>
          Leave source empty to disable XMLTV. Otherwise use an http(s) URL or absolute path.
        </p>
        <div className={styles.settingsFields}>
          <label className={styles.label}>
            Source
            <input
              className={styles.input}
              type="text"
              value={form.xmltv.source}
              onChange={(e) =>
                setForm((f) => (f ? { ...f, xmltv: { ...f.xmltv, source: e.target.value } } : f))
              }
              placeholder="https://… or /path/to/guide.xml"
              autoComplete="off"
              disabled={saving === 'xmltv'}
            />
          </label>
          <label className={styles.label}>
            Refresh hours
            <input
              className={styles.input}
              type="number"
              min={1}
              max={168}
              value={form.xmltv.refreshHours}
              onChange={(e) =>
                setForm((f) =>
                  f ? { ...f, xmltv: { ...f.xmltv, refreshHours: e.target.value } } : f,
                )
              }
              disabled={saving === 'xmltv'}
            />
          </label>
        </div>
        <div className={styles.settingsFooter}>
          {saved === 'xmltv' ? <span className={styles.savedFlash}>{SAVE_FEEDBACK}</span> : null}
          <button
            type="submit"
            className={`${styles.btn} ${styles.btnPrimary} ${styles.settingsSave}`}
            disabled={saving === 'xmltv'}
          >
            {saving === 'xmltv' ? 'Saving…' : 'Save'}
          </button>
        </div>
      </form>

      {/* Schedules Direct */}
      <form className={styles.settingsCard} onSubmit={onSdSubmit}>
        <div className={styles.sectionHead}>
          <h3 className={styles.cardTitle}>Schedules Direct</h3>
        </div>
        <p className={styles.dim} style={{ margin: '0 0 0.75rem', fontSize: '0.85rem' }}>
          Clear username and save to remove Schedules Direct credentials and lineup.
        </p>
        {sdClearHint ? (
          <p className={styles.banner} role="status">
            {sdClearHint}
          </p>
        ) : null}
        <div className={styles.settingsFields}>
          <label className={styles.label}>
            Username
            <input
              className={styles.input}
              type="text"
              value={form.schedulesDirect.username}
              onChange={(e) =>
                setForm((f) =>
                  f
                    ? {
                        ...f,
                        schedulesDirect: { ...f.schedulesDirect, username: e.target.value },
                      }
                    : f,
                )
              }
              autoComplete="off"
              disabled={saving === 'schedulesDirect'}
            />
          </label>
          <label className={styles.label}>
            Password
            <input
              className={styles.input}
              type="password"
              value={form.schedulesDirect.password}
              onChange={(e) =>
                setForm((f) =>
                  f
                    ? {
                        ...f,
                        schedulesDirect: { ...f.schedulesDirect, password: e.target.value },
                      }
                    : f,
                )
              }
              placeholder={
                form.schedulesDirect.passwordConfigured
                  ? PASSWORD_PLACEHOLDER_CONFIGURED
                  : undefined
              }
              autoComplete="new-password"
              disabled={saving === 'schedulesDirect'}
            />
          </label>
          <label className={styles.label}>
            Lineup
            <select
              className={styles.select}
              value={form.schedulesDirect.lineupId}
              onChange={(e) =>
                setForm((f) =>
                  f
                    ? {
                        ...f,
                        schedulesDirect: { ...f.schedulesDirect, lineupId: e.target.value },
                      }
                    : f,
                )
              }
              disabled={saving === 'schedulesDirect'}
            >
              <option value="">Select a lineup…</option>
              {form.schedulesDirect.lineupId &&
              !lineups.some((l) => l.lineupId === form.schedulesDirect.lineupId) ? (
                <option value={form.schedulesDirect.lineupId}>
                  {form.schedulesDirect.lineupId} (current)
                </option>
              ) : null}
              {lineups.map((lu) => (
                <option key={lu.lineupId} value={lu.lineupId}>
                  {lineupOptionLabel(lu)}
                </option>
              ))}
            </select>
          </label>
          <div className={styles.actions} style={{ alignSelf: 'end' }}>
            <button
              type="button"
              className={styles.btn}
              onClick={() => void onLoadLineups()}
              disabled={lineupBusy}
            >
              {lineupBusy ? 'Loading…' : 'Load lineups'}
            </button>
          </div>
        </div>
        {lineupError ? <p className={styles.statusError}>{lineupError}</p> : null}
        {lineups.length > 0 ? (
          <p className={styles.dim} style={{ margin: '0.5rem 0 0', fontSize: '0.8rem' }}>
            {lineups.length} lineup{lineups.length === 1 ? '' : 's'} loaded.
          </p>
        ) : null}
        <div className={styles.settingsFooter}>
          {saved === 'schedulesDirect' ? (
            <span className={styles.savedFlash}>{SAVE_FEEDBACK}</span>
          ) : null}
          <button
            type="submit"
            className={`${styles.btn} ${styles.btnPrimary} ${styles.settingsSave}`}
            disabled={saving === 'schedulesDirect'}
          >
            {saving === 'schedulesDirect' ? 'Saving…' : 'Save'}
          </button>
        </div>
      </form>

      {/* Transcode */}
      <form className={styles.settingsCard} onSubmit={onTranscodeSubmit}>
        <div className={styles.sectionHead}>
          <h3 className={styles.cardTitle}>Transcode</h3>
        </div>
        <div className={styles.settingsFields}>
          <label className={styles.label}>
            Encoder
            <select
              className={styles.select}
              value={form.transcode.encoder}
              onChange={(e) =>
                setForm((f) =>
                  f ? { ...f, transcode: { ...f.transcode, encoder: e.target.value } } : f,
                )
              }
              disabled={saving === 'transcode'}
            >
              {encoders.map((o) => (
                <option key={o.value} value={o.value}>
                  {o.label}
                </option>
              ))}
              {/* Keep current selection visible if not in probed list */}
              {form.transcode.encoder !== 'auto' &&
              !form.transcode.available.includes(form.transcode.encoder) ? (
                <option value={form.transcode.encoder}>{form.transcode.encoder}</option>
              ) : null}
            </select>
          </label>
          <label className={styles.label} style={{ flexDirection: 'row', alignItems: 'center', gap: '0.5rem' }}>
            <input
              className={styles.toggle}
              type="checkbox"
              checked={form.transcode.allowHevc}
              onChange={(e) =>
                setForm((f) =>
                  f
                    ? { ...f, transcode: { ...f.transcode, allowHevc: e.target.checked } }
                    : f,
                )
              }
              disabled={saving === 'transcode'}
            />
            Allow HEVC
          </label>
        </div>
        <div className={styles.settingsFooter}>
          {saved === 'transcode' ? (
            <span className={styles.savedFlash}>{SAVE_FEEDBACK}</span>
          ) : null}
          <button
            type="submit"
            className={`${styles.btn} ${styles.btnPrimary} ${styles.settingsSave}`}
            disabled={saving === 'transcode'}
          >
            {saving === 'transcode' ? 'Saving…' : 'Save'}
          </button>
        </div>
      </form>

      {/* Streaming (DVR buffer) */}
      <form className={styles.settingsCard} onSubmit={onStreamingSubmit}>
        <div className={styles.sectionHead}>
          <h3 className={styles.cardTitle}>Streaming</h3>
        </div>
        <p className={styles.dim} style={{ margin: '0 0 0.75rem', fontSize: '0.85rem' }}>
          Live pause/rewind buffer length. {STREAMING_TMPFS_HINT}
        </p>
        <div className={styles.settingsFields}>
          <label className={styles.label}>
            Buffer minutes
            <input
              className={styles.input}
              type="number"
              min={2}
              max={60}
              value={form.streaming.bufferMinutes}
              onChange={(e) =>
                setForm((f) =>
                  f
                    ? {
                        ...f,
                        streaming: { ...f.streaming, bufferMinutes: e.target.value },
                      }
                    : f,
                )
              }
              disabled={saving === 'streaming'}
            />
          </label>
          <label className={styles.label} style={{ flexDirection: 'row', alignItems: 'center', gap: '0.5rem' }}>
            <input
              className={styles.toggle}
              type="checkbox"
              checked={form.streaming.adaptive}
              onChange={(e) =>
                setForm((f) =>
                  f ? { ...f, streaming: { ...f.streaming, adaptive: e.target.checked } } : f,
                )
              }
              disabled={saving === 'streaming'}
            />
            Adaptive quality (one shared stream per channel)
          </label>
          <p className={styles.dim} style={{ margin: 0, fontSize: '0.85rem' }}>
            Every viewer of a channel shares one transcode at 1080/720/480/360 (never above
            the broadcast), and players pick the quality for their connection. Uses about
            1.7× the GPU of one stream. Applies to new sessions.
          </p>
        </div>
        <div className={styles.settingsFooter}>
          {saved === 'streaming' ? (
            <span className={styles.savedFlash}>{SAVE_FEEDBACK}</span>
          ) : null}
          <button
            type="submit"
            className={`${styles.btn} ${styles.btnPrimary} ${styles.settingsSave}`}
            disabled={saving === 'streaming'}
          >
            {saving === 'streaming' ? 'Saving…' : 'Save'}
          </button>
        </div>
      </form>

      {/* Notifications (servers that support them) */}
      {form.notifications ? (
        <form className={styles.settingsCard} onSubmit={onNotificationsSubmit}>
          <div className={styles.sectionHead}>
            <h3 className={styles.cardTitle}>Notifications</h3>
          </div>
          <p className={styles.dim} style={{ margin: '0 0 0.75rem', fontSize: '0.85rem' }}>
            {NOTIFICATIONS_HINT}
          </p>
          <div style={{ display: 'flex', flexDirection: 'column', gap: '0.5rem', maxWidth: '40rem' }}>
            <label className={styles.label}>
              URL
              <input
                className={styles.input}
                type="text"
                inputMode="url"
                value={form.notifications.url}
                onChange={(e) => {
                  const url = e.target.value
                  setTestResult(null)
                  setForm((f) =>
                    f && f.notifications ? { ...f, notifications: { ...f.notifications, url } } : f,
                  )
                }}
                placeholder={NOTIFICATIONS_PLACEHOLDER}
                autoComplete="off"
                spellCheck={false}
                disabled={saving === 'notifications'}
              />
            </label>
            {notificationTargetLabel(form.notifications.url) ? (
              <p className={styles.dim} style={{ margin: 0, fontSize: '0.8rem' }}>
                {notificationTargetLabel(form.notifications.url)}
              </p>
            ) : null}
            {NOTIFICATION_EVENT_OPTIONS.map((opt) => (
              <label
                key={opt.key}
                className={styles.label}
                style={{ flexDirection: 'row', alignItems: 'center', gap: '0.5rem', fontSize: '0.9rem' }}
              >
                <input
                  className={styles.toggle}
                  type="checkbox"
                  checked={form.notifications?.events[opt.key] ?? false}
                  onChange={(e) => {
                    const on = e.target.checked
                    setForm((f) =>
                      f && f.notifications
                        ? {
                            ...f,
                            notifications: {
                              ...f.notifications,
                              events: { ...f.notifications.events, [opt.key]: on },
                            },
                          }
                        : f,
                    )
                  }}
                  disabled={saving === 'notifications'}
                />
                {opt.label}
              </label>
            ))}
          </div>
          {testResult ? (
            <p
              className={testResult.ok ? styles.savedFlash : styles.statusError}
              role="status"
              style={{ margin: '0.5rem 0 0' }}
            >
              {testResult.text}
            </p>
          ) : null}
          <div className={styles.settingsFooter}>
            {saved === 'notifications' ? (
              <span className={styles.savedFlash}>{SAVE_FEEDBACK}</span>
            ) : null}
            <button
              type="button"
              className={styles.btn}
              onClick={() => void onSendTest()}
              disabled={testBusy || form.notifications.url.trim() === ''}
            >
              {testBusy ? 'Sending…' : 'Send test'}
            </button>
            <button
              type="submit"
              className={`${styles.btn} ${styles.btnPrimary} ${styles.settingsSave}`}
              disabled={saving === 'notifications'}
            >
              {saving === 'notifications' ? 'Saving…' : 'Save'}
            </button>
          </div>
        </form>
      ) : null}

      <BackupCard />
    </div>
  )
}

function BackupCard() {
  const { client } = useAuth()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const download = async () => {
    setBusy(true)
    setError(null)
    try {
      const { blob, filename } = await client.downloadBackup()
      const url = URL.createObjectURL(blob)
      const a = document.createElement('a')
      a.href = url
      a.download = filename
      document.body.appendChild(a)
      a.click()
      a.remove()
      setTimeout(() => URL.revokeObjectURL(url), 10_000)
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Backup failed')
    } finally {
      setBusy(false)
    }
  }

  return (
    <section className={styles.settingsCard}>
      <div className={styles.sectionHead}>
        <h3 className={styles.cardTitle}>Backup</h3>
      </div>
      <p className={styles.dim} style={{ margin: '0 0 0.75rem', fontSize: '0.85rem' }}>
        Saves accounts, channels, guide matches, series rules, the recording list and these
        settings (not recorded video). It includes password hashes, the Schedules Direct
        password and the notification URL, so keep it private. To restore, stop Bowtie, replace bowtie.db in the data
        folder with this file (delete any bowtie.db-journal or -wal file next to it) and
        start Bowtie; everyone signs in again.
      </p>
      {error ? <p className={styles.statusError}>{error}</p> : null}
      <div className={styles.settingsFooter}>
        <button
          type="button"
          className={`${styles.btn} ${styles.settingsSave}`}
          onClick={download}
          disabled={busy}
        >
          {busy ? 'Preparing…' : 'Download backup'}
        </button>
      </div>
    </section>
  )
}
