import { useCallback, useEffect, useMemo, useState } from 'react'
import {
  ApiError,
  type AdminChannel,
  type SessionInfo,
  type TranscodeStatus,
} from '../api/client'
import { useAuth } from '../auth/AuthContext'
import { formatTimestamp, formatUptime } from './adminModel'
import styles from './Admin.module.css'

const POLL_MS = 5_000

export function Sessions() {
  const { client } = useAuth()
  const [sessions, setSessions] = useState<SessionInfo[] | null>(null)
  const [transcode, setTranscode] = useState<TranscodeStatus | null>(null)
  const [channelMap, setChannelMap] = useState<Map<number, AdminChannel>>(new Map())
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)
  const [now, setNow] = useState(() => new Date())
  const [busyId, setBusyId] = useState<string | null>(null)

  const load = useCallback(async () => {
    try {
      const [sess, tc] = await Promise.all([
        client.getAdminSessions(),
        client.getTranscodeStatus().catch(() => null),
      ])
      setSessions(sess)
      if (tc) setTranscode(tc)
      setError(null)
    } catch (err) {
      setError(err instanceof ApiError ? err.message || 'Failed to load sessions' : 'Failed to load sessions')
    } finally {
      setLoading(false)
    }
  }, [client])

  // Guide numbers are not on SessionInfo; map from admin channels once.
  useEffect(() => {
    let cancelled = false
    ;(async () => {
      try {
        const chans = await client.getAdminChannels()
        if (cancelled) return
        setChannelMap(new Map(chans.map((c) => [c.id, c])))
      } catch {
        // Non-fatal; sessions still show channel name.
      }
    })()
    return () => {
      cancelled = true
    }
  }, [client])

  useEffect(() => {
    void load()
  }, [load])

  useEffect(() => {
    const id = window.setInterval(() => {
      void load()
      setNow(new Date())
    }, POLL_MS)
    return () => window.clearInterval(id)
  }, [load])

  const encoderLine = useMemo(() => {
    if (!transcode) return null
    const backends = transcode.available?.length
      ? transcode.available.join(', ')
      : 'none'
    return `Encoder: selected ${transcode.selected || '—'} · available [${backends}] · ffmpeg ${transcode.ffmpegVersion || '—'}`
  }, [transcode])

  async function onEnd(sess: SessionInfo) {
    if (!window.confirm(`End session on ${sess.channelName}? All viewers will be disconnected.`)) {
      return
    }
    setBusyId(sess.id)
    setError(null)
    try {
      await client.terminateSession(sess.id)
      await load()
    } catch (err) {
      setError(err instanceof ApiError ? err.message || 'Terminate failed' : 'Terminate failed')
    } finally {
      setBusyId(null)
    }
  }

  return (
    <div>
      <div className={styles.sectionHead}>
        <h2 className={styles.sectionTitle}>Sessions</h2>
      </div>

      {encoderLine ? <p className={styles.encoderLine}>{encoderLine}</p> : null}

      {loading && !sessions ? <p className={styles.status}>Loading sessions…</p> : null}
      {error ? <p className={styles.statusError}>{error}</p> : null}

      {!loading && sessions && sessions.length === 0 ? (
        <p className={styles.empty}>
          No live sessions. Streams appear here when someone tunes a channel.
        </p>
      ) : null}

      {sessions && sessions.length > 0 ? (
        <div className={styles.cardGrid}>
          {sessions.map((sess) => {
            const ch = channelMap.get(sess.channelId)
            const guideNum = ch?.guideNumber
            return (
              <article key={sess.id} className={styles.card}>
                <div className={styles.actions} style={{ alignItems: 'baseline', gap: '0.5rem' }}>
                  {guideNum ? <span className={styles.guideNum}>{guideNum}</span> : null}
                  <h3 className={styles.cardTitle} style={{ fontSize: '1.05rem' }}>
                    {sess.channelName || ch?.name || `Channel ${sess.channelId}`}
                  </h3>
                </div>
                <div className={styles.mono}>
                  {sess.videoCodec || '—'} / {sess.profile || '—'} / {sess.backend || '—'}
                </div>
                <div className={styles.cardMeta}>
                  uptime {formatUptime(sess.startedAt, now)} · started {formatTimestamp(sess.startedAt)}
                </div>

                <div>
                  <div className={styles.dim} style={{ fontSize: '0.75rem', marginBottom: '0.25rem' }}>
                    Viewers ({sess.viewers?.length ?? 0})
                  </div>
                  {sess.viewers && sess.viewers.length > 0 ? (
                    <ul className={styles.viewerList}>
                      {sess.viewers.map((v) => (
                        <li key={v.id}>
                          <span>{v.username || v.id}</span>
                          <span className={styles.mono}>{formatTimestamp(v.lastSeen)}</span>
                        </li>
                      ))}
                    </ul>
                  ) : (
                    <span className={styles.dim} style={{ fontSize: '0.8rem' }}>
                      No viewers attached
                    </span>
                  )}
                </div>

                <div className={styles.actions}>
                  <button
                    type="button"
                    className={`${styles.btn} ${styles.btnSm} ${styles.btnDanger}`}
                    disabled={busyId === sess.id}
                    onClick={() => void onEnd(sess)}
                  >
                    {busyId === sess.id ? 'Ending…' : 'End session'}
                  </button>
                </div>
              </article>
            )
          })}
        </div>
      ) : null}
    </div>
  )
}
