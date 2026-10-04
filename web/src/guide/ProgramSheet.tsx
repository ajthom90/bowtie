import { useEffect, useRef, useState } from 'react'
import { ApiError, type GuideChannel, type RecordingConflict } from '../api/client'
import { useAuth } from '../auth/AuthContext'
import {
  conflictHeading,
  conflictLine,
  formatWhen,
  guideMarkText,
  programRecordAction,
} from '../recordings/recordingsModel'
import type { GuideProgram } from './guideModel'
import styles from './ProgramSheet.module.css'

type Props = {
  channel: GuideChannel
  program: GuideProgram
  now: Date
  onWatch: () => void
  onClose: () => void
  /** A recording was scheduled, cancelled or stopped; warning text when the server sent one. */
  onChanged: (warning?: string) => void
  onRecordings?: () => void
}

type Conflict = Pick<RecordingConflict, 'tunerCount' | 'conflicts'>

function isConflict(body: unknown): body is RecordingConflict {
  return (
    typeof body === 'object' &&
    body !== null &&
    typeof (body as RecordingConflict).tunerCount === 'number' &&
    Array.isArray((body as RecordingConflict).conflicts)
  )
}

/** Program details with Watch and Record / Cancel recording. */
export function ProgramSheet({
  channel,
  program,
  now,
  onWatch,
  onClose,
  onChanged,
  onRecordings,
}: Props) {
  const { client } = useAuth()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [conflict, setConflict] = useState<Conflict | null>(null)
  const firstBtnRef = useRef<HTMLButtonElement | null>(null)

  const action = programRecordAction(program, now)
  const mark = program.recording

  useEffect(() => {
    firstBtnRef.current?.focus()
  }, [conflict])

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onClose])

  const run = async (fn: () => Promise<string | undefined>) => {
    setBusy(true)
    setError(null)
    try {
      const warning = await fn()
      onChanged(warning)
    } catch (err) {
      if (err instanceof ApiError && err.status === 409 && isConflict(err.body)) {
        setConflict({ tunerCount: err.body.tunerCount, conflicts: err.body.conflicts })
      } else {
        setError(err instanceof ApiError && err.message ? err.message : 'Something went wrong. Try again.')
      }
    } finally {
      setBusy(false)
    }
  }

  const record = (force: boolean) =>
    run(async () => {
      const res = await client.createRecording({
        channelId: channel.channelId,
        programStart: program.start,
        ...(force ? { force: true } : {}),
      })
      const text = res.warnings.map((w) => w.message).filter(Boolean).join(' ')
      return text || undefined
    })

  const cancel = () => {
    if (!mark) return
    void run(async () => {
      await client.deleteRecording(mark.id)
      return undefined
    })
  }

  const stop = () => {
    if (!mark) return
    if (!window.confirm(`Stop recording “${program.title}” now? What's recorded so far is kept.`)) return
    void run(async () => {
      await client.stopRecording(mark.id)
      return undefined
    })
  }

  const titleId = `program-sheet-${channel.channelId}`

  return (
    <div className={styles.backdrop} onClick={onClose}>
      <div
        className={styles.sheet}
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        onClick={(e) => e.stopPropagation()}
      >
        {conflict ? (
          <>
            <h2 id={titleId} className={styles.title}>
              {conflictHeading(conflict.tunerCount)}
            </h2>
            <ul className={styles.conflicts}>
              {conflict.conflicts.map((c) => (
                <li key={c.id}>{conflictLine(c)}</li>
              ))}
            </ul>
            <p className={styles.hint}>
              Record anyway to try if a tuner frees up. The recordings above go first.
            </p>
            {error ? <p className={styles.error}>{error}</p> : null}
            <div className={styles.actions}>
              <button
                ref={firstBtnRef}
                type="button"
                className={`${styles.btn} ${styles.btnRecord}`}
                disabled={busy}
                onClick={() => void record(true)}
              >
                Record anyway
              </button>
              <button type="button" className={styles.btn} disabled={busy} onClick={onClose}>
                Cancel
              </button>
            </div>
          </>
        ) : (
          <>
            <p className={styles.meta}>
              <span className={styles.channelNum}>{channel.guideNumber}</span>
              <span>{channel.name}</span>
            </p>
            <h2 id={titleId} className={styles.title}>
              {program.title}
            </h2>
            {program.subtitle ? <p className={styles.subtitle}>{program.subtitle}</p> : null}
            <p className={styles.when}>
              {formatWhen(program.start, program.stop, now)}
              {mark && guideMarkText(mark.state) ? (
                <span className={styles.recState}> · ● {guideMarkText(mark.state)}</span>
              ) : null}
            </p>
            {program.description ? <p className={styles.desc}>{program.description}</p> : null}
            {error ? <p className={styles.error}>{error}</p> : null}
            <div className={styles.actions}>
              <button
                ref={firstBtnRef}
                type="button"
                className={`${styles.btn} ${styles.btnPrimary}`}
                onClick={onWatch}
              >
                Watch {channel.guideNumber}
              </button>
              {action === 'record' ? (
                <button
                  type="button"
                  className={`${styles.btn} ${styles.btnRecord}`}
                  disabled={busy}
                  onClick={() => void record(false)}
                >
                  ● Record
                </button>
              ) : null}
              {action === 'cancel' ? (
                <button type="button" className={styles.btn} disabled={busy} onClick={cancel}>
                  Cancel recording
                </button>
              ) : null}
              {action === 'stop' ? (
                <button type="button" className={styles.btn} disabled={busy} onClick={stop}>
                  Stop recording
                </button>
              ) : null}
              {action === 'recorded' && onRecordings ? (
                <button type="button" className={styles.btn} onClick={onRecordings}>
                  Open Recordings
                </button>
              ) : null}
              <button type="button" className={styles.btn} onClick={onClose}>
                Close
              </button>
            </div>
          </>
        )}
      </div>
    </div>
  )
}
