import { useEffect, useMemo, useRef, useState } from 'react'
import type { GuideChannel } from '../api/client'
import { currentProgramTitle, receptionNote, sortFavoritesFirst } from '../guide/guideModel'
import { ALL_TUNERS_BUSY, busyNote, watchableOnly } from '../guide/watchableModel'
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
  const panelRef = useRef<HTMLDivElement | null>(null)

  // Focus the filter; give focus back to whatever opened the picker on close.
  useEffect(() => {
    const opener = document.activeElement as HTMLElement | null
    filterRef.current?.focus()
    return () => {
      if (opener && opener.isConnected) opener.focus()
    }
  }, [])

  // Escape closes; Tab stays inside the dialog (the tiles behind are inert).
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        onClose()
        return
      }
      const panel = panelRef.current
      if (e.key !== 'Tab' || !panel) return
      const list = Array.from(
        panel.querySelectorAll<HTMLElement>('button:not([disabled]), input:not([disabled]), [tabindex]:not([tabindex="-1"])'),
      )
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

  const listed = useMemo(() => {
    if (!channels) return []
    const q = filter.trim().toLowerCase()
    // Only channels a tuner can take (all tuners busy hides the rest).
    const sorted = sortFavoritesFirst(watchableOnly(channels))
    if (!q) return sorted
    return sorted.filter(
      (c) => c.guideNumber.toLowerCase().startsWith(q) || c.name.toLowerCase().includes(q),
    )
  }, [channels, filter])

  const tunersNote = channels ? busyNote(channels) : null

  return (
    <div className={styles.backdrop} onClick={onClose}>
      <div
        ref={panelRef}
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
        {tunersNote ? (
          <p className={styles.sheetHint} role="status">
            {tunersNote}
          </p>
        ) : null}
        {channels && listed.length === 0 && tunersNote !== ALL_TUNERS_BUSY ? (
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
