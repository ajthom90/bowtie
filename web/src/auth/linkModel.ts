/** Quick sign-in codes: eight letters/digits, shown as XXXX-XXXX. */
export const CODE_LENGTH = 8

/** Uppercase letters and digits only, at most eight (the server normalizes the same way). */
export function normalizeCode(raw: string): string {
  return raw
    .toUpperCase()
    .replace(/[^A-Z0-9]/g, '')
    .slice(0, CODE_LENGTH)
}

/** What the code box shows while typing: "BCDF-2345". */
export function formatCodeInput(raw: string): string {
  const c = normalizeCode(raw)
  return c.length > 4 ? `${c.slice(0, 4)}-${c.slice(4)}` : c
}

export function isCompleteCode(raw: string): boolean {
  return normalizeCode(raw).length === CODE_LENGTH
}

/** The ?code= of the /link URL a TV shows as a QR code. */
export function codeFromSearch(search: string): string {
  return normalizeCode(new URLSearchParams(search).get('code') ?? '')
}

export function isLinkPath(pathname: string): boolean {
  return pathname === '/link' || pathname === '/link/'
}

/** The device's name, or "a TV" when it didn't send one. */
export function deviceLabel(name: string): string {
  return name.trim() || 'a TV'
}
