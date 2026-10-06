import { ApiError } from './client'

/** The network failed, or a proxy in front of Bowtie couldn't reach it. */
export const CANT_REACH_SERVER = "Can't reach your Bowtie server. Check your connection and try again."

/** A live stream or recording stopped loading part-way. */
export const STREAM_STOPPED = 'The stream stopped. Try again.'

/** The browser has no way to play the stream at all. */
export const CANT_PLAY_HERE =
  "This browser can't play TV from Bowtie. Try a current version of Chrome, Edge, Firefox or Safari."

/** Statuses a reverse proxy answers with when Bowtie itself is down. */
const PROXY_DOWN = new Set([502, 503, 504])

/**
 * What a viewer sees for a failed request: the server's own message (already
 * plain words) when it sent one; otherwise "can't reach" for network and
 * proxy failures, or `fallback`. Technical detail goes to the console only.
 */
export function viewerErrorText(err: unknown, fallback: string): string {
  if (err instanceof ApiError && !err.technical && err.message) {
    return err.message
  }
  console.warn('Bowtie request failed:', err)
  // fetch() rejects with a TypeError when the network or server is unreachable.
  if (err instanceof TypeError) return CANT_REACH_SERVER
  if (err instanceof ApiError && err.technical && PROXY_DOWN.has(err.status)) {
    return CANT_REACH_SERVER
  }
  return fallback
}
