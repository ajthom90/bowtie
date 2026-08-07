import {
  useCallback,
  useEffect,
  useId,
  useRef,
  useState,
  type KeyboardEvent as ReactKeyboardEvent,
  type MouseEvent as ReactMouseEvent,
} from 'react'
import styles from './QualitySheet.module.css'

export type QualityOption = {
  value: string
  label: string
}

type Props = {
  value: string
  options: readonly QualityOption[]
  onChange: (value: string) => void
  /** Accessible name for the control (stable for automation). */
  'aria-label'?: string
}

function labelFor(options: readonly QualityOption[], value: string): string {
  return options.find((o) => o.value === value)?.label ?? value
}

/**
 * Mobile quality picker: trigger button opens a focus-trapped bottom sheet
 * (aria-modal). Desktop Player keeps a native &lt;select&gt;; this replaces it under 640px.
 */
export function QualitySheet({ value, options, onChange, 'aria-label': ariaLabel = 'Quality' }: Props) {
  const [open, setOpen] = useState(false)
  const triggerRef = useRef<HTMLButtonElement | null>(null)
  const panelRef = useRef<HTMLDivElement | null>(null)
  const previouslyFocused = useRef<HTMLElement | null>(null)
  const titleId = useId()

  const close = useCallback(() => {
    setOpen(false)
  }, [])

  const select = useCallback(
    (next: string) => {
      onChange(next)
      setOpen(false)
    },
    [onChange],
  )

  // Focus trap + restore on close.
  useEffect(() => {
    if (!open) return

    previouslyFocused.current =
      document.activeElement instanceof HTMLElement ? document.activeElement : null

    const panel = panelRef.current
    if (!panel) return

    const focusables = () =>
      Array.from(
        panel.querySelectorAll<HTMLElement>(
          'button:not([disabled]), [href], input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])',
        ),
      ).filter((el) => !el.hasAttribute('disabled') && el.tabIndex !== -1)

    // Focus the currently selected option, else first.
    const nodes = focusables()
    const selected = nodes.find((n) => n.getAttribute('data-value') === value)
    ;(selected ?? nodes[0])?.focus()

    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.preventDefault()
        e.stopPropagation()
        close()
        return
      }
      if (e.key !== 'Tab') return
      const list = focusables()
      if (list.length === 0) {
        e.preventDefault()
        return
      }
      const first = list[0]
      const last = list[list.length - 1]
      const active = document.activeElement as HTMLElement | null
      if (e.shiftKey) {
        if (active === first || !panel.contains(active)) {
          e.preventDefault()
          last.focus()
        }
      } else if (active === last || !panel.contains(active)) {
        e.preventDefault()
        first.focus()
      }
    }

    document.addEventListener('keydown', onKeyDown, true)
    return () => {
      document.removeEventListener('keydown', onKeyDown, true)
      const restore = previouslyFocused.current ?? triggerRef.current
      restore?.focus()
    }
  }, [open, close, value])

  const onScrimClick = (e: ReactMouseEvent) => {
    if (e.target === e.currentTarget) {
      close()
    }
  }

  const onTriggerKey = (e: ReactKeyboardEvent) => {
    if (e.key === 'ArrowDown' || e.key === 'Enter' || e.key === ' ') {
      e.preventDefault()
      setOpen(true)
    }
  }

  const currentLabel = labelFor(options, value)

  return (
    <div className={styles.root}>
      <button
        ref={triggerRef}
        type="button"
        className={styles.trigger}
        aria-label={ariaLabel}
        aria-haspopup="dialog"
        aria-expanded={open}
        onClick={() => setOpen(true)}
        onKeyDown={onTriggerKey}
      >
        {currentLabel}
      </button>

      {open ? (
        <div className={styles.scrim} onClick={onScrimClick} data-testid="quality-sheet-scrim">
          <div
            ref={panelRef}
            className={styles.panel}
            role="dialog"
            aria-modal="true"
            aria-labelledby={titleId}
            onClick={(e) => e.stopPropagation()}
          >
            <div className={styles.handle} aria-hidden="true" />
            <h2 id={titleId} className={styles.title}>
              {ariaLabel}
            </h2>
            <ul className={styles.list} role="listbox" aria-label={ariaLabel}>
              {options.map((opt) => {
                const selected = opt.value === value
                return (
                  <li key={opt.value} role="presentation">
                    <button
                      type="button"
                      role="option"
                      aria-selected={selected}
                      data-value={opt.value}
                      className={`${styles.option}${selected ? ` ${styles.optionSelected}` : ''}`}
                      onClick={() => select(opt.value)}
                    >
                      {opt.label}
                    </button>
                  </li>
                )
              })}
            </ul>
            <button type="button" className={styles.cancel} onClick={close}>
              Cancel
            </button>
          </div>
        </div>
      ) : null}
    </div>
  )
}

/** Hook: true when viewport is at or under the mobile breakpoint (default 640px). */
export function useIsNarrow(breakpointPx = 640): boolean {
  const query = `(max-width: ${breakpointPx}px)`
  const [narrow, setNarrow] = useState(() => {
    if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') {
      return false
    }
    return window.matchMedia(query).matches
  })

  useEffect(() => {
    if (typeof window.matchMedia !== 'function') return
    const mq = window.matchMedia(query)
    const apply = () => setNarrow(mq.matches)
    apply()
    mq.addEventListener('change', apply)
    return () => mq.removeEventListener('change', apply)
  }, [query])

  return narrow
}
