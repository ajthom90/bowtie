import Hls from 'hls.js'
import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type ChangeEvent,
  type MouseEvent as ReactMouseEvent,
} from 'react'
import { type CreateSessionResponse, type ReceptionSignal, type SessionMeta } from '../api/client'
import { CANT_PLAY_HERE, STREAM_STOPPED } from '../api/errorText'
import { useAuth } from '../auth/AuthContext'
import type { WatchTarget } from '../guide/Guide'
import { canPlayNativeHls, detectCaps } from './caps'
import { isParentalBlock, startErrorFrom, type StartError } from './errorModel'
import { QUALITY_HINT, QualitySheet, useIsNarrow } from './QualitySheet'
import { audioTrackLabel, loadTrackPrefs, pickAudioIndex, saveTrackPrefs } from './tracksModel'
import { SeekBar } from './SeekBar'
import { bestEffortDelete, streamTokenFromPlaylist } from './sessionStop'
import { createSessionAttempt, type SessionAttempt, type SessionHandle } from './sessionLifecycle'
import { signalStatsLine, weakSignalNote } from './signalModel'
import {
  OUT_OF_WINDOW_NOTICE,
  clampSeek,
  createHeartbeatController,
  skipBack,
  type LiveWindow,
} from './seekModel'
import styles from './Player.module.css'

const PROFILES = ['original', 'high', 'medium', 'low'] as const
type Profile = (typeof PROFILES)[number]

const QUALITY_OPTIONS = PROFILES.map((p) => ({
  value: p,
  label: p.charAt(0).toUpperCase() + p.slice(1),
}))

const OVERLAY_HIDE_MS = 3000
const LIVE_WINDOW_POLL_MS = 250
const NOTICE_HIDE_MS = 4000

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

/**
 * Build adapter LiveWindow from hls.js levelDetails + liveSyncPosition +
 * video.currentTime (A8). Falls back to video.seekable for native HLS.
 */
function buildLiveWindow(hls: Hls | null, video: HTMLVideoElement): LiveWindow | null {
  if (hls) {
    const levelIdx = hls.currentLevel >= 0 ? hls.currentLevel : hls.levels.length - 1
    const details = (levelIdx >= 0 ? hls.levels[levelIdx]?.details : null) ?? null
    // Also try first loaded level if current is unset.
    const d =
      details ??
      hls.levels.map((l) => l.details).find((x) => x != null && x.live) ??
      hls.levels.map((l) => l.details).find((x) => x != null) ??
      null
    if (d) {
      const start = d.fragmentStart
      const end = d.edge
      const liveEdge =
        hls.liveSyncPosition != null && Number.isFinite(hls.liveSyncPosition)
          ? hls.liveSyncPosition
          : end
      if (Number.isFinite(start) && Number.isFinite(end) && end > start) {
        return {
          start,
          end,
          liveEdge: Math.min(Math.max(liveEdge, start), end) || end,
          current: video.currentTime,
        }
      }
    }
  }

  if (video.seekable && video.seekable.length > 0) {
    const start = video.seekable.start(0)
    const end = video.seekable.end(video.seekable.length - 1)
    if (Number.isFinite(start) && Number.isFinite(end) && end > start) {
      return {
        start,
        end,
        liveEdge: end,
        current: video.currentTime,
      }
    }
  }
  return null
}

/** Stop a viewer with one keepalive DELETE (survives navigation and tab close). */
function stopViewer(h: SessionHandle): Promise<void> {
  return bestEffortDelete(
    h.viewerId,
    localStorage.getItem('bowtie.accessToken'),
    streamTokenFromPlaylist(h.playlistUrl),
  )
}

export function Player({ target, onBack }: Props) {
  const { client } = useAuth()
  const videoRef = useRef<HTMLVideoElement | null>(null)
  const hlsRef = useRef<Hls | null>(null)
  const viewerIdRef = useRef<string | null>(null)
  /** The current try at opening a viewer; released on cleanup. */
  const attemptRef = useRef<SessionAttempt | null>(null)
  /** The last stop request, awaited before asking for a new viewer. */
  const pendingStopRef = useRef<Promise<void>>(Promise.resolve())
  const playlistUrlRef = useRef<string | null>(null)
  const hideTimerRef = useRef<number | null>(null)
  const noticeTimerRef = useRef<number | null>(null)
  const heartbeatRef = useRef<ReturnType<typeof createHeartbeatController> | null>(null)
  /** Suppress out-of-window auto-jump while the user is scrubbing. */
  const scrubbingRef = useRef(false)

  const [profile, setProfile] = useState<Profile>('original')
  const [sessionMeta, setSessionMeta] = useState<SessionMeta | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<StartError | null>(null)
  const [playing, setPlaying] = useState(true)
  const [muted, setMuted] = useState(false)
  const [volume, setVolume] = useState(1)
  const [showStats, setShowStats] = useState(false)
  const [overlayVisible, setOverlayVisible] = useState(true)
  const [liveWindow, setLiveWindow] = useState<LiveWindow | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  /** Antenna reception from the latest heartbeat (null = unknown). */
  const [signal, setSignal] = useState<ReceptionSignal | null>(null)
  const signalNote = weakSignalNote(signal)
  const signalLine = signalStatsLine(signal)
  const [hlsStats, setHlsStats] = useState<HlsStats>({
    bandwidth: null,
    droppedFrames: null,
    bufferLength: null,
  })
  const [sessionEpoch, setSessionEpoch] = useState(0)
  const [audioTracks, setAudioTracks] = useState<{ name?: string; lang?: string }[]>([])
  const [audioIdx, setAudioIdx] = useState(0)
  const [hasCaptions, setHasCaptions] = useState(false)
  const [captionsOn, setCaptionsOn] = useState(false)
  const [playingHeight, setPlayingHeight] = useState<number | null>(null)
  const isNarrow = useIsNarrow(640)

  const showNotice = useCallback((msg: string) => {
    setNotice(msg)
    if (noticeTimerRef.current != null) {
      window.clearTimeout(noticeTimerRef.current)
    }
    noticeTimerRef.current = window.setTimeout(() => {
      setNotice(null)
      noticeTimerRef.current = null
    }, NOTICE_HIDE_MS)
  }, [])

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
    setAudioTracks([])
    setHasCaptions(false)
    setCaptionsOn(false)
    setPlayingHeight(null)
    const video = videoRef.current
    if (video) {
      video.removeAttribute('src')
      video.load()
    }
  }

  const seekTo = useCallback(
    (pos: number, opts?: { fromOutOfWindow?: boolean }) => {
      const video = videoRef.current
      if (!video) return
      const w = buildLiveWindow(hlsRef.current, video)
      if (!w) {
        video.currentTime = pos
        return
      }
      const { pos: clamped, clamped: wasClamped } = clampSeek(pos, w)
      video.currentTime = clamped
      setLiveWindow({ ...w, current: clamped })
      if (wasClamped && (opts?.fromOutOfWindow || pos < w.start)) {
        showNotice(OUT_OF_WINDOW_NOTICE)
      }
      bumpOverlay()
    },
    [bumpOverlay, showNotice],
  )

  const onSeekBarSeek = useCallback(
    (pos: number) => {
      scrubbingRef.current = true
      seekTo(pos)
      // Allow auto out-of-window check again after a short settle.
      window.setTimeout(() => {
        scrubbingRef.current = false
      }, 500)
    },
    [seekTo],
  )

  const onSkipBack = useCallback(() => {
    const video = videoRef.current
    if (!video) return
    const w = buildLiveWindow(hlsRef.current, video)
    if (!w) return
    const { pos } = skipBack(w, 30)
    seekTo(pos)
  }, [seekTo])

  const onJumpToLive = useCallback(() => {
    const video = videoRef.current
    if (!video) return
    const w = buildLiveWindow(hlsRef.current, video)
    if (!w) return
    seekTo(w.liveEdge)
  }, [seekTo])

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
      hls.subtitleDisplay = true
      hls.on(Hls.Events.AUDIO_TRACKS_UPDATED, (_e, data) => {
        const tracks = data.audioTracks.map((t) => ({ name: t.name, lang: t.lang }))
        setAudioTracks(tracks)
        const want = pickAudioIndex(tracks, loadTrackPrefs().audioLang)
        if (want >= 0 && want !== hls.audioTrack) hls.audioTrack = want
        setAudioIdx(hls.audioTrack)
      })
      hls.on(Hls.Events.AUDIO_TRACK_SWITCHED, (_e, data) => setAudioIdx(data.id))
      hls.on(Hls.Events.SUBTITLE_TRACKS_UPDATED, (_e, data) => {
        const any = data.subtitleTracks.length > 0
        setHasCaptions(any)
        const on = any && loadTrackPrefs().captions === true
        hls.subtitleTrack = on ? 0 : -1
        setCaptionsOn(on)
      })
      hls.on(Hls.Events.LEVEL_SWITCHED, (_e, data) => {
        setPlayingHeight(hls.levels[data.level]?.height ?? null)
      })
      hls.on(Hls.Events.MANIFEST_PARSED, () => {
        void video.play().catch(() => {
          /* autoplay may require mute */
        })
      })
      hls.on(Hls.Events.ERROR, (_event, data) => {
        if (data.fatal) {
          console.warn('Playback error:', data.type, data.details, data.error)
          setError({
            message: data.type === Hls.ErrorTypes.NETWORK_ERROR
              ? STREAM_STOPPED
              : 'The picture stopped playing. Try again or pick a lower quality.',
            tunerBusy: false,
            retry: true,
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
        message: CANT_PLAY_HERE,
        tunerBusy: false,
        retry: true,
      })
    }
  }, [])

  const onPickAudio = useCallback((i: number) => {
    const hls = hlsRef.current
    if (!hls) return
    hls.audioTrack = i
    saveTrackPrefs({ ...loadTrackPrefs(), audioLang: hls.audioTracks[i]?.lang ?? null })
  }, [])

  const onToggleCaptions = useCallback(() => {
    const hls = hlsRef.current
    if (!hls) return
    const next = hls.subtitleTrack < 0
    hls.subtitleTrack = next ? 0 : -1
    setCaptionsOn(next)
    saveTrackPrefs({ ...loadTrackPrefs(), captions: next })
  }, [])

  // Open the viewer for this channel and quality (again on Try again). Starting
  // can take 10–20 s: whichever of "created" and "cleanup" (Back, a new quality,
  // StrictMode's dev remount) comes second stops the viewer, so leaving while
  // "Starting stream…" never strands a tuner until the server's reaper.
  useEffect(() => {
    const attempt = createSessionAttempt((h) => {
      pendingStopRef.current = stopViewer(h)
    })
    attemptRef.current = attempt
    // The previous viewer (quality change) is stopped before the new one is
    // asked for, so the two never hold two tuners at once.
    const prevStop = pendingStopRef.current
    setLoading(true)
    setError(null)
    setSessionMeta(null)
    setSignal(null)

    void (async () => {
      await prevStop
      if (attempt.released) return
      let res: CreateSessionResponse
      try {
        res = await client.createSession(target.channelId, detectCaps(profile))
      } catch (err) {
        if (attempt.released) return
        setLoading(false)
        // 403 code "parental": the server's message, without Try again.
        setError(startErrorFrom(err))
        return
      }
      if (!attempt.resolve({ viewerId: res.viewerId, playlistUrl: res.playlistUrl })) return
      viewerIdRef.current = res.viewerId
      playlistUrlRef.current = res.playlistUrl
      setSessionMeta(res.session ?? null)
      attachPlayback(res.playlistUrl)
      setLoading(false)
      bumpOverlay()
    })()

    return () => {
      heartbeatRef.current?.stop()
      attempt.release()
      viewerIdRef.current = null
      playlistUrlRef.current = null
      destroyHls()
    }
  }, [attachPlayback, bumpOverlay, client, profile, target.channelId, sessionEpoch])

  // Unmount: timers and heartbeats (the viewer is stopped by the effect above).
  useEffect(() => {
    return () => {
      clearHideTimer()
      if (noticeTimerRef.current != null) {
        window.clearTimeout(noticeTimerRef.current)
      }
      heartbeatRef.current?.stop()
      heartbeatRef.current = null
    }
  }, [])

  // pagehide / beforeunload: stop the viewer (keepalive DELETE). Back from the
  // back/forward cache starts a new one. visibilitychange is used only for
  // heartbeats (A6) — never tears down the session.
  useEffect(() => {
    const onHide = () => {
      heartbeatRef.current?.stop()
      attemptRef.current?.release()
    }
    const onShow = (e: PageTransitionEvent) => {
      if (e.persisted && attemptRef.current?.released) setSessionEpoch((n) => n + 1)
    }
    window.addEventListener('beforeunload', onHide)
    window.addEventListener('pagehide', onHide)
    window.addEventListener('pageshow', onShow)
    return () => {
      window.removeEventListener('beforeunload', onHide)
      window.removeEventListener('pagehide', onHide)
      window.removeEventListener('pageshow', onShow)
    }
  }, [])

  // Heartbeats: keyed on session-open (viewerId + playlist token). Continue through
  // pause/stall; stop only on real leave. visibilitychange-hidden → one beat (A6).
  useEffect(() => {
    if (loading || error) {
      heartbeatRef.current?.stop()
      return
    }

    const send = () => {
      const id = viewerIdRef.current
      const playlist = playlistUrlRef.current
      if (!id || !playlist) return
      const token = streamTokenFromPlaylist(playlist)
      if (!token) return
      void client.heartbeat(id, token).then(
        (reading) => {
          // Ignore a beat that lands after this viewer was replaced or stopped.
          if (viewerIdRef.current === id) setSignal(reading)
        },
        (err: unknown) => {
        // Parental controls stopped the stream (the program changed to a
        // blocked one): show why. Other failures are best-effort.
        if (isParentalBlock(err)) {
          // The server already ended this viewer: nothing to stop on leave.
          attemptRef.current?.forget()
          viewerIdRef.current = null
          destroyHls()
          setError(startErrorFrom(err))
        }
        },
      )
    }

    const ctrl = createHeartbeatController({ send })
    heartbeatRef.current = ctrl

    // Only start when a session is actually open.
    if (viewerIdRef.current && playlistUrlRef.current) {
      ctrl.start()
    }

    const onVis = () => {
      ctrl.handleVisibilityChange(document.visibilityState)
    }
    document.addEventListener('visibilitychange', onVis)
    return () => {
      document.removeEventListener('visibilitychange', onVis)
      ctrl.stop()
      if (heartbeatRef.current === ctrl) {
        heartbeatRef.current = null
      }
    }
  }, [client, loading, error, sessionEpoch])

  // Poll live window + out-of-window clamp (spec B).
  useEffect(() => {
    if (loading || error) {
      setLiveWindow(null)
      return
    }
    const tick = () => {
      const video = videoRef.current
      if (!video) return
      const w = buildLiveWindow(hlsRef.current, video)
      if (!w) {
        setLiveWindow(null)
        return
      }
      setLiveWindow(w)

      // Out-of-window: current fell below sliding window start → jump to live + notice.
      if (!scrubbingRef.current && w.current < w.start - 0.25) {
        const { pos, clamped } = clampSeek(w.current, w)
        if (clamped) {
          video.currentTime = pos
          setLiveWindow({ ...w, current: pos })
          showNotice(OUT_OF_WINDOW_NOTICE)
        }
      }
    }
    tick()
    const id = window.setInterval(tick, LIVE_WINDOW_POLL_MS)
    return () => window.clearInterval(id)
  }, [loading, error, showNotice, sessionEpoch])

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

  const onBackClick = () => {
    // Stop now (even mid-start); don't wait on the network to go back.
    attemptRef.current?.release()
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

        {/* Outside the auto-hiding overlay so it stays put while the signal is weak. */}
        {signalNote && !loading && !error ? (
          <div className={styles.signalNote} role="status" aria-live="polite">
            {signalNote}
          </div>
        ) : null}

        {error ? (
          <div className={styles.errorBox}>
            <p className={styles.errorMsg}>{error.message}</p>
            <div className={styles.errorActions}>
              {error.retry ? (
                <button type="button" className={`${styles.btn} ${styles.btnPrimary}`} onClick={onRetry}>
                  Try again
                </button>
              ) : null}
              <button type="button" className={styles.btn} onClick={onBackClick}>
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
                <span className={styles.statsKey}>playing</span>
                <span>{playingHeight ? `${playingHeight}p` : '—'}</span>
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
              {signalLine ? <div className={styles.statsLine}>{signalLine}</div> : null}
            </div>
          ) : null}

          {notice ? (
            <div className={styles.notice} role="status" aria-live="polite">
              {notice}
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
            <div className={styles.seekRow}>
              <SeekBar
                window={liveWindow}
                disabled={!!error || loading}
                onSeek={onSeekBarSeek}
                onSkipBack={onSkipBack}
                onJumpToLive={onJumpToLive}
              />
            </div>
            <div className={styles.controlRow}>
              <button type="button" className={styles.btn} onClick={onBackClick}>
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
                    title={QUALITY_HINT}
                  >
                    {QUALITY_OPTIONS.map((o) => (
                      <option key={o.value} value={o.value}>
                        {o.label}
                      </option>
                    ))}
                  </select>
                </label>
              )}
              {audioTracks.length > 1 ? (
                <label>
                  <span className="visually-hidden">Audio track</span>
                  <select
                    className={styles.select}
                    value={audioIdx}
                    onChange={(e) => onPickAudio(Number(e.target.value))}
                    aria-label="Audio track"
                  >
                    {audioTracks.map((t, i) => (
                      <option key={i} value={i}>
                        {audioTrackLabel(t, i)}
                      </option>
                    ))}
                  </select>
                </label>
              ) : null}
              {hasCaptions ? (
                <button
                  type="button"
                  className={`${styles.btn} ${captionsOn ? styles.btnPrimary : ''}`}
                  onClick={() => {
                    onToggleCaptions()
                    bumpOverlay()
                  }}
                  aria-pressed={captionsOn}
                  aria-label="Closed captions"
                >
                  CC
                </button>
              ) : null}
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
    </div>
  )
}
