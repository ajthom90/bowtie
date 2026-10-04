/** An open live viewer on the server. */
export type SessionHandle = {
  viewerId: string
  playlistUrl: string
}

export type SessionAttempt = {
  /** True once the player has let go (unmount, new quality, page hide). */
  readonly released: boolean
  /**
   * The server created the viewer. Returns false (after stopping it) when
   * the player let go while the request was in flight.
   */
  resolve: (h: SessionHandle) => boolean
  /** Let go: stops the open viewer, if any. Safe to call more than once. */
  release: () => void
  /** The server already ended the viewer (e.g. parental block): nothing to stop. */
  forget: () => void
  current: () => SessionHandle | null
}

/**
 * One try at opening a live viewer. Starting a stream can take 10–20 s;
 * whichever of "created" and "let go" comes second stops the viewer, so
 * leaving early never strands a tuner. `stop` runs at most once.
 */
export function createSessionAttempt(stop: (h: SessionHandle) => void): SessionAttempt {
  let released = false
  let handle: SessionHandle | null = null
  return {
    get released() {
      return released
    },
    resolve(h) {
      if (released) {
        stop(h)
        return false
      }
      handle = h
      return true
    },
    release() {
      if (released) return
      released = true
      const h = handle
      handle = null
      if (h) stop(h)
    },
    forget() {
      handle = null
    },
    current: () => handle,
  }
}
