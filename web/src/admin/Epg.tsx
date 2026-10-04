import { useCallback, useEffect, useState } from 'react'
import { ApiError, type EPGSourceState, type EPGSourceStatus } from '../api/client'
import { useAuth } from '../auth/AuthContext'
import {
  anyEpgConfigured,
  epgErrorText,
  epgHealth,
  epgHealthLabel,
  epgSources,
  epgSummaryBanner,
  formatTimestamp,
  isZeroTime,
  type EPGSourceKey,
} from './adminModel'
import styles from './Admin.module.css'

const SOURCE_NOTES: Partial<Record<EPGSourceKey, string>> = {
  hdhomerun: 'From SiliconDust, no account needed. Refreshes about once a day. No ratings.',
}

function SourceCard({
  sourceKey,
  name,
  note,
  state,
}: {
  sourceKey: EPGSourceKey
  name: string
  note?: string
  state: EPGSourceState
}) {
  const lastSuccess = isZeroTime(state.lastSuccess)
    ? 'never'
    : formatTimestamp(state.lastSuccess)
  const health = epgHealth(state)
  const errorText = epgErrorText(sourceKey, state.lastError)

  return (
    <article className={styles.card}>
      <h3 className={styles.cardTitle}>{name}</h3>
      <div className={styles.cardMeta}>
        {health === 'off' ? 'Not configured' : `Configured · ${epgHealthLabel(health)}`}
      </div>
      {note ? (
        <div className={styles.dim} style={{ fontSize: '0.82rem' }}>
          {note}
        </div>
      ) : null}
      <div>
        <span className={styles.dim}>Last success </span>
        <span className={styles.mono}>{lastSuccess}</span>
      </div>
      {errorText ? (
        <p className={styles.alertText} role="status">
          {errorText}
        </p>
      ) : null}
      {health === 'never' ? (
        <div className={styles.dim} role="status">
          Never fetched yet. Refresh now to try a download.
        </div>
      ) : null}
      {health === 'stale' ? (
        <div className={styles.banner} role="alert">
          Guide data is stale. Refresh now or check the source configuration.
        </div>
      ) : null}
    </article>
  )
}

export function Epg() {
  const { client } = useAuth()
  const [status, setStatus] = useState<EPGSourceStatus | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)
  const [refreshing, setRefreshing] = useState(false)
  const [refreshMsg, setRefreshMsg] = useState<string | null>(null)

  const load = useCallback(async () => {
    try {
      const data = await client.getEPGStatus()
      setStatus(data)
      setError(null)
    } catch (err) {
      setError(err instanceof ApiError ? err.message || 'Failed to load EPG status' : 'Failed to load EPG status')
    } finally {
      setLoading(false)
    }
  }, [client])

  useEffect(() => {
    void load()
  }, [load])

  async function onRefresh() {
    setRefreshing(true)
    setRefreshMsg(null)
    setError(null)
    try {
      await client.refreshEPG()
      setRefreshMsg('Refresh started')
      // Status may lag; reload shortly.
      window.setTimeout(() => void load(), 1500)
    } catch (err) {
      setError(err instanceof ApiError ? err.message || 'Refresh failed' : 'Refresh failed')
    } finally {
      setRefreshing(false)
    }
  }

  const summary = status ? epgSummaryBanner(status) : null

  return (
    <div>
      <div className={styles.sectionHead}>
        <h2 className={styles.sectionTitle}>EPG</h2>
        <div className={styles.actions}>
          <button
            type="button"
            className={`${styles.btn} ${styles.btnPrimary}`}
            onClick={() => void onRefresh()}
            disabled={refreshing}
          >
            {refreshing ? 'Starting…' : 'Refresh now'}
          </button>
          {refreshMsg ? <span className={styles.savedFlash}>{refreshMsg}</span> : null}
        </div>
      </div>

      {summary ? (
        <div className={styles.banner} role="alert">
          {summary}
        </div>
      ) : null}

      {loading && !status ? <p className={styles.status}>Loading EPG status…</p> : null}
      {error ? <p className={styles.statusError}>{error}</p> : null}

      {status ? (
        <div className={styles.cardGrid}>
          {epgSources(status).map((s) => (
            <SourceCard key={s.key} sourceKey={s.key} name={s.label} note={SOURCE_NOTES[s.key]} state={s.state} />
          ))}
        </div>
      ) : null}

      {!loading && status && !anyEpgConfigured(status) ? (
        <p className={styles.empty} style={{ marginTop: '1rem' }}>
          No EPG sources configured. Turn on the HDHomeRun free guide or set XMLTV and/or
          Schedules Direct in Settings, then refresh.
        </p>
      ) : null}
    </div>
  )
}
