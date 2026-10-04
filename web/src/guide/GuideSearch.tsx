import { useEffect, useMemo, useRef, useState } from 'react'
import { ApiError, type GuideSearchResult } from '../api/client'
import { useAuth } from '../auth/AuthContext'
import { formatWhen } from '../recordings/recordingsModel'
import type { WatchTarget } from './Guide'
import { isConflict, type SheetChannel, type SheetConflict } from './ProgramSheet'
import {
  SEARCH_DEBOUNCE_MS,
  SEARCH_LIMIT,
  createDebouncer,
  lockText,
  normalizeQuery,
  resultActions,
  searchStatusText,
} from './searchModel'
import styles from './GuideSearch.module.css'

export type SearchSheetRequest = {
  channel: SheetChannel
  program: GuideSearchResult
  initialView?: 'details' | 'series'
  initialConflict?: SheetConflict
}

type Props = {
  onWatch: (target: WatchTarget) => void
  /** Open the program sheet (details, series form, or tuner conflict). */
  onOpenSheet: (req: SearchSheetRequest) => void
  /** Show a notice under the toolbar (e.g. a recording warning). */
  onNotice: (text: string) => void
  /** A recording was scheduled from the results; the guide should reload. */
  onChanged: () => void
  /** Bumped by the guide after a sheet change so results re-run. */
  refreshKey: number
  /** A sheet is open over the panel: ignore outside clicks and Escape. */
  suspended: boolean
}

function sheetChannel(r: GuideSearchResult): SheetChannel {
  return { channelId: r.channelId, guideNumber: r.guideNumber, name: r.channelName }
}

/** Search box in the guide header with a results panel. */
export function GuideSearch({ onWatch, onOpenSheet, onNotice, onChanged, refreshKey, suspended }: Props) {
  const { client } = useAuth()
  const [text, setText] = useState('')
  const [query, setQuery] = useState<string | null>(null)
  const [results, setResults] = useState<GuideSearchResult[] | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [rowError, setRowError] = useState<string | null>(null)
  const [busyKey, setBusyKey] = useState<string | null>(null)
  const [open, setOpen] = useState(false)
  const seqRef = useRef(0)
  const wrapRef = useRef<HTMLDivElement | null>(null)
  const inputRef = useRef<HTMLInputElement | null>(null)

  const debouncer = useMemo(
    () => createDebouncer((raw: string) => setQuery(normalizeQuery(raw)), SEARCH_DEBOUNCE_MS),
    [],
  )
  useEffect(() => () => debouncer.cancel(), [debouncer])

  // Run the search; only the newest request may land.
  useEffect(() => {
    const seq = ++seqRef.current
    if (!query) {
      setResults(null)
      setLoading(false)
      setError(null)
      return
    }
    setLoading(true)
    setError(null)
    client.searchGuide(query, SEARCH_LIMIT).then(
      (rows) => {
        if (seq !== seqRef.current) return
        setResults(rows)
        setLoading(false)
      },
      (err: unknown) => {
        if (seq !== seqRef.current) return
        setResults(null)
        setError(err instanceof ApiError && err.message ? err.message : 'Search failed. Try again.')
        setLoading(false)
      },
    )
  }, [client, query, refreshKey])

  // Close on outside click / Escape (not while a sheet is open on top).
  useEffect(() => {
    if (!open || suspended) return
    const onDown = (e: MouseEvent) => {
      if (wrapRef.current && !wrapRef.current.contains(e.target as Node)) setOpen(false)
    }
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setOpen(false)
    }
    document.addEventListener('mousedown', onDown)
    window.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('mousedown', onDown)
      window.removeEventListener('keydown', onKey)
    }
  }, [open, suspended])

  const clear = () => {
    debouncer.cancel()
    setText('')
    setQuery(null)
    setRowError(null)
    setOpen(false)
    inputRef.current?.focus()
  }

  const record = async (r: GuideSearchResult) => {
    const key = `${r.channelId}-${r.start}`
    setBusyKey(key)
    setRowError(null)
    try {
      const res = await client.createRecording({ channelId: r.channelId, programStart: r.start })
      const mark = { id: res.recording.id, state: res.recording.state }
      setResults((rs) =>
        rs ? rs.map((x) => (x.channelId === r.channelId && x.start === r.start ? { ...x, recording: mark } : x)) : rs,
      )
      const warning = res.warnings.map((w) => w.message).filter(Boolean).join(' ')
      if (warning) onNotice(warning)
      onChanged()
    } catch (err) {
      if (err instanceof ApiError && err.status === 409 && isConflict(err.body)) {
        onOpenSheet({
          channel: sheetChannel(r),
          program: r,
          initialConflict: { tunerCount: err.body.tunerCount, conflicts: err.body.conflicts },
        })
      } else {
        setRowError(err instanceof ApiError && err.message ? err.message : 'Could not schedule the recording.')
      }
    } finally {
      setBusyKey(null)
    }
  }

  const now = new Date()
  const status = searchStatusText({ query, loading, error, count: results?.length ?? 0 })
  const showPanel = open && query !== null

  return (
    <div className={styles.wrap} ref={wrapRef}>
      <div className={styles.field}>
        <span className={styles.icon} aria-hidden>
          ⌕
        </span>
        <input
          ref={inputRef}
          className={styles.input}
          type="search"
          placeholder="Search the guide"
          aria-label="Search the guide"
          aria-controls="guide-search-results"
          aria-expanded={showPanel}
          autoComplete="off"
          enterKeyHint="search"
          value={text}
          onChange={(e) => {
            setText(e.target.value)
            setOpen(true)
            debouncer.call(e.target.value)
          }}
          onFocus={() => setOpen(true)}
          onKeyDown={(e) => {
            if (e.key === 'Enter') {
              debouncer.cancel()
              setQuery(normalizeQuery(text))
              setOpen(true)
            }
          }}
        />
        {text ? (
          <button type="button" className={styles.clear} onClick={clear} aria-label="Clear search">
            ×
          </button>
        ) : null}
      </div>

      {showPanel ? (
        <section id="guide-search-results" className={styles.panel} aria-label="Search results">
          <div className={styles.panelHead}>
            <span className={error ? styles.statusError : styles.status} role="status">
              {status}
            </span>
            <button type="button" className={styles.btn} onClick={() => setOpen(false)}>
              Close
            </button>
          </div>
          {rowError ? (
            <p className={styles.statusError} role="alert">
              {rowError}
            </p>
          ) : null}
          {results && results.length > 0 ? (
            <ul className={styles.list}>
              {results.map((r) => {
                const a = resultActions(r, now)
                const key = `${r.channelId}-${r.start}`
                return (
                  <li key={key} className={styles.item}>
                    <button
                      type="button"
                      className={styles.itemMain}
                      onClick={() => onOpenSheet({ channel: sheetChannel(r), program: r })}
                      aria-haspopup="dialog"
                    >
                      <span className={styles.itemMeta}>
                        <span className={styles.chNum}>{r.guideNumber}</span>
                        <span className={styles.chName}>{r.channelName}</span>
                        <span className={styles.when}>{formatWhen(r.start, r.stop, now)}</span>
                        {a.watch ? <span className={styles.onNow}>On now</span> : null}
                      </span>
                      <span className={styles.itemTitle}>
                        {r.title}
                        {r.locked ? (
                          <span className={styles.lock}>{lockText(r.rating)}</span>
                        ) : r.rating ? (
                          <span className={styles.rating}>{r.rating}</span>
                        ) : null}
                      </span>
                      {r.subtitle ? <span className={styles.itemSub}>{r.subtitle}</span> : null}
                    </button>
                    <div className={styles.itemActions}>
                      {a.watch ? (
                        <button
                          type="button"
                          className={`${styles.btn} ${styles.btnPrimary}`}
                          onClick={() =>
                            onWatch({
                              channelId: r.channelId,
                              guideNumber: r.guideNumber,
                              name: r.channelName,
                              programTitle: r.title,
                            })
                          }
                        >
                          Watch
                        </button>
                      ) : null}
                      {a.record ? (
                        <button
                          type="button"
                          className={`${styles.btn} ${styles.btnRecord}`}
                          disabled={busyKey === key}
                          onClick={() => void record(r)}
                        >
                          ● Record
                        </button>
                      ) : null}
                      {a.markText ? <span className={styles.mark}>● {a.markText}</span> : null}
                      {a.series ? (
                        <button
                          type="button"
                          className={`${styles.btn} ${styles.btnRecord}`}
                          onClick={() =>
                            onOpenSheet({ channel: sheetChannel(r), program: r, initialView: 'series' })
                          }
                        >
                          Record series
                        </button>
                      ) : null}
                    </div>
                  </li>
                )
              })}
            </ul>
          ) : null}
        </section>
      ) : null}
    </div>
  )
}
