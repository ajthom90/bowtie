import { useEffect, useMemo, useRef, useState } from 'react'
import type { GuideChannel } from '../api/client'
import { currentProgramTitle, receptionNote, sortFavoritesFirst } from '../guide/guideModel'
import type { TileChannel } from './multiviewModel'
import styles from './Multiview.module.css'

type Props = {
  title: string
  /** null while loading. */
  channels: GuideChannel[] | null
  error: string | null
  now: Date
  /** Channels already on screen (shown but not pickable). */
  onScreen: number[]
  onPick: (channel: TileChannel) => void
  onClose: () => void
}

/** Pick one channel for a tile: favorites first, then guide order. */
export function MultiviewPicker({ title, channels, error, now, onScreen, onPick, onClose }: Props) {
  const [filter, setFilter] = useState('')
  const filterRef = useRef<HTMLInputElement | null>(null)

  useEffect(() => {
    filterRef.current?.focus()
  }, [])

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onClose])

  const listed = useMemo(() => {
    if (!channels) return []
    const q = filter.trim().toLowerCase()
    const sorted = sortFavoritesFirst(channels)
    if (!q) return sorted
    return sorted.filter(
      (c) => c.guideNumber.toLowerCase().startsWith(q) || c.name.toLowerCase().includes(q),
    )
  }, [channels, filter])

  return (
    <div className={styles.backdrop} onClick={onClose}>
      <div
        className={styles.sheet}
        role="dialog"
        aria-modal="true"
        aria-labelledby="multiview-picker-title"
        onClick={(e) => e.stopPropagation()}
      >
        <h2 id="multiview-picker-title" className={styles.sheetTitle}>
          {title}
        </h2>
        <input
          ref={filterRef}
          className={styles.filter}
          type="search"
          placeholder="Channel number or name"
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
          aria-label="Filter channels"
        />

        {error ? <p className={styles.sheetHint}>{error}</p> : null}
        {!channels && !error ? <p className={styles.sheetHint}>Loading channels…</p> : null}
        {channels && listed.length === 0 ? (
          <p className={styles.sheetHint}>No matching channels.</p>
        ) : null}

        <ul className={styles.pickList} aria-label="Channels">
          {listed.map((c) => {
            const taken = onScreen.includes(c.channelId)
            const program = currentProgramTitle(c.programs, now)
            const rx = receptionNote(c.reception)
            return (
              <li key={c.channelId}>
                <button
                  type="button"
                  className={styles.pickRow}
                  disabled={taken}
                  onClick={() =>
                    onPick({ channelId: c.channelId, guideNumber: c.guideNumber, name: c.name })
                  }
                  aria-label={`Channel ${c.guideNumber} ${c.name}${taken ? ', already on screen' : ''}`}
                >
                  <span className={styles.pickNum}>{c.guideNumber}</span>
                  <span className={styles.pickMeta}>
                    <span className={styles.pickName}>
                      {c.name}
                      {c.favorite ? (
                        <span className={styles.pickFav} aria-hidden>
                          {' '}
                          ★
                        </span>
                      ) : null}
                    </span>
                    {program ? <span className={styles.pickProgram}>{program}</span> : null}
                  </span>
                  {taken ? <span className={styles.pickNote}>On screen</span> : null}
                  {!taken && rx ? <span className={styles.pickNote}>{rx}</span> : null}
                </button>
              </li>
            )
          })}
        </ul>

        <div className={styles.sheetActions}>
          <button type="button" className={styles.btn} onClick={onClose}>
            Cancel
          </button>
        </div>
      </div>
    </div>
  )
}
