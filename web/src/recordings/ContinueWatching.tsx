import { useId } from 'react'
import type { Recording } from '../api/client'
import {
  formatTimeLeft,
  progressPct,
  removeLabel,
  resumeLabel,
  secondsLeft,
} from './continueModel'
import styles from './ContinueWatching.module.css'

type Props = {
  /** Already selected and ordered (selectContinueWatching). Renders nothing when empty. */
  items: Recording[]
  /** Compact strip for above the guide grid. */
  compact?: boolean
  /** Plays from the saved position. */
  onPlay: (rec: Recording) => void
  /** Resets the position to 0 (drops the item everywhere). */
  onRemove: (rec: Recording) => void
  /** Item whose remove is in flight. */
  busyId?: number | null
  /** Guide only: hide the whole row. */
  onHide?: () => void
}

/** "Continue watching": recordings started and not finished, newest first. */
export function ContinueWatching({ items, compact = false, onPlay, onRemove, busyId, onHide }: Props) {
  const headingId = useId()
  if (items.length === 0) return null

  return (
    <section
      className={`${styles.section}${compact ? ` ${styles.compact}` : ''}`}
      aria-labelledby={headingId}
    >
      <div className={styles.head}>
        <h2 id={headingId} className={styles.heading}>
          Continue watching
        </h2>
        {onHide ? (
          <button
            type="button"
            className={styles.hide}
            onClick={onHide}
            aria-label="Hide Continue watching on the guide"
            title="Hide until you watch something else"
          >
            Hide
          </button>
        ) : null}
      </div>
      <ul className={styles.list}>
        {items.map((rec) => {
          const pct = progressPct(rec)
          const left = formatTimeLeft(secondsLeft(rec))
          return (
            <li key={rec.id} className={styles.item}>
              <button
                type="button"
                className={styles.play}
                onClick={() => onPlay(rec)}
                aria-label={resumeLabel(rec)}
              >
                <span className={styles.title}>{rec.title}</span>
                {rec.subtitle && !compact ? <span className={styles.sub}>{rec.subtitle}</span> : null}
                <span className={styles.meta}>
                  {compact ? `${rec.channelName} · ${left}` : rec.channelName}
                </span>
                {!compact ? <span className={styles.left}>{left}</span> : null}
              </button>
              <div
                className={styles.bar}
                role="progressbar"
                aria-label={`${rec.title} watched`}
                aria-valuemin={0}
                aria-valuemax={100}
                aria-valuenow={Math.round(pct)}
                aria-valuetext={`${Math.round(pct)}% watched, ${left}`}
              >
                <span className={styles.fill} style={{ width: `${pct}%` }} />
              </div>
              <button
                type="button"
                className={styles.remove}
                onClick={() => onRemove(rec)}
                disabled={busyId === rec.id}
                aria-label={removeLabel(rec)}
                title="Remove from Continue watching"
              >
                <span aria-hidden>×</span>
              </button>
            </li>
          )
        })}
      </ul>
    </section>
  )
}
