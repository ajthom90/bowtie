import { useEffect, useMemo, useRef, useState } from 'react'
import type { AdminChannel, User } from '../api/client'
import { initialSelection, pickerChannels, toggleChannel } from './parentalModel'
import styles from './ChannelPicker.module.css'

type Props = {
  user: User
  channels: AdminChannel[]
  busy: boolean
  /** null = every channel. */
  onSave: (allowed: number[] | null) => void
  onClose: () => void
}

/** Parental controls: which channels an account may see and watch. */
export function ChannelPicker({ user, channels, busy, onSave, onClose }: Props) {
  const allowed = user.allowedChannelIds ?? null
  const listed = useMemo(() => pickerChannels(channels, allowed), [channels, allowed])
  const [all, setAll] = useState(allowed === null)
  const [picked, setPicked] = useState<number[]>(() => initialSelection(allowed, listed))
  const allRef = useRef<HTMLInputElement | null>(null)

  useEffect(() => {
    allRef.current?.focus()
  }, [])

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onClose])

  const titleId = `channel-picker-${user.id}`

  return (
    <div className={styles.backdrop} onClick={onClose}>
      <form
        className={styles.sheet}
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        onClick={(e) => e.stopPropagation()}
        onSubmit={(e) => {
          e.preventDefault()
          onSave(all ? null : picked)
        }}
      >
        <h2 id={titleId} className={styles.title}>
          Channels for {user.username}
        </h2>
        <p className={styles.hint}>Channels this account can see in the guide and watch.</p>

        <label className={`${styles.choice} ${styles.allChoice}`}>
          <input
            ref={allRef}
            type="checkbox"
            checked={all}
            onChange={(e) => setAll(e.target.checked)}
            disabled={busy}
          />
          All channels
        </label>

        {listed.length === 0 ? (
          <p className={styles.hint}>No channels are enabled yet.</p>
        ) : (
          <fieldset className={styles.list} disabled={all || busy} aria-label="Allowed channels">
            {listed.map((c) => (
              <label key={c.id} className={`${styles.choice}${c.enabled ? '' : ` ${styles.disabledCh}`}`}>
                <input
                  type="checkbox"
                  checked={all || picked.includes(c.id)}
                  onChange={() => setPicked((p) => toggleChannel(p, c.id))}
                />
                <span className={styles.chNum}>{c.guideNumber}</span>
                <span className={styles.chName}>{c.name}</span>
                {c.enabled ? null : <span className={styles.chNote}>disabled</span>}
              </label>
            ))}
          </fieldset>
        )}

        {!all ? (
          <p className={styles.count} role="status">
            {picked.length} of {listed.length} allowed
          </p>
        ) : null}

        <div className={styles.actions}>
          <button type="submit" className={`${styles.btn} ${styles.btnPrimary}`} disabled={busy}>
            {busy ? 'Saving…' : 'Save'}
          </button>
          <button type="button" className={styles.btn} onClick={onClose} disabled={busy}>
            Cancel
          </button>
        </div>
      </form>
    </div>
  )
}
