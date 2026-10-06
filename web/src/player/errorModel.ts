import { ApiError } from '../api/client'
import { viewerErrorText } from '../api/errorText'
import { tunerBusyMessage } from './seekModel'

/** What the player's error box shows. */
export type StartError = {
  message: string
  tunerBusy: boolean
  /** Offer Try again (pointless when parental controls block the program). */
  retry: boolean
}

/** 403 `{"code": "parental"}`: parental controls block this channel or program. */
export function isParentalBlock(err: unknown): err is ApiError {
  if (!(err instanceof ApiError) || err.status !== 403) return false
  const body = err.body
  return typeof body === 'object' && body !== null && (body as { code?: unknown }).code === 'parental'
}

/** Error box contents for a failed session start (or a stopped stream). */
export function startErrorFrom(err: unknown): StartError {
  if (isParentalBlock(err)) {
    return { message: err.message || 'Blocked by parental controls.', tunerBusy: false, retry: false }
  }
  // The server's 503 (not a proxy's): every tuner is busy.
  if (err instanceof ApiError && err.status === 503 && !err.technical) {
    const otherInUse = (err.body as { otherInUse?: number } | undefined)?.otherInUse
    return { message: tunerBusyMessage(otherInUse), tunerBusy: true, retry: true }
  }
  return { message: viewerErrorText(err, 'Could not start playback.'), tunerBusy: false, retry: true }
}
