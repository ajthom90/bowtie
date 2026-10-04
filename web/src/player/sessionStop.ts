export function streamTokenFromPlaylist(playlistUrl: string): string | null {
  try {
    const u = new URL(playlistUrl, window.location.origin)
    return u.searchParams.get('token')
  } catch {
    return null
  }
}

/**
 * Stop a viewer with one keepalive DELETE, so it survives navigation and tab
 * close. (sendBeacon can only POST, and the server has no POST stop route.)
 * Resolves when the request settles; never rejects.
 */
export function bestEffortDelete(
  viewerId: string,
  accessToken: string | null,
  streamToken: string | null,
): Promise<void> {
  let url = `/api/v1/sessions/${encodeURIComponent(viewerId)}`
  if (streamToken) {
    url += `?token=${encodeURIComponent(streamToken)}`
  }
  try {
    return fetch(url, {
      method: 'DELETE',
      headers: accessToken ? { Authorization: `Bearer ${accessToken}` } : {},
      keepalive: true,
    }).then(
      () => undefined,
      () => undefined,
    )
  } catch {
    return Promise.resolve()
  }
}
