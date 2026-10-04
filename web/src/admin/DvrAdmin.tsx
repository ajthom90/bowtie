import { useCallback, useEffect, useState, type FormEvent } from 'react'
import { ApiError, type DVRStorage } from '../api/client'
import { useAuth } from '../auth/AuthContext'
import {
  DISK_FULL_WARNING,
  DISK_LOW_NOTE,
  buildPaddingPayload,
  formatBytes,
  gaugeSegments,
  paddingToForm,
  storageWarning,
  type PaddingForm,
} from './dvrModel'
import { SAVE_FEEDBACK } from './settingsModel'
import styles from './Admin.module.css'

function message(err: unknown, fallback: string): string {
  return err instanceof ApiError ? err.message || fallback : fallback
}

export function DvrAdmin() {
  const { client } = useAuth()
  const [storage, setStorage] = useState<DVRStorage | null>(null)
  const [unavailable, setUnavailable] = useState(false)
  const [storageError, setStorageError] = useState<string | null>(null)
  const [form, setForm] = useState<PaddingForm | null>(null)
  const [settingsError, setSettingsError] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [saved, setSaved] = useState(false)
  const [saveError, setSaveError] = useState<string | null>(null)

  const loadStorage = useCallback(async () => {
    try {
      setStorage(await client.getDVRStorage())
      setStorageError(null)
      setUnavailable(false)
    } catch (err) {
      if (err instanceof ApiError && err.status === 503) {
        setUnavailable(true)
      } else {
        setStorageError(message(err, 'Failed to load storage'))
      }
    }
  }, [client])

  const load = useCallback(async () => {
    setLoading(true)
    setSettingsError(null)
    const settings = client.getSettings().then(
      (s) => setForm(paddingToForm(s.dvr)),
      (err: unknown) => setSettingsError(message(err, 'Failed to load settings')),
    )
    await Promise.all([settings, loadStorage()])
    setLoading(false)
  }, [client, loadStorage])

  useEffect(() => {
    void load()
  }, [load])

  async function onSave(e: FormEvent) {
    e.preventDefault()
    if (!form) return
    setSaveError(null)
    const payload = buildPaddingPayload(form)
    if (!payload.ok) {
      setSaveError(payload.error)
      return
    }
    setSaving(true)
    try {
      const updated = await client.putSettings({ dvr: payload.dvr })
      setForm(paddingToForm(updated.dvr ?? payload.dvr))
      setSaved(true)
      window.setTimeout(() => setSaved(false), 2000)
    } catch (err) {
      setSaveError(message(err, 'Save failed'))
    } finally {
      setSaving(false)
    }
  }

  if (loading && !form && !storage) {
    return <p className={styles.status}>Loading recordings…</p>
  }

  return (
    <div>
      <div className={styles.sectionHead}>
        <h2 className={styles.sectionTitle}>Recording storage &amp; padding</h2>
        <button type="button" className={styles.btn} onClick={() => void loadStorage()}>
          Refresh
        </button>
      </div>

      {unavailable ? (
        <p className={styles.status}>Recording isn&apos;t available on this server.</p>
      ) : null}
      {storageError ? <p className={styles.statusError}>{storageError}</p> : null}
      {storage ? <StorageCard storage={storage} /> : null}

      {settingsError ? (
        <div>
          <p className={styles.statusError}>{settingsError}</p>
          <button type="button" className={styles.btn} onClick={() => void load()}>
            Try again
          </button>
        </div>
      ) : null}
      {form ? (
        <form className={styles.settingsCard} onSubmit={onSave}>
          <div className={styles.sectionHead}>
            <h3 className={styles.cardTitle}>Padding</h3>
          </div>
          <p className={styles.dim} style={{ margin: '0 0 0.75rem', fontSize: '0.85rem' }}>
            Shows often start early or run late. Changes apply to recordings scheduled from now
            on; ones already scheduled keep their padding.
          </p>
          <div className={styles.settingsFields}>
            <label className={styles.label}>
              Start recording N minutes early
              <input
                className={styles.input}
                type="number"
                inputMode="decimal"
                min={0}
                max={30}
                step="any"
                value={form.start}
                onChange={(e) => setForm((f) => (f ? { ...f, start: e.target.value } : f))}
                disabled={saving}
              />
            </label>
            <label className={styles.label}>
              Keep recording N minutes after
              <input
                className={styles.input}
                type="number"
                inputMode="decimal"
                min={0}
                max={60}
                step="any"
                value={form.end}
                onChange={(e) => setForm((f) => (f ? { ...f, end: e.target.value } : f))}
                disabled={saving}
              />
            </label>
          </div>
          {saveError ? (
            <p className={styles.statusError} style={{ margin: '0.75rem 0 0' }}>
              {saveError}
            </p>
          ) : null}
          <div className={styles.settingsFooter}>
            {saved ? <span className={styles.savedFlash}>{SAVE_FEEDBACK}</span> : null}
            <button
              type="submit"
              className={`${styles.btn} ${styles.btnPrimary} ${styles.settingsSave}`}
              disabled={saving}
            >
              {saving ? 'Saving…' : 'Save'}
            </button>
          </div>
        </form>
      ) : null}
    </div>
  )
}

function StorageCard({ storage }: { storage: DVRStorage }) {
  const g = gaugeSegments(storage)
  const warning = storageWarning(storage)
  const counts = storage.recordings
  return (
    <section className={styles.settingsCard}>
      <div className={styles.sectionHead}>
        <h3 className={styles.cardTitle}>Storage</h3>
        <span className={`${styles.cardMeta} ${styles.dvrDir}`}>{storage.dir}</span>
      </div>

      {warning === 'full' ? <p className={styles.banner}>{DISK_FULL_WARNING}</p> : null}
      {warning === 'low' ? <p className={styles.note}>{DISK_LOW_NOTE}</p> : null}

      <div
        className={styles.gauge}
        role="img"
        aria-label={`${formatBytes(g.recordingsBytes)} used by recordings, ${formatBytes(g.otherBytes)} used by other files, ${formatBytes(g.freeBytes)} free of ${formatBytes(storage.totalBytes)}`}
      >
        <span className={styles.gaugeRecordings} style={{ width: `${g.recordingsPct}%` }} />
        <span className={styles.gaugeOther} style={{ width: `${g.otherPct}%` }} />
        <span
          className={`${styles.gaugeFree}${warning === 'full' ? ` ${styles.gaugeFreeAlert}` : ''}`}
          style={{ width: `${g.freePct}%` }}
        />
      </div>

      <ul className={styles.gaugeLegend}>
        <li>
          <span className={`${styles.swatch} ${styles.gaugeRecordings}`} aria-hidden="true" />
          Recordings <span className={styles.mono}>{formatBytes(g.recordingsBytes)}</span>
        </li>
        <li>
          <span className={`${styles.swatch} ${styles.gaugeOther}`} aria-hidden="true" />
          Other <span className={styles.mono}>{formatBytes(g.otherBytes)}</span>
        </li>
        <li>
          <span className={`${styles.swatch} ${styles.gaugeFree}`} aria-hidden="true" />
          Free <span className={styles.mono}>{formatBytes(g.freeBytes)}</span>
        </li>
        <li className={styles.dim}>
          of <span className={styles.mono}>{formatBytes(storage.totalBytes)}</span>
        </li>
      </ul>

      <dl className={styles.dvrCounts}>
        <div>
          <dt>Ready</dt>
          <dd>{counts.ready}</dd>
        </div>
        <div>
          <dt>Scheduled</dt>
          <dd>{counts.scheduled}</dd>
        </div>
        {counts.recording > 0 ? (
          <div>
            <dt>Recording</dt>
            <dd>{counts.recording}</dd>
          </div>
        ) : null}
        <div>
          <dt>Failed</dt>
          <dd className={counts.failed > 0 ? styles.alertText : undefined}>{counts.failed}</dd>
        </div>
      </dl>
    </section>
  )
}
