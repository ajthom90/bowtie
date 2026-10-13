import { useEffect, useRef, useState, type FormEvent } from 'react'
import { ApiError, type GuideChannel, type RecordingConflict } from '../api/client'
import { viewerErrorText } from '../api/errorText'
import { useAuth } from '../auth/AuthContext'
import {
  conflictHeading,
  conflictLine,
  formatWhen,
  guideMarkText,
  programRecordAction,
} from '../recordings/recordingsModel'
import {
  DEFAULT_SERIES_FORM,
  buildRulePayload,
  scheduledText,
  seriesAvailable,
  type SeriesForm,
} from '../recordings/seriesModel'
import type { GuideProgram } from './guideModel'
import { lockText } from './searchModel'
import { ALL_TUNERS_BUSY } from './watchableModel'
import styles from './ProgramSheet.module.css'

/** The channel fields the sheet needs (a guide row or a search result). */
export type SheetChannel = Pick<GuideChannel, 'channelId' | 'guideNumber' | 'name'>

export type SheetConflict = Pick<RecordingConflict, 'tunerCount' | 'conflicts'>

type View = 'details' | 'series' | 'seriesDone'

type Props = {
  channel: SheetChannel
  program: GuideProgram
  now: Date
  onWatch: () => void
  /** False while every tuner is busy: no Watch (recording still works). */
  watchable?: boolean
  onClose: () => void
  /** A recording or series rule was saved, cancelled or stopped; warning text when the server sent one. */
  onChanged: (warning?: string) => void
  onRecordings?: () => void
  /** Open straight on the Record series form. */
  initialView?: 'details' | 'series'
  /** Open on the tuner-conflict prompt (a Record elsewhere got a 409). */
  initialConflict?: SheetConflict
}

export function isConflict(body: unknown): body is RecordingConflict {
  return (
    typeof body === 'object' &&
    body !== null &&
    typeof (body as RecordingConflict).tunerCount === 'number' &&
    Array.isArray((body as RecordingConflict).conflicts)
  )
}

/** Program details with Watch, Record / Cancel recording and Record series. */
export function ProgramSheet({
  channel,
  program,
  now,
  onWatch,
  watchable = true,
  onClose,
  onChanged,
  onRecordings,
  initialView = 'details',
  initialConflict,
}: Props) {
  const { client } = useAuth()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [conflict, setConflict] = useState<SheetConflict | null>(initialConflict ?? null)
  const [view, setView] = useState<View>(initialView)
  const [series, setSeries] = useState<SeriesForm>(DEFAULT_SERIES_FORM)
  const [scheduled, setScheduled] = useState(0)
  const firstBtnRef = useRef<HTMLButtonElement | null>(null)
  const firstFieldRef = useRef<HTMLInputElement | null>(null)

  const action = programRecordAction(program, now)
  const canSeries = seriesAvailable(program, now)
  const mark = program.recording
  // Closing after a rule was saved still refreshes the guide.
  const dismiss = view === 'seriesDone' ? () => onChanged() : onClose

  useEffect(() => {
    if (view === 'series') firstFieldRef.current?.focus()
    else firstBtnRef.current?.focus()
  }, [conflict, view])

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') dismiss()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [dismiss])

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
        setError(viewerErrorText(err, 'Something went wrong. Try again.'))
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

  const saveSeries = async (e: FormEvent) => {
    e.preventDefault()
    const payload = buildRulePayload({ channelId: channel.channelId, start: program.start }, series)
    if (!payload.ok) {
      setError(payload.error)
      return
    }
    setBusy(true)
    setError(null)
    try {
      const res = await client.createRecordingRule(payload.body)
      setScheduled(res.scheduled)
      setView('seriesDone')
    } catch (err) {
      setError(viewerErrorText(err, 'Could not record this show. Try again.'))
    } finally {
      setBusy(false)
    }
  }

  const titleId = `program-sheet-${channel.channelId}`

  const header = (
    <>
      <p className={styles.meta}>
        <span className={styles.channelNum}>{channel.guideNumber}</span>
        <span>{channel.name}</span>
      </p>
      <h2 id={titleId} className={styles.title}>
        {program.title}
      </h2>
      {program.subtitle ? <p className={styles.subtitle}>{program.subtitle}</p> : null}
    </>
  )

  let content
  if (conflict) {
    content = (
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
    )
  } else if (view === 'series') {
    content = (
      <form onSubmit={(e) => void saveSeries(e)}>
        {header}
        <p className={styles.formLead}>Record every episode of this show.</p>
        <fieldset className={styles.fieldset}>
          <legend className={styles.legend}>Channel</legend>
          <label className={styles.choice}>
            <input
              ref={firstFieldRef}
              type="radio"
              name="series-channel"
              checked={!series.anyChannel}
              onChange={() => setSeries((f) => ({ ...f, anyChannel: false }))}
              disabled={busy}
            />
            This channel ({channel.guideNumber} {channel.name})
          </label>
          <label className={styles.choice}>
            <input
              type="radio"
              name="series-channel"
              checked={series.anyChannel}
              onChange={() => setSeries((f) => ({ ...f, anyChannel: true }))}
              disabled={busy}
            />
            Any channel
          </label>
        </fieldset>
        <label className={styles.choice}>
          <input
            type="checkbox"
            checked={series.newOnly}
            onChange={(e) => setSeries((f) => ({ ...f, newOnly: e.target.checked }))}
            disabled={busy}
          />
          New episodes only
        </label>
        <label className={styles.numberField}>
          <span>Keep latest</span>
          <input
            className={styles.input}
            type="number"
            inputMode="numeric"
            min={0}
            step={1}
            value={series.keepLatest}
            onChange={(e) => setSeries((f) => ({ ...f, keepLatest: e.target.value }))}
            disabled={busy}
            aria-describedby="series-keep-hint"
          />
          <span id="series-keep-hint" className={styles.fieldHint}>
            0 keeps all. Kept recordings don't count.
          </span>
        </label>
        {error ? <p className={styles.error}>{error}</p> : null}
        <div className={styles.actions}>
          <button type="submit" className={`${styles.btn} ${styles.btnRecord}`} disabled={busy}>
            {busy ? 'Saving…' : '● Record series'}
          </button>
          <button
            type="button"
            className={styles.btn}
            disabled={busy}
            onClick={() => {
              setError(null)
              if (initialView === 'series') onClose()
              else setView('details')
            }}
          >
            {initialView === 'series' ? 'Cancel' : 'Back'}
          </button>
        </div>
      </form>
    )
  } else if (view === 'seriesDone') {
    content = (
      <>
        {header}
        <p className={styles.done} role="status">
          {scheduledText(scheduled)}
        </p>
        <div className={styles.actions}>
          <button
            ref={firstBtnRef}
            type="button"
            className={`${styles.btn} ${styles.btnPrimary}`}
            onClick={() => onChanged()}
          >
            Done
          </button>
          {onRecordings ? (
            <button type="button" className={styles.btn} onClick={onRecordings}>
              Open Recordings
            </button>
          ) : null}
        </div>
      </>
    )
  } else {
    content = (
      <>
        {header}
        <p className={styles.when}>
          {formatWhen(program.start, program.stop, now)}
          {program.rating && !program.locked ? (
            <span className={styles.rating}> · {program.rating}</span>
          ) : null}
          {mark && guideMarkText(mark.state) ? (
            <span className={styles.recState}> · ● {guideMarkText(mark.state)}</span>
          ) : null}
        </p>
        {program.locked ? (
          <p className={styles.locked}>
            <span className={styles.lockBadge}>{lockText(program.rating)}</span>
            Blocked by parental controls.
          </p>
        ) : program.description ? (
          <p className={styles.desc}>{program.description}</p>
        ) : null}
        {error ? <p className={styles.error}>{error}</p> : null}
        {watchable ? null : (
          <p className={styles.busy} role="status">
            {ALL_TUNERS_BUSY}
          </p>
        )}
        <div className={styles.actions}>
          {watchable ? (
            <button
              ref={firstBtnRef}
              type="button"
              className={`${styles.btn} ${styles.btnPrimary}`}
              onClick={onWatch}
            >
              Watch {channel.guideNumber}
            </button>
          ) : null}
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
          {canSeries ? (
            <button
              type="button"
              className={`${styles.btn} ${styles.btnRecord}`}
              disabled={busy}
              onClick={() => {
                setError(null)
                setView('series')
              }}
            >
              Record series
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
          <button
            ref={watchable ? undefined : firstBtnRef}
            type="button"
            className={styles.btn}
            onClick={onClose}
          >
            Close
          </button>
        </div>
      </>
    )
  }

  return (
    <div className={styles.backdrop} onClick={dismiss}>
      <div
        className={styles.sheet}
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        onClick={(e) => e.stopPropagation()}
      >
        {content}
      </div>
    </div>
  )
}
