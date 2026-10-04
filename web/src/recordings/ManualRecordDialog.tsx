import { useEffect, useMemo, useRef, useState, type FormEvent } from 'react'
import { ApiError, type ViewerChannel } from '../api/client'
import { useAuth } from '../auth/AuthContext'
import { sortFavoritesFirst } from '../guide/guideModel'
import { isConflict, type SheetConflict } from '../guide/ProgramSheet'
import {
  buildManualRequest,
  buildWindow,
  defaultStartTime,
  defaultTitle,
  localDateValue,
  plusOneHour,
  type ManualRequestBody,
} from './manualRecordModel'
import { conflictHeading, conflictLine } from './recordingsModel'
import styles from './ManualRecord.module.css'

type Props = {
  onClose: () => void
  /** Scheduled; warning text when the server sent one. */
  onDone: (warning?: string) => void
}

const FOCUSABLE =
  'button:not([disabled]), input:not([disabled]), select:not([disabled]), [tabindex]:not([tabindex="-1"])'

/** Record by time: a channel and a clock window, not a guide program. */
export function ManualRecordDialog({ onClose, onDone }: Props) {
  const { client } = useAuth()
  const [opened] = useState(() => new Date())
  const [channels, setChannels] = useState<ViewerChannel[] | null>(null)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [channelId, setChannelId] = useState<number | null>(null)
  const [date, setDate] = useState(() => localDateValue(opened))
  const [start, setStart] = useState(() => defaultStartTime(opened))
  const [end, setEnd] = useState(() => plusOneHour(defaultStartTime(opened)))
  const [endEdited, setEndEdited] = useState(false)
  const [title, setTitle] = useState('')
  const [titleEdited, setTitleEdited] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [conflict, setConflict] = useState<{ info: SheetConflict; body: ManualRequestBody } | null>(null)
  const panelRef = useRef<HTMLDivElement | null>(null)
  const firstRef = useRef<HTMLSelectElement | null>(null)
  const anywayRef = useRef<HTMLButtonElement | null>(null)

  useEffect(() => {
    let live = true
    client
      .getChannels()
      .then((list) => {
        if (!live) return
        const sorted = sortFavoritesFirst(list)
        setChannels(sorted)
        setChannelId((id) => id ?? sorted[0]?.id ?? null)
      })
      .catch((err: unknown) => {
        if (live) setLoadError(err instanceof ApiError && err.message ? err.message : 'Could not load channels.')
      })
    return () => {
      live = false
    }
  }, [client])

  // Focus the first field; give focus back to whatever opened the dialog on close.
  useEffect(() => {
    const opener = document.activeElement as HTMLElement | null
    const first = firstRef.current
    if (first && !first.disabled) first.focus()
    else panelRef.current?.focus()
    return () => {
      if (opener && opener.isConnected) opener.focus()
    }
  }, [])

  useEffect(() => {
    if (conflict) anywayRef.current?.focus()
  }, [conflict])

  // Channels arrived while the dialog itself held focus: move it to the select.
  useEffect(() => {
    if (channels && document.activeElement === panelRef.current) firstRef.current?.focus()
  }, [channels])

  // Escape closes; Tab stays inside the dialog.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        onClose()
        return
      }
      const panel = panelRef.current
      if (e.key !== 'Tab' || !panel) return
      const list = Array.from(panel.querySelectorAll<HTMLElement>(FOCUSABLE))
      if (list.length === 0) return
      const first = list[0]
      const last = list[list.length - 1]
      const active = document.activeElement as HTMLElement | null
      if (e.shiftKey ? active === first || !panel.contains(active) : active === last || !panel.contains(active)) {
        e.preventDefault()
        ;(e.shiftKey ? last : first).focus()
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onClose])

  const channel = channels?.find((c) => c.id === channelId) ?? null
  const suggestedTitle = useMemo(() => {
    const w = buildWindow(date, start, start)
    return w ? defaultTitle(channel?.name ?? '', w.start) : ''
  }, [channel, date, start])
  const shownTitle = titleEdited ? title : suggestedTitle

  const send = async (body: ManualRequestBody, force: boolean) => {
    setBusy(true)
    setError(null)
    try {
      const res = await client.createRecording(force ? { ...body, force: true } : body)
      const text = res.warnings.map((w) => w.message).filter(Boolean).join(' ')
      onDone(text || undefined)
    } catch (err) {
      if (err instanceof ApiError && err.status === 409 && isConflict(err.body)) {
        setConflict({ info: { tunerCount: err.body.tunerCount, conflicts: err.body.conflicts }, body })
      } else {
        setError(err instanceof ApiError && err.message ? err.message : 'Could not schedule the recording. Try again.')
      }
    } finally {
      setBusy(false)
    }
  }

  const submit = (e: FormEvent) => {
    e.preventDefault()
    const res = buildManualRequest({ channelId, date, start, end, title: shownTitle }, new Date())
    if (!res.ok) {
      setError(res.error)
      return
    }
    void send(res.body, false)
  }

  let content
  if (conflict) {
    content = (
      <>
        <h2 id="manual-record-title" className={styles.title}>
          {conflictHeading(conflict.info.tunerCount)}
        </h2>
        <ul className={styles.conflicts}>
          {conflict.info.conflicts.map((c) => (
            <li key={c.id}>{conflictLine(c)}</li>
          ))}
        </ul>
        <p className={styles.hint}>
          Record anyway to try if a tuner frees up. The recordings above go first.
        </p>
        {error ? (
          <p className={styles.error} role="alert">
            {error}
          </p>
        ) : null}
        <div className={styles.actions}>
          <button
            ref={anywayRef}
            type="button"
            className={`${styles.btn} ${styles.btnPrimary}`}
            disabled={busy}
            onClick={() => void send(conflict.body, true)}
          >
            Record anyway
          </button>
          <button type="button" className={styles.btn} disabled={busy} onClick={onClose}>
            Cancel
          </button>
        </div>
      </>
    )
  } else {
    content = (
      <form onSubmit={submit} noValidate>
        <h2 id="manual-record-title" className={styles.title}>
          Record by time
        </h2>

        <div className={styles.field}>
          <label htmlFor="manual-record-channel">Channel</label>
          <select
            ref={firstRef}
            id="manual-record-channel"
            value={channelId ?? ''}
            disabled={!channels || channels.length === 0}
            onChange={(e) => setChannelId(e.target.value ? Number(e.target.value) : null)}
          >
            {!channels ? <option value="">{loadError ? 'No channels' : 'Loading channels…'}</option> : null}
            {channels?.map((c) => (
              <option key={c.id} value={c.id}>
                {c.guideNumber} {c.name}
                {c.favorite ? ' ★' : ''}
              </option>
            ))}
          </select>
        </div>

        <div className={styles.field}>
          <label htmlFor="manual-record-date">Date</label>
          <input id="manual-record-date" type="date" value={date} onChange={(e) => setDate(e.target.value)} />
        </div>

        <div className={styles.row}>
          <div className={styles.field}>
            <label htmlFor="manual-record-start">Start time</label>
            <input
              id="manual-record-start"
              type="time"
              value={start}
              onChange={(e) => {
                setStart(e.target.value)
                if (!endEdited) setEnd(plusOneHour(e.target.value))
              }}
            />
          </div>
          <div className={styles.field}>
            <label htmlFor="manual-record-end">End time</label>
            <input
              id="manual-record-end"
              type="time"
              value={end}
              aria-describedby="manual-record-end-hint"
              onChange={(e) => {
                setEnd(e.target.value)
                setEndEdited(true)
              }}
            />
          </div>
        </div>
        <p id="manual-record-end-hint" className={styles.hint}>
          An end time before the start time ends the next day.
        </p>

        <div className={styles.field}>
          <label htmlFor="manual-record-name">Title</label>
          <input
            id="manual-record-name"
            type="text"
            value={shownTitle}
            maxLength={200}
            onChange={(e) => {
              setTitle(e.target.value)
              setTitleEdited(true)
            }}
          />
        </div>

        {loadError ? (
          <p className={styles.error} role="alert">
            {loadError}
          </p>
        ) : null}
        {error ? (
          <p className={styles.error} role="alert">
            {error}
          </p>
        ) : null}

        <div className={styles.actions}>
          <button type="submit" className={`${styles.btn} ${styles.btnPrimary}`} disabled={busy || !channels}>
            {busy ? 'Scheduling…' : 'Record'}
          </button>
          <button type="button" className={styles.btn} onClick={onClose}>
            Cancel
          </button>
        </div>
      </form>
    )
  }

  return (
    <div className={styles.backdrop} onClick={onClose}>
      <div
        ref={panelRef}
        className={styles.sheet}
        role="dialog"
        aria-modal="true"
        aria-labelledby="manual-record-title"
        tabIndex={-1}
        onClick={(e) => e.stopPropagation()}
      >
        {content}
      </div>
    </div>
  )
}
