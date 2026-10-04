export function streamTokenFromPlaylist(playlistUrl: string): string | null {
  try {
    const u = new URL(playlistUrl, window.location.origin)
    return u.searchParams.get('token')
  } catch {
    return null
  }
}

/** Best-effort session stop when the page is unloading (DELETE cannot use sendBeacon). */
export function bestEffortDelete(viewerId: string, accessToken: string | null, streamToken: string | null) {
  let url = `/api/v1/sessions/${encodeURIComponent(viewerId)}`
  if (streamToken) {
    url += `?token=${encodeURIComponent(streamToken)}`
  }
  try {
    void fetch(url, {
      method: 'DELETE',
      headers: accessToken ? { Authorization: `Bearer ${accessToken}` } : {},
      keepalive: true,
    })
  } catch {
    // ignore
  }
  // sendBeacon is POST-only; some browsers still fire it as a secondary signal
  // when a stream token is present (server ignores unknown methods). Prefer keepalive DELETE.
  if (streamToken && typeof navigator.sendBeacon === 'function') {
    try {
      navigator.sendBeacon(
        `/api/v1/sessions/${encodeURIComponent(viewerId)}?token=${encodeURIComponent(streamToken)}`,
      )
    } catch {
      // ignore
    }
  }
}
