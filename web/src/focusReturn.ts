/**
 * Focus return after a full-screen player: the page that opened it is
 * unmounted while the player shows, so the opener is remembered by its
 * label and found again once the page is back.
 */
export type FocusMark = {
  /** aria-label of the control, if any. */
  label: string | null
  /** Its trimmed text, if any. */
  text: string | null
}

const FOCUSABLE =
  'button, a[href], input:not([type="hidden"]), select, textarea, [tabindex]'

function usable(el: Element): el is HTMLElement {
  if (!(el instanceof HTMLElement)) return false
  if ((el as HTMLButtonElement).disabled) return false
  if (el.getAttribute('tabindex') === '-1') return false
  if (el.closest('[hidden], [aria-hidden="true"]')) return false
  return true
}

export function focusMarkOf(el: Element | null): FocusMark | null {
  if (!el || !(el instanceof HTMLElement) || el === document.body || el === document.documentElement) {
    return null
  }
  const label = el.getAttribute('aria-label')?.trim() || null
  const text = el.textContent?.trim() || null
  if (!label && !text) return null
  return { label, text }
}

/**
 * The control to focus: the remembered one (same aria-label, else same text),
 * or — unless `fallback` is false — the first focusable control.
 */
export function findFocusTarget(
  root: ParentNode,
  mark: FocusMark | null,
  opts: { fallback?: boolean } = {},
): HTMLElement | null {
  const all = Array.from(root.querySelectorAll(FOCUSABLE)).filter(usable)
  if (mark?.label) {
    const hit = all.find((el) => el.getAttribute('aria-label')?.trim() === mark.label)
    if (hit) return hit
  }
  if (mark?.text) {
    const hit = all.find(
      (el) => !el.getAttribute('aria-label') && el.textContent?.trim() === mark.text,
    )
    if (hit) return hit
  }
  if (opts.fallback === false) return null
  return all[0] ?? null
}

/**
 * Focus the remembered control once the returning page has rendered it
 * (pages load their data first). Gives up — focusing the first control —
 * after `timeoutMs`, and stops early if the user has focused something.
 * Returns a cancel function.
 */
export function restoreFocus(mark: FocusMark | null, timeoutMs = 2000): () => void {
  const deadline = Date.now() + timeoutMs
  let timer: number | null = null
  const userMoved = () =>
    document.activeElement !== null && document.activeElement !== document.body
  const tick = () => {
    timer = null
    if (userMoved()) return
    const last = Date.now() >= deadline
    const el = findFocusTarget(document, mark, { fallback: last || !mark })
    if (el) {
      el.focus()
      return
    }
    if (!last) timer = window.setTimeout(tick, 100)
  }
  timer = window.setTimeout(tick, 0)
  return () => {
    if (timer != null) window.clearTimeout(timer)
  }
}
