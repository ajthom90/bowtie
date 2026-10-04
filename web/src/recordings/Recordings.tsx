import { useCallback, useEffect, useState } from 'react'
import { ApiError, type Recording, type RecordingRule } from '../api/client'
import { useAuth } from '../auth/AuthContext'
import { lockText } from '../guide/searchModel'
import {
  EMPTY_TAB_COPY,
  RECORDINGS_TABS,
  failureLine,
  formatClock,
  formatDuration,
  formatSize,
  formatWhen,
  isListTab,
  isSeriesRecording,
  recordingBadges,
  removeConfirmText,
  rowActions,
  tabQuery,
  type RecordingListTab,
  type RecordingsTab,
} from './recordingsModel'
import { ruleSummary, stopShowConfirmText } from './seriesModel'
import styles from './Recordings.module.css'

/** Refresh while open so states (recording → converting → ready) move on their own. */
const REFRESH_MS = 20_000

type Props = {
  tab: RecordingsTab
  onTab: (tab: RecordingsTab) => void
  onGuide: () => void
  onAdmin?: () => void
  onAccount?: () => void
  onPlay: (rec: Recording) => void
}

function errorText(err: unknown, fallback: string): string {
  return err instanceof ApiError && err.message ? err.message : fallback
}

export function Recordings({ tab, onTab, onGuide, onAdmin, onAccount, onPlay }: Props) {
  const { user, logout } = useAuth()

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
          {onAccount ? (
            <button type="button" className={styles.btn} onClick={onAccount} title="Account">
              {user?.username}
            </button>
          ) : (
            <span className={styles.subtitle}>{user?.username}</span>
          )}
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
        {isListTab(tab) ? <RecordingList key={tab} tab={tab} onPlay={onPlay} /> : <ShowsList />}
      </main>
    </div>
  )
}

/** Upcoming / Recorded / Missed. */
function RecordingList({ tab, onPlay }: { tab: RecordingListTab; onPlay: (rec: Recording) => void }) {
  const { client } = useAuth()
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
    <>
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
    </>
  )
}

/** Series rules: every show being recorded. */
function ShowsList() {
  const { client } = useAuth()
  const [rules, setRules] = useState<RecordingRule[] | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [actionError, setActionError] = useState<string | null>(null)
  const [busyId, setBusyId] = useState<number | null>(null)

  const load = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      setRules(await client.listRecordingRules())
    } catch (err) {
      setRules(null)
      setError(errorText(err, 'Failed to load shows'))
    } finally {
      setLoading(false)
    }
  }, [client])

  useEffect(() => {
    void load()
  }, [load])

  const onStopShow = async (rule: RecordingRule) => {
    if (!window.confirm(stopShowConfirmText(rule))) return
    setBusyId(rule.id)
    setActionError(null)
    try {
      await client.deleteRecordingRule(rule.id)
      setRules((rs) => rs?.filter((r) => r.id !== rule.id) ?? rs)
    } catch (err) {
      setActionError(errorText(err, 'Could not stop recording this show.'))
    } finally {
      setBusyId(null)
    }
  }

  return (
    <>
      {actionError ? (
        <p className={styles.statusError} role="alert">
          {actionError}
        </p>
      ) : null}
      {loading && rules === null ? <p className={styles.status}>Loading…</p> : null}
      {error ? (
        <div className={styles.status}>
          <p className={styles.statusError}>{error}</p>
          <button type="button" className={styles.btn} onClick={() => void load()}>
            Try again
          </button>
        </div>
      ) : null}
      {rules && rules.length === 0 ? <p className={styles.status}>{EMPTY_TAB_COPY.shows}</p> : null}
      {rules && rules.length > 0 ? (
        <ul className={styles.list}>
          {rules.map((rule) => (
            <li key={rule.id} className={styles.row}>
              <div className={styles.rowMain}>
                <div className={styles.rowTitleLine}>
                  <span className={styles.rowTitle}>{rule.title}</span>
                </div>
                <div className={styles.rowMeta}>{ruleSummary(rule)}</div>
                <div className={styles.rowBy}>
                  {rule.scheduledBy ? `Added by ${rule.scheduledBy}` : 'Added by a removed user'}
                </div>
              </div>
              {rule.canManage ? (
                <div className={styles.rowActions}>
                  <button
                    type="button"
                    className={`${styles.btn} ${styles.btnDanger}`}
                    disabled={busyId === rule.id}
                    onClick={() => void onStopShow(rule)}
                  >
                    Stop recording this show
                  </button>
                </div>
              ) : null}
            </li>
          ))}
        </ul>
      ) : null}
    </>
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
  tab: RecordingListTab
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
  const failure = failureLine(rec)
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
          {isSeriesRecording(rec) ? <span className={styles.badge}>Series</span> : null}
          {rec.locked ? (
            <span className={`${styles.badge} ${styles.badgeLock}`}>{lockText(rec.rating)}</span>
          ) : null}
          {rec.protected && !actions.keep ? <span className={styles.badge}>Kept</span> : null}
        </div>
        {rec.subtitle ? <div className={styles.rowSub}>{rec.subtitle}</div> : null}
        <div className={styles.rowMeta}>{meta.join(' · ')}</div>
        {failure ? (
          <div className={rec.failure === 'skipped' ? styles.skipped : styles.failure}>{failure}</div>
        ) : null}
        <div className={styles.rowBy}>
          {rec.scheduledBy ? `Scheduled by ${rec.scheduledBy}` : 'Scheduled by a removed user'}
          {actions.play && rec.positionSec > 10 ? ` · Watched to ${formatClock(rec.positionSec)}` : ''}
        </div>
      </div>
      <div className={styles.rowActions}>
        {actions.play ? (
          <button
            type="button"
            className={`${styles.btn} ${styles.btnPrimary}`}
            onClick={onPlay}
            disabled={actions.playLocked}
            title={actions.playLocked ? 'Blocked by parental controls' : undefined}
          >
            {actions.playLocked ? '🔒 Play' : 'Play'}
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
