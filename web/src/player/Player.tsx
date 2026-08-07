import Hls from 'hls.js'
import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type ChangeEvent,
  type MouseEvent as ReactMouseEvent,
} from 'react'
import { ApiError, type CreateSessionResponse, type SessionMeta } from '../api/client'
import { useAuth } from '../auth/AuthContext'
import type { WatchTarget } from '../guide/Guide'
import { canPlayNativeHls, detectCaps } from './caps'
import { QualitySheet, useIsNarrow } from './QualitySheet'
import styles from './Player.module.css'

const PROFILES = ['original', 'high', 'medium', 'low'] as const
type Profile = (typeof PROFILES)[number]

const QUALITY_OPTIONS = PROFILES.map((p) => ({
  value: p,
  label: p.charAt(0).toUpperCase() + p.slice(1),
}))

const OVERLAY_HIDE_MS = 3000

type Props = {
  target: WatchTarget
  onBack: () => void
}

type HlsStats = {
  bandwidth: number | null
  droppedFrames: number | null
  bufferLength: number | null
}

function field(v: string | undefined | null): string {
  return v && v.length > 0 ? v : '—'
}

function formatBandwidth(bps: number | null): string {
  if (bps == null || !Number.isFinite(bps) || bps <= 0) return '—'
  if (bps >= 1_000_000) return `${(bps / 1_000_000).toFixed(2)} Mbps`
  if (bps >= 1000) return `${(bps / 1000).toFixed(0)} kbps`
  return `${Math.round(bps)} bps`
}

function formatBuffer(sec: number | null): string {
  if (sec == null || !Number.isFinite(sec)) return '—'
  return `${sec.toFixed(1)} s`
}

function streamTokenFromPlaylist(playlistUrl: string): string | null {
  try {
    const u = new URL(playlistUrl, window.location.origin)
    return u.searchParams.get('token')
  } catch {
    return null
  }
}

/** Best-effort session stop when the page is unloading (DELETE cannot use sendBeacon). */
function bestEffortDelete(viewerId: string, accessToken: string | null, streamToken: string | null) {
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

export function Player({ target, onBack }: Props) {
  const { client } = useAuth()
  const videoRef = useRef<HTMLVideoElement | null>(null)
  const hlsRef = useRef<Hls | null>(null)
  const viewerIdRef = useRef<string | null>(null)
  const playlistUrlRef = useRef<string | null>(null)
  const hideTimerRef = useRef<number | null>(null)

  const [profile, setProfile] = useState<Profile>('original')
  const [sessionMeta, setSessionMeta] = useState<SessionMeta | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<{ message: string; tunerBusy: boolean } | null>(null)
  const [playing, setPlaying] = useState(true)
  const [muted, setMuted] = useState(false)
  const [volume, setVolume] = useState(1)
  const [showStats, setShowStats] = useState(false)
  const [overlayVisible, setOverlayVisible] = useState(true)
  const [hlsStats, setHlsStats] = useState<HlsStats>({
    bandwidth: null,
    droppedFrames: null,
    bufferLength: null,
  })
  const [sessionEpoch, setSessionEpoch] = useState(0)
  const isNarrow = useIsNarrow(640)

  const clearHideTimer = () => {
    if (hideTimerRef.current != null) {
      window.clearTimeout(hideTimerRef.current)
      hideTimerRef.current = null
    }
  }

  const bumpOverlay = useCallback(() => {
    setOverlayVisible(true)
    clearHideTimer()
    hideTimerRef.current = window.setTimeout(() => {
      setOverlayVisible(false)
      hideTimerRef.current = null
    }, OVERLAY_HIDE_MS)
  }, [])

  const destroyHls = () => {
    if (hlsRef.current) {
      hlsRef.current.destroy()
      hlsRef.current = null
    }
    const video = videoRef.current
    if (video) {
      video.removeAttribute('src')
      video.load()
    }
  }

  const stopSession = useCallback(async () => {
    const id = viewerIdRef.current
    viewerIdRef.current = null
    playlistUrlRef.current = null
    destroyHls()
    if (!id) return
    try {
      await client.deleteSession(id)
    } catch {
      // best-effort
    }
  }, [client])

  const attachPlayback = useCallback((playlistUrl: string) => {
    const video = videoRef.current
    if (!video) return

    destroyHls()

    if (Hls.isSupported()) {
      const hls = new Hls({
        enableWorker: true,
        lowLatencyMode: false,
      })
      hlsRef.current = hls
      hls.loadSource(playlistUrl)
      hls.attachMedia(video)
      hls.on(Hls.Events.MANIFEST_PARSED, () => {
        void video.play().catch(() => {
          /* autoplay may require mute */
        })
      })
      hls.on(Hls.Events.ERROR, (_event, data) => {
        if (data.fatal) {
          setError({
            message: data.type === Hls.ErrorTypes.NETWORK_ERROR
              ? 'Playback failed — network error. Try again.'
              : 'Playback failed. Try again or pick a lower quality.',
            tunerBusy: false,
          })
        }
      })
    } else if (canPlayNativeHls()) {
      video.src = playlistUrl
      void video.play().catch(() => {
        /* autoplay may require mute */
      })
    } else {
      setError({
        message: 'This browser cannot play HLS video.',
        tunerBusy: false,
      })
    }
  }, [])

  const startSession = useCallback(async () => {
    setLoading(true)
    setError(null)
    setSessionMeta(null)

    // Tear down prior viewer if recreating (quality change).
    if (viewerIdRef.current) {
      const prev = viewerIdRef.current
      viewerIdRef.current = null
      try {
        await client.deleteSession(prev)
      } catch {
        // ignore
      }
    }
    destroyHls()

    try {
      const caps = detectCaps(profile)
      const res: CreateSessionResponse = await client.createSession(target.channelId, caps)
      viewerIdRef.current = res.viewerId
      playlistUrlRef.current = res.playlistUrl
      setSessionMeta(res.session ?? null)
      attachPlayback(res.playlistUrl)
      setLoading(false)
      bumpOverlay()
    } catch (err) {
      setLoading(false)
      if (err instanceof ApiError && err.status === 503) {
        setError({
          message:
            'All tuners are in use — someone else is watching. Try again in a few minutes.',
          tunerBusy: true,
        })
      } else if (err instanceof ApiError) {
        setError({
          message: err.message || 'Could not start playback.',
          tunerBusy: false,
        })
      } else {
        setError({ message: 'Could not start playback.', tunerBusy: false })
      }
    }
  }, [attachPlayback, bumpOverlay, client, profile, target.channelId])

  // Create / recreate session when channel or quality changes.
  useEffect(() => {
    void startSession()
    return () => {
      // Cleanup on unmount only handled below; quality change reuses startSession cleanup.
    }
    // sessionEpoch forces retry
  }, [startSession, sessionEpoch])

  // Unmount: stop session.
  useEffect(() => {
    return () => {
      clearHideTimer()
      const id = viewerIdRef.current
      const playlist = playlistUrlRef.current
      viewerIdRef.current = null
      playlistUrlRef.current = null
      if (hlsRef.current) {
        hlsRef.current.destroy()
        hlsRef.current = null
      }
      if (id) {
        const access = localStorage.getItem('bowtie.accessToken')
        const token = playlist ? streamTokenFromPlaylist(playlist) : null
        // Prefer keepalive when unmounting (component teardown may race with navigation).
        bestEffortDelete(id, access, token)
        void client.deleteSession(id).catch(() => {
          /* ignore */
        })
      }
    }
  }, [client])

  // beforeunload / pagehide best-effort stop (fetch keepalive; sendBeacon is POST-only).
  // visibilitychange-hidden is intentionally not used: it fires on tab switch and would
  // tear down a live session. pagehide covers mobile background-kill / unload.
  useEffect(() => {
    const onUnload = () => {
      const id = viewerIdRef.current
      if (!id) return
      const access = localStorage.getItem('bowtie.accessToken')
      const token = playlistUrlRef.current
        ? streamTokenFromPlaylist(playlistUrlRef.current)
        : null
      bestEffortDelete(id, access, token)
    }
    window.addEventListener('beforeunload', onUnload)
    window.addEventListener('pagehide', onUnload)
    return () => {
      window.removeEventListener('beforeunload', onUnload)
      window.removeEventListener('pagehide', onUnload)
    }
  }, [])

  // Stats polling from hls.js + video element.
  useEffect(() => {
    if (!showStats) return
    const id = window.setInterval(() => {
      const video = videoRef.current
      const hls = hlsRef.current
      let bandwidth: number | null = null
      let bufferLength: number | null = null
      let droppedFrames: number | null = null

      if (hls) {
        bandwidth = hls.bandwidthEstimate || null
        if (video && video.buffered.length > 0) {
          const end = video.buffered.end(video.buffered.length - 1)
          bufferLength = Math.max(0, end - video.currentTime)
        }
      } else if (video && video.buffered.length > 0) {
        const end = video.buffered.end(video.buffered.length - 1)
        bufferLength = Math.max(0, end - video.currentTime)
      }

      if (video) {
        const q = video.getVideoPlaybackQuality?.()
        if (q) {
          droppedFrames = q.droppedVideoFrames
        }
      }

      setHlsStats({ bandwidth, droppedFrames, bufferLength })
    }, 1000)
    return () => window.clearInterval(id)
  }, [showStats])

  // Sync play state from video element.
  useEffect(() => {
    const video = videoRef.current
    if (!video) return
    const onPlay = () => setPlaying(true)
    const onPause = () => setPlaying(false)
    video.addEventListener('play', onPlay)
    video.addEventListener('pause', onPause)
    return () => {
      video.removeEventListener('play', onPlay)
      video.removeEventListener('pause', onPause)
    }
  }, [loading, error])

  const onStageMove = () => {
    bumpOverlay()
  }

  const togglePlay = () => {
    const video = videoRef.current
    if (!video) return
    if (video.paused) {
      void video.play()
    } else {
      video.pause()
    }
    bumpOverlay()
  }

  const toggleMute = () => {
    const video = videoRef.current
    if (!video) return
    video.muted = !video.muted
    setMuted(video.muted)
    bumpOverlay()
  }

  const onVolume = (e: ChangeEvent<HTMLInputElement>) => {
    const v = Number(e.target.value)
    setVolume(v)
    const video = videoRef.current
    if (video) {
      video.volume = v
      if (v > 0 && video.muted) {
        video.muted = false
        setMuted(false)
      }
    }
    bumpOverlay()
  }

  const onQualitySelect = (e: ChangeEvent<HTMLSelectElement>) => {
    setProfile(e.target.value as Profile)
    bumpOverlay()
  }

  const onQualityChange = (next: string) => {
    setProfile(next as Profile)
    bumpOverlay()
  }

  const toggleFullscreen = () => {
    const el = videoRef.current?.parentElement
    if (!el) return
    if (!document.fullscreenElement) {
      void el.requestFullscreen?.()
    } else {
      void document.exitFullscreen?.()
    }
    bumpOverlay()
  }

  const onBackClick = async () => {
    await stopSession()
    onBack()
  }

  const onRetry = () => {
    setSessionEpoch((n) => n + 1)
  }

  // Keep overlay interactive when hovering controls.
  const onControlsEnter = (e: ReactMouseEvent) => {
    e.stopPropagation()
    clearHideTimer()
    setOverlayVisible(true)
  }

  return (
    <div
      className={styles.stage}
      onMouseMove={onStageMove}
      onClick={bumpOverlay}
      role="application"
      aria-label={`Watching channel ${target.guideNumber}`}
    >
      <div className={styles.videoWrap}>
        <video
          ref={videoRef}
          className={styles.video}
          playsInline
          autoPlay
          muted={muted}
          onClick={(e) => {
            e.stopPropagation()
            togglePlay()
          }}
        />

        {loading && !error ? <div className={styles.loading}>Starting stream…</div> : null}

        {error ? (
          <div className={styles.errorBox}>
            <p className={styles.errorMsg}>{error.message}</p>
            <div className={styles.errorActions}>
              {error.tunerBusy ? (
                <button type="button" className={`${styles.btn} ${styles.btnPrimary}`} onClick={onRetry}>
                  Try again
                </button>
              ) : (
                <button type="button" className={`${styles.btn} ${styles.btnPrimary}`} onClick={onRetry}>
                  Try again
                </button>
              )}
              <button type="button" className={styles.btn} onClick={() => void onBackClick()}>
                Back to guide
              </button>
            </div>
          </div>
        ) : null}

        <div
          className={`${styles.overlay} ${overlayVisible ? styles.overlayVisible : styles.overlayHidden}`}
          aria-hidden={!overlayVisible}
        >
          <div className={styles.topLeft}>
            <span className={styles.channelNum}>{target.guideNumber}</span>
            <span className={styles.channelName}>{target.name}</span>
            {target.programTitle ? (
              <span className={styles.programTitle}>{target.programTitle}</span>
            ) : null}
          </div>

          {showStats ? (
            <div className={styles.stats} role="status" aria-label="Stream statistics">
              <div className={styles.statsRow}>
                <span className={styles.statsKey}>profile</span>
                <span>{field(sessionMeta?.profile ?? profile)}</span>
              </div>
              <div className={styles.statsRow}>
                <span className={styles.statsKey}>video codec</span>
                <span>{field(sessionMeta?.videoCodec)}</span>
              </div>
              <div className={styles.statsRow}>
                <span className={styles.statsKey}>backend</span>
                <span>{field(sessionMeta?.backend)}</span>
              </div>
              <div className={styles.statsRow}>
                <span className={styles.statsKey}>bandwidth</span>
                <span>{formatBandwidth(hlsStats.bandwidth)}</span>
              </div>
              <div className={styles.statsRow}>
                <span className={styles.statsKey}>dropped frames</span>
                <span>
                  {hlsStats.droppedFrames == null ? '—' : String(hlsStats.droppedFrames)}
                </span>
              </div>
              <div className={styles.statsRow}>
                <span className={styles.statsKey}>buffer</span>
                <span>{formatBuffer(hlsStats.bufferLength)}</span>
              </div>
            </div>
          ) : null}

          <div
            className={styles.controls}
            onMouseEnter={onControlsEnter}
            onMouseMove={(e) => {
              e.stopPropagation()
              clearHideTimer()
              setOverlayVisible(true)
            }}
          >
            <button type="button" className={styles.btn} onClick={() => void onBackClick()}>
              Back to guide
            </button>
            <button
              type="button"
              className={styles.btn}
              onClick={togglePlay}
              aria-label={playing ? 'Pause' : 'Play'}
            >
              {playing ? 'Pause' : 'Play'}
            </button>
            <button
              type="button"
              className={styles.btn}
              onClick={toggleMute}
              aria-label={muted ? 'Unmute' : 'Mute'}
            >
              {muted ? 'Unmute' : 'Mute'}
            </button>
            <label className={styles.volume}>
              <span className="visually-hidden">Volume</span>
              <input
                type="range"
                min={0}
                max={1}
                step={0.05}
                value={muted ? 0 : volume}
                onChange={onVolume}
                aria-label="Volume"
              />
            </label>
            {isNarrow ? (
              <QualitySheet
                value={profile}
                options={QUALITY_OPTIONS}
                onChange={onQualityChange}
                aria-label="Quality"
              />
            ) : (
              <label>
                <span className="visually-hidden">Quality</span>
                <select
                  className={styles.select}
                  value={profile}
                  onChange={onQualitySelect}
                  aria-label="Quality"
                >
                  {QUALITY_OPTIONS.map((o) => (
                    <option key={o.value} value={o.value}>
                      {o.label}
                    </option>
                  ))}
                </select>
              </label>
            )}
            <button
              type="button"
              className={styles.btn}
              onClick={() => {
                setShowStats((s) => !s)
                bumpOverlay()
              }}
              aria-pressed={showStats}
            >
              Stats
            </button>
            <span className={styles.spacer} />
            <button type="button" className={styles.btn} onClick={toggleFullscreen}>
              Fullscreen
            </button>
          </div>
        </div>
      </div>
    </div>
  )
}
