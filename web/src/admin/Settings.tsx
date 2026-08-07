import { useCallback, useEffect, useState, type FormEvent } from 'react'
import { ApiError, type SDLineupSummary } from '../api/client'
import { useAuth } from '../auth/AuthContext'
import {
  PASSWORD_PLACEHOLDER_CONFIGURED,
  SAVE_FEEDBACK,
  buildSectionPayload,
  encoderOptions,
  lineupOptionLabel,
  settingsToForm,
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

      {/* XMLTV */}
      <form className={styles.settingsCard} onSubmit={onXmltvSubmit}>
        <div className={styles.sectionHead}>
          <h3 className={styles.cardTitle}>XMLTV</h3>
          <div className={styles.actions}>
            {saved === 'xmltv' ? <span className={styles.savedFlash}>{SAVE_FEEDBACK}</span> : null}
            <button
              type="submit"
              className={`${styles.btn} ${styles.btnPrimary}`}
              disabled={saving === 'xmltv'}
            >
              {saving === 'xmltv' ? 'Saving…' : 'Save'}
            </button>
          </div>
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
      </form>

      {/* Schedules Direct */}
      <form className={styles.settingsCard} onSubmit={onSdSubmit}>
        <div className={styles.sectionHead}>
          <h3 className={styles.cardTitle}>Schedules Direct</h3>
          <div className={styles.actions}>
            {saved === 'schedulesDirect' ? (
              <span className={styles.savedFlash}>{SAVE_FEEDBACK}</span>
            ) : null}
            <button
              type="submit"
              className={`${styles.btn} ${styles.btnPrimary}`}
              disabled={saving === 'schedulesDirect'}
            >
              {saving === 'schedulesDirect' ? 'Saving…' : 'Save'}
            </button>
          </div>
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
      </form>

      {/* Transcode */}
      <form className={styles.settingsCard} onSubmit={onTranscodeSubmit}>
        <div className={styles.sectionHead}>
          <h3 className={styles.cardTitle}>Transcode</h3>
          <div className={styles.actions}>
            {saved === 'transcode' ? (
              <span className={styles.savedFlash}>{SAVE_FEEDBACK}</span>
            ) : null}
            <button
              type="submit"
              className={`${styles.btn} ${styles.btnPrimary}`}
              disabled={saving === 'transcode'}
            >
              {saving === 'transcode' ? 'Saving…' : 'Save'}
            </button>
          </div>
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
      </form>
    </div>
  )
}
