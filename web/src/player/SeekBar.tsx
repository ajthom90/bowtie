import type { ChangeEvent } from 'react'
import {
  behindSeconds,
  formatBehind,
  isAtLiveEdge,
  type LiveWindow,
} from './seekModel'
import styles from './SeekBar.module.css'

export type SeekBarProps = {
  window: LiveWindow | null
  disabled?: boolean
  onSeek: (pos: number) => void
  onSkipBack: () => void
  onJumpToLive: () => void
}

/**
 * Live DVR seek bar: range over the buffered window, LIVE badge
 * (amber at edge / dim -mm:ss when behind), skip-back 30s, jump-to-live.
 */
export function SeekBar({
  window: liveWindow,
  disabled = false,
  onSeek,
  onSkipBack,
  onJumpToLive,
}: SeekBarProps) {
  const ready =
    liveWindow != null &&
    Number.isFinite(liveWindow.start) &&
    Number.isFinite(liveWindow.liveEdge) &&
    liveWindow.liveEdge > liveWindow.start

  const atEdge = ready && isAtLiveEdge(liveWindow!)
  const behind = ready ? behindSeconds(liveWindow!) : 0
  const min = ready ? liveWindow!.start : 0
  const max = ready ? liveWindow!.liveEdge : 1
  const value = ready
    ? Math.min(max, Math.max(min, liveWindow!.current))
    : 0

  const onRange = (e: ChangeEvent<HTMLInputElement>) => {
    if (!ready) return
    onSeek(Number(e.target.value))
  }

  return (
    <div className={styles.bar} role="group" aria-label="Live seek">
      <span
        className={`${styles.liveBadge} ${atEdge ? styles.liveAtEdge : styles.liveBehind}`}
        aria-label={atEdge ? 'Live' : `Behind live ${formatBehind(behind)}`}
      >
        {atEdge ? 'LIVE' : formatBehind(behind)}
      </span>

      <input
        className={styles.range}
        type="range"
        min={min}
        max={max}
        step={0.1}
        value={value}
        disabled={disabled || !ready}
        onChange={onRange}
        aria-label="Seek within live buffer"
        aria-valuemin={min}
        aria-valuemax={max}
        aria-valuenow={value}
      />

      <button
        type="button"
        className={styles.btn}
        disabled={disabled || !ready}
        onClick={onSkipBack}
        aria-label="Skip back 30 seconds"
      >
        −30s
      </button>
      <button
        type="button"
        className={styles.btn}
        disabled={disabled || !ready || !!atEdge}
        onClick={onJumpToLive}
        aria-label="Jump to live"
      >
        Live
      </button>
    </div>
  )
}
