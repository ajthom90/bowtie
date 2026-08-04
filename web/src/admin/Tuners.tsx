import { useCallback, useEffect, useState, type FormEvent } from 'react'
import { ApiError, type DeviceStatus } from '../api/client'
import { useAuth } from '../auth/AuthContext'
import { isTunerIdle, signalPercent } from './adminModel'
import styles from './Admin.module.css'

const POLL_MS = 10_000

export function Tuners() {
  const { client } = useAuth()
  const [devices, setDevices] = useState<DeviceStatus[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)
  const [ip, setIp] = useState('')
  const [addError, setAddError] = useState<string | null>(null)
  const [adding, setAdding] = useState(false)
  const [syncing, setSyncing] = useState(false)
  const [syncMsg, setSyncMsg] = useState<string | null>(null)

  const load = useCallback(async () => {
    try {
      const data = await client.getAdminTuners()
      setDevices(data)
      setError(null)
    } catch (err) {
      setError(err instanceof ApiError ? err.message || 'Failed to load tuners' : 'Failed to load tuners')
    } finally {
      setLoading(false)
    }
  }, [client])

  useEffect(() => {
    void load()
  }, [load])

  // Poll every 10s while this page is mounted (visible tab).
  useEffect(() => {
    const id = window.setInterval(() => {
      void load()
    }, POLL_MS)
    return () => window.clearInterval(id)
  }, [load])

  async function onAdd(e: FormEvent) {
    e.preventDefault()
    setAddError(null)
    setAdding(true)
    try {
      await client.addDevice(ip.trim())
      setIp('')
      await load()
    } catch (err) {
      if (err instanceof ApiError) {
        setAddError(err.message || 'Could not add device')
      } else {
        setAddError('Could not add device')
      }
    } finally {
      setAdding(false)
    }
  }

  async function onSync() {
    setSyncing(true)
    setSyncMsg(null)
    try {
      await client.syncChannels()
      setSyncMsg('Lineups synced')
      await load()
    } catch (err) {
      setSyncMsg(err instanceof ApiError ? err.message || 'Sync failed' : 'Sync failed')
    } finally {
      setSyncing(false)
    }
  }

  async function onRemove(deviceId: string) {
    if (!window.confirm(`Remove device ${deviceId}?`)) return
    try {
      await client.deleteDevice(deviceId)
      await load()
    } catch (err) {
      setError(err instanceof ApiError ? err.message || 'Remove failed' : 'Remove failed')
    }
  }

  return (
    <div>
      <div className={styles.sectionHead}>
        <h2 className={styles.sectionTitle}>Tuners</h2>
        <div className={styles.actions}>
          <button
            type="button"
            className={styles.btn}
            onClick={() => void onSync()}
            disabled={syncing}
          >
            {syncing ? 'Syncing…' : 'Sync lineups'}
          </button>
          {syncMsg ? (
            <span className={syncMsg === 'Lineups synced' ? styles.savedFlash : styles.inlineError}>
              {syncMsg}
            </span>
          ) : null}
        </div>
      </div>

      <form className={styles.formRow} onSubmit={(e) => void onAdd(e)}>
        <label className={styles.label}>
          Add device by IP
          <input
            className={styles.input}
            name="deviceIp"
            value={ip}
            onChange={(e) => setIp(e.target.value)}
            placeholder="192.168.1.100"
            disabled={adding}
            required
            autoComplete="off"
          />
        </label>
        <button type="submit" className={`${styles.btn} ${styles.btnPrimary}`} disabled={adding || !ip.trim()}>
          {adding ? 'Adding…' : 'Add device'}
        </button>
        {addError ? <p className={styles.inlineError} role="alert">{addError}</p> : null}
      </form>

      {loading && !devices ? <p className={styles.status}>Loading devices…</p> : null}
      {error ? <p className={styles.statusError}>{error}</p> : null}

      {!loading && devices && devices.length === 0 ? (
        <p className={styles.empty}>
          No devices yet. Add your HDHomeRun by IP, or check that it&apos;s on the same network.
        </p>
      ) : null}

      {devices && devices.length > 0 ? (
        <div className={styles.cardGrid}>
          {devices.map((ds) => (
            <article key={ds.device.deviceId} className={styles.card}>
              <div className={styles.actions} style={{ justifyContent: 'space-between' }}>
                <h3 className={styles.cardTitle}>{ds.device.model || 'HDHomeRun'}</h3>
                {!ds.reachable ? <span className={styles.unreachable}>Unreachable</span> : null}
              </div>
              <div className={styles.cardMeta}>
                {ds.device.ip}
                {ds.device.streamPort ? `:${ds.device.streamPort}` : ''} · {ds.device.deviceId}
                {ds.device.manual ? ' · manual' : ''}
              </div>

              {ds.tuners.length === 0 ? (
                <p className={styles.dim} style={{ margin: 0, fontSize: '0.8rem' }}>
                  No tuner status
                </p>
              ) : (
                ds.tuners.map((t, i) => {
                  const idle = isTunerIdle(t)
                  const ss = signalPercent(t.signalStrengthPercent)
                  const channel =
                    t.vctNumber || t.vctName
                      ? [t.vctNumber, t.vctName].filter(Boolean).join(' · ')
                      : 'idle'
                  return (
                    <div
                      key={t.resource || i}
                      className={`${styles.tunerRow}${idle ? ` ${styles.tunerIdle}` : ''}`}
                    >
                      <span className={styles.mono}>{t.resource || `t${i}`}</span>
                      <div>
                        <div style={{ marginBottom: '0.2rem' }}>{channel}</div>
                        <div className={styles.signalBar} aria-hidden>
                          <div className={styles.signalFill} style={{ width: `${ss}%` }} />
                        </div>
                      </div>
                      <span className={styles.signalPct}>{ss}%</span>
                    </div>
                  )
                })
              )}

              <div className={styles.actions}>
                <button
                  type="button"
                  className={`${styles.btn} ${styles.btnSm} ${styles.btnDanger}`}
                  onClick={() => void onRemove(ds.device.deviceId)}
                >
                  Remove
                </button>
              </div>
            </article>
          ))}
        </div>
      ) : null}
    </div>
  )
}
