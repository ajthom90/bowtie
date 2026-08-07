import { useCallback, useEffect, useState } from 'react'
import { ApiError, type EPGSourceState, type EPGSourceStatus } from '../api/client'
import { useAuth } from '../auth/AuthContext'
import { formatTimestamp, isZeroTime } from './adminModel'
import styles from './Admin.module.css'

type SourceKey = 'xmltv' | 'sd'

const SOURCE_LABELS: Record<SourceKey, string> = {
  xmltv: 'XMLTV',
  sd: 'Schedules Direct',
}

function SourceCard({ name, state }: { name: string; state: EPGSourceState }) {
  const lastSuccess = isZeroTime(state.lastSuccess)
    ? 'never'
    : formatTimestamp(state.lastSuccess)

  return (
    <article className={styles.card}>
      <h3 className={styles.cardTitle}>{name}</h3>
      <div className={styles.cardMeta}>
        {state.configured ? 'Configured' : 'Not configured'}
      </div>
      <div>
        <span className={styles.dim}>Last success </span>
        <span className={styles.mono}>{lastSuccess}</span>
      </div>
      {state.lastError ? (
        <p className={styles.alertText} role="status">
          {state.lastError}
        </p>
      ) : null}
      {state.stale ? (
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

  const anyStale = Boolean(status?.xmltv.stale || status?.sd.stale)

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

      {anyStale ? (
        <div className={styles.banner} role="alert">
          One or more EPG sources are stale. Viewer guide data may be incomplete.
        </div>
      ) : null}

      {loading && !status ? <p className={styles.status}>Loading EPG status…</p> : null}
      {error ? <p className={styles.statusError}>{error}</p> : null}

      {status ? (
        <div className={styles.cardGrid}>
          <SourceCard name={SOURCE_LABELS.xmltv} state={status.xmltv} />
          <SourceCard name={SOURCE_LABELS.sd} state={status.sd} />
        </div>
      ) : null}

      {!loading && status && !status.xmltv.configured && !status.sd.configured ? (
        <p className={styles.empty} style={{ marginTop: '1rem' }}>
          No EPG sources configured. Set XMLTV and/or Schedules Direct in Settings, then refresh.
        </p>
      ) : null}
    </div>
  )
}
