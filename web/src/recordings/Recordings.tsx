import { useCallback, useEffect, useState } from 'react'
import { ApiError, type Recording } from '../api/client'
import { useAuth } from '../auth/AuthContext'
import {
  EMPTY_TAB_COPY,
  RECORDINGS_TABS,
  failureText,
  formatClock,
  formatDuration,
  formatSize,
  formatWhen,
  recordingBadges,
  removeConfirmText,
  rowActions,
  tabQuery,
  type RecordingsTab,
} from './recordingsModel'
import styles from './Recordings.module.css'

/** Refresh while open so states (recording → converting → ready) move on their own. */
const REFRESH_MS = 20_000

type Props = {
  tab: RecordingsTab
  onTab: (tab: RecordingsTab) => void
  onGuide: () => void
  onAdmin?: () => void
  onPlay: (rec: Recording) => void
}

function errorText(err: unknown, fallback: string): string {
  return err instanceof ApiError && err.message ? err.message : fallback
}

export function Recordings({ tab, onTab, onGuide, onAdmin, onPlay }: Props) {
  const { client, user, logout } = useAuth()
  const [rows, setRows] = useState<Recording[] | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [actionError, setActionError] = useState<string | null>(null)
  const [busyId, setBusyId] = useState<number | null>(null)

  const load = useCallback(
    async (opts?: { quiet?: boolean }) => {
      if (!opts?.quiet) {
        setLoading(true)
        setError(null)
      }
      try {
        const data = await client.listRecordings(tabQuery(tab))
        setRows(data)
        setError(null)
      } catch (err) {
        if (!opts?.quiet) {
          setRows(null)
          setError(errorText(err, 'Failed to load recordings'))
        }
      } finally {
        if (!opts?.quiet) setLoading(false)
      }
    },
    [client, tab],
  )

  useEffect(() => {
    setRows(null)
    setActionError(null)
    void load()
  }, [load])

  useEffect(() => {
    const id = window.setInterval(() => {
      if (document.visibilityState === 'visible') void load({ quiet: true })
    }, REFRESH_MS)
    return () => window.clearInterval(id)
  }, [load])

  const act = async (rec: Recording, fn: () => Promise<void>, fallback: string) => {
    setBusyId(rec.id)
    setActionError(null)
    try {
      await fn()
    } catch (err) {
      setActionError(errorText(err, fallback))
    } finally {
      setBusyId(null)
    }
  }

  const onStop = (rec: Recording) => {
    if (!window.confirm(`Stop recording “${rec.title}” now? What's recorded so far is kept.`)) return
    void act(
      rec,
      async () => {
        await client.stopRecording(rec.id)
        await load({ quiet: true })
      },
      'Could not stop the recording.',
    )
  }

  const onRemove = (rec: Recording, kind: 'cancel' | 'delete') => {
    if (!window.confirm(removeConfirmText(rec, kind))) return
    void act(
      rec,
      async () => {
        await client.deleteRecording(rec.id)
        setRows((rs) => rs?.filter((r) => r.id !== rec.id) ?? rs)
      },
      kind === 'cancel' ? 'Could not cancel the recording.' : 'Could not delete the recording.',
    )
  }

  const onKeep = (rec: Recording) => {
    void act(
      rec,
      async () => {
        const updated = await client.patchRecording(rec.id, { protected: !rec.protected })
        setRows((rs) => rs?.map((r) => (r.id === rec.id ? { ...r, ...updated } : r)) ?? rs)
      },
      'Could not update the recording.',
    )
  }

  const now = new Date()

  return (
    <div className={styles.page}>
      <header className={styles.toolbar}>
        <div className={styles.toolbarLeft}>
          <span className={styles.brand}>Bowtie</span>
          <span className={styles.subtitle}>Recordings</span>
        </div>
        <div className={styles.toolbarRight}>
          <button type="button" className={styles.btn} onClick={onGuide}>
            Guide
          </button>
          {onAdmin ? (
            <button type="button" className={styles.btn} onClick={onAdmin}>
              Admin
            </button>
          ) : null}
          <span className={styles.subtitle}>{user?.username}</span>
          <button type="button" className={styles.btn} onClick={() => void logout()}>
            Sign out
          </button>
        </div>
      </header>

      <nav className={styles.nav} aria-label="Recordings">
        {RECORDINGS_TABS.map((t) => (
          <button
            key={t.id}
            type="button"
            className={`${styles.navBtn}${tab === t.id ? ` ${styles.navBtnActive}` : ''}`}
            aria-current={tab === t.id ? 'page' : undefined}
            onClick={() => onTab(t.id)}
          >
            {t.label}
          </button>
        ))}
      </nav>

      <main className={styles.body}>
        {actionError ? (
          <p className={styles.statusError} role="alert">
            {actionError}
          </p>
        ) : null}

        {loading && rows === null ? <p className={styles.status}>Loading…</p> : null}

        {error ? (
          <div className={styles.status}>
            <p className={styles.statusError}>{error}</p>
            <button type="button" className={styles.btn} onClick={() => void load()}>
              Try again
            </button>
          </div>
        ) : null}

        {rows && rows.length === 0 ? <p className={styles.status}>{EMPTY_TAB_COPY[tab]}</p> : null}

        {rows && rows.length > 0 ? (
          <ul className={styles.list}>
            {rows.map((rec) => (
              <RecordingRow
                key={rec.id}
                rec={rec}
                tab={tab}
                now={now}
                busy={busyId === rec.id}
                onPlay={() => onPlay(rec)}
                onStop={() => onStop(rec)}
                onRemove={(kind) => onRemove(rec, kind)}
                onKeep={() => onKeep(rec)}
              />
            ))}
          </ul>
        ) : null}
      </main>
    </div>
  )
}

function RecordingRow({
  rec,
  tab,
  now,
  busy,
  onPlay,
  onStop,
  onRemove,
  onKeep,
}: {
  rec: Recording
  tab: RecordingsTab
  now: Date
  busy: boolean
  onPlay: () => void
  onStop: () => void
  onRemove: (kind: 'cancel' | 'delete') => void
  onKeep: () => void
}) {
  const badges = recordingBadges(rec)
  const actions = rowActions(rec)
  const remove = actions.remove
  const failure = rec.state === 'failed' ? failureText(rec.failure, rec.failureDetail) : ''
  const meta = [rec.channelName, formatWhen(rec.start, rec.stop, now)]
  if (tab === 'recorded') {
    meta.push(formatDuration(rec.durationSec), formatSize(rec.sizeBytes))
  }

  return (
    <li className={styles.row}>
      <div className={styles.rowMain}>
        <div className={styles.rowTitleLine}>
          <span className={styles.rowTitle}>{rec.title}</span>
          {badges.map((b) => (
            <span
              key={b.label}
              className={`${styles.badge} ${b.tone === 'live' ? styles.badgeLive : ''} ${b.tone === 'warn' ? styles.badgeWarn : ''}`}
            >
              {b.label}
            </span>
          ))}
          {rec.protected && !actions.keep ? <span className={styles.badge}>Kept</span> : null}
        </div>
        {rec.subtitle ? <div className={styles.rowSub}>{rec.subtitle}</div> : null}
        <div className={styles.rowMeta}>{meta.join(' · ')}</div>
        {failure ? <div className={styles.failure}>Missed: {failure}</div> : null}
        <div className={styles.rowBy}>
          {rec.scheduledBy ? `Scheduled by ${rec.scheduledBy}` : 'Scheduled by a removed user'}
          {actions.play && rec.positionSec > 10 ? ` · Watched to ${formatClock(rec.positionSec)}` : ''}
        </div>
      </div>
      <div className={styles.rowActions}>
        {actions.play ? (
          <button type="button" className={`${styles.btn} ${styles.btnPrimary}`} onClick={onPlay}>
            Play
          </button>
        ) : null}
        {actions.stop ? (
          <button type="button" className={styles.btn} disabled={busy} onClick={onStop}>
            Stop
          </button>
        ) : null}
        {actions.keep ? (
          <button
            type="button"
            className={styles.btn}
            disabled={busy}
            onClick={onKeep}
            aria-pressed={rec.protected}
            title="Kept recordings are never deleted automatically when space runs low"
          >
            {rec.protected ? 'Kept ✓' : 'Keep'}
          </button>
        ) : null}
        {remove ? (
          <button
            type="button"
            className={`${styles.btn} ${styles.btnDanger}`}
            disabled={busy}
            onClick={() => onRemove(remove)}
          >
            {remove === 'cancel' ? 'Cancel' : 'Delete'}
          </button>
        ) : null}
      </div>
    </li>
  )
}
