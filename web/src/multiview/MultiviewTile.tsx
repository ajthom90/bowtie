import Hls from 'hls.js'
import { useEffect, useRef, useState, type MouseEvent as ReactMouseEvent } from 'react'
import { useAuth } from '../auth/AuthContext'
import { canPlayNativeHls, detectCaps } from '../player/caps'
import { isParentalBlock, startErrorFrom, type StartError } from '../player/errorModel'
import { createHeartbeatController } from '../player/seekModel'
import { bestEffortDelete, streamTokenFromPlaylist } from '../player/sessionStop'
import type { Tile } from './multiviewModel'
import styles from './Multiview.module.css'

type Props = {
  tile: Tile
  /** Position (0-based); keys 1–4 pick tiles by position. */
  index: number
  audio: boolean
  programTitle?: string
  onAudio: () => void
  onChange: () => void
  onRemove: () => void
}

type WebkitVideo = HTMLVideoElement & { webkitEnterFullscreen?: () => void }

const PLAYBACK_FAILED: StartError = {
  message: 'Playback failed. Try again.',
  tunerBusy: false,
  retry: true,
}

/** One live channel: its own viewer, heartbeat and hls.js instance. */
export function MultiviewTile({ tile, index, audio, programTitle, onAudio, onChange, onRemove }: Props) {
  const { client } = useAuth()
  const wrapRef = useRef<HTMLDivElement | null>(null)
  const videoRef = useRef<HTMLVideoElement | null>(null)
  const audioRef = useRef(audio)
  audioRef.current = audio
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<StartError | null>(null)
  /** The browser refused to play this tile with sound until a tap. */
  const [soundBlocked, setSoundBlocked] = useState(false)
  const [epoch, setEpoch] = useState(0)

  const { channelId } = tile.channel
  const { profile } = tile

  useEffect(() => {
    let cancelled = false
    let released = false
    let viewerId: string | null = null
    let playlistUrl: string | null = null
    let hls: Hls | null = null
    const heartbeat = createHeartbeatController({
      send: () => {
        const token = playlistUrl ? streamTokenFromPlaylist(playlistUrl) : null
        if (!viewerId || !token) return
        void client.heartbeat(viewerId, token).catch((err: unknown) => {
          if (isParentalBlock(err)) {
            viewerId = null
            release()
            setError(startErrorFrom(err))
          }
        })
      },
    })

    // Stops the viewer (keepalive, so it survives navigation and tab close).
    // Safe to call again: a session that resolves after cleanup is stopped too.
    const release = () => {
      released = true
      heartbeat.stop()
      if (hls) {
        hls.destroy()
        hls = null
      }
      const video = videoRef.current
      if (video) {
        video.removeAttribute('src')
        video.load()
      }
      if (viewerId) {
        const token = playlistUrl ? streamTokenFromPlaylist(playlistUrl) : null
        bestEffortDelete(viewerId, localStorage.getItem('bowtie.accessToken'), token)
        viewerId = null
      }
    }

    const play = (video: HTMLVideoElement) => {
      video.muted = !audioRef.current
      void video.play().catch(() => {
        if (cancelled || video.muted) return
        // Autoplay with sound refused: play muted until the tile is tapped.
        video.muted = true
        setSoundBlocked(true)
        void video.play().catch(() => {})
      })
    }

    const attach = (url: string) => {
      const video = videoRef.current
      if (!video) return
      if (Hls.isSupported()) {
        hls = new Hls({ enableWorker: true, lowLatencyMode: false })
        hls.loadSource(url)
        hls.attachMedia(video)
        hls.on(Hls.Events.MANIFEST_PARSED, () => play(video))
        hls.on(Hls.Events.ERROR, (_e, data) => {
          if (!data.fatal || cancelled) return
          release()
          setError(
            data.type === Hls.ErrorTypes.NETWORK_ERROR
              ? { ...PLAYBACK_FAILED, message: 'Playback failed — network error. Try again.' }
              : PLAYBACK_FAILED,
          )
        })
      } else if (canPlayNativeHls()) {
        video.src = url
        play(video)
      } else {
        release()
        setError({ ...PLAYBACK_FAILED, message: 'This browser cannot play HLS video.' })
      }
    }

    setLoading(true)
    setError(null)
    client.createSession(channelId, detectCaps(profile)).then(
      (res) => {
        viewerId = res.viewerId
        playlistUrl = res.playlistUrl
        // Unmounted or the page is unloading: stop it straight away.
        if (cancelled || released) {
          release()
          return
        }
        setLoading(false)
        attach(res.playlistUrl)
        if (!released) heartbeat.start()
      },
      (err: unknown) => {
        if (cancelled) return
        setLoading(false)
        setError(startErrorFrom(err))
      },
    )

    const onVis = () => heartbeat.handleVisibilityChange(document.visibilityState)
    // Back from the back/forward cache after pagehide stopped the viewer: start again.
    const onShow = (e: PageTransitionEvent) => {
      if (e.persisted && released && !cancelled) setEpoch((n) => n + 1)
    }
    document.addEventListener('visibilitychange', onVis)
    window.addEventListener('pagehide', release)
    window.addEventListener('beforeunload', release)
    window.addEventListener('pageshow', onShow)
    return () => {
      cancelled = true
      document.removeEventListener('visibilitychange', onVis)
      window.removeEventListener('pageshow', onShow)
      window.removeEventListener('pagehide', release)
      window.removeEventListener('beforeunload', release)
      release()
    }
  }, [client, channelId, profile, epoch])

  useEffect(() => {
    const video = videoRef.current
    if (!video) return
    if (!audio) {
      video.muted = true
      setSoundBlocked(false)
      return
    }
    video.muted = false
    if (video.paused && !video.currentSrc) return
    void video.play().catch(() => {
      video.muted = true
      setSoundBlocked(true)
      void video.play().catch(() => {})
    })
  }, [audio])

  const takeAudio = () => {
    onAudio()
    const video = videoRef.current
    if (video && (soundBlocked || !audio)) {
      video.muted = false
      setSoundBlocked(false)
      void video.play().catch(() => {})
    }
  }

  const stop = (e: ReactMouseEvent) => e.stopPropagation()

  const toggleFullscreen = (e: ReactMouseEvent) => {
    e.stopPropagation()
    takeAudio()
    if (document.fullscreenElement) {
      void document.exitFullscreen?.()
      return
    }
    const wrap = wrapRef.current
    if (wrap?.requestFullscreen) {
      void wrap.requestFullscreen().catch(() => {})
    } else {
      ;(videoRef.current as WebkitVideo | null)?.webkitEnterFullscreen?.()
    }
  }

  const { guideNumber, name } = tile.channel
  const label = `Tile ${index + 1}: channel ${guideNumber} ${name}`

  return (
    <div
      ref={wrapRef}
      className={`${styles.tile}${audio ? ` ${styles.tileAudio}` : ''}`}
      onClick={takeAudio}
      role="group"
      aria-label={audio ? `${label}, playing sound` : label}
    >
      <video ref={videoRef} className={styles.video} playsInline autoPlay muted />

      {loading && !error ? <div className={styles.tileStatus}>Starting stream…</div> : null}

      {error ? (
        <div className={styles.tileError} role="alert">
          <p className={styles.tileErrorMsg}>{error.message}</p>
          <div className={styles.tileErrorActions} onClick={stop}>
            {error.retry ? (
              <button
                type="button"
                className={`${styles.btn} ${styles.btnPrimary}`}
                onClick={() => setEpoch((n) => n + 1)}
              >
                Try again
              </button>
            ) : null}
            <button type="button" className={styles.btn} onClick={onChange}>
              Change channel
            </button>
            <button type="button" className={styles.btn} onClick={onRemove}>
              Remove
            </button>
          </div>
        </div>
      ) : null}

      <div className={styles.tileTop}>
        <span className={styles.tileIndex} aria-hidden>
          {index + 1}
        </span>
        <span className={styles.tileNum}>{guideNumber}</span>
        <span className={styles.tileMeta}>
          <span className={styles.tileName}>{name}</span>
          {programTitle ? <span className={styles.tileProgram}>{programTitle}</span> : null}
        </span>
        {audio ? (
          <span className={styles.soundBadge}>{soundBlocked ? 'Tap for sound' : 'Sound'}</span>
        ) : null}
      </div>

      <div className={styles.tileControls} onClick={stop}>
        <button
          type="button"
          className={`${styles.ctl}${audio ? ` ${styles.ctlOn}` : ''}`}
          onClick={takeAudio}
          aria-pressed={audio}
          aria-label={`Sound from channel ${guideNumber}`}
          title={`Sound (${index + 1})`}
        >
          Sound
        </button>
        <button
          type="button"
          className={styles.ctl}
          onClick={onChange}
          aria-label={`Change channel ${guideNumber}`}
        >
          Change
        </button>
        <button
          type="button"
          className={styles.ctl}
          onClick={toggleFullscreen}
          aria-label={`Full screen channel ${guideNumber}`}
        >
          Full screen
        </button>
        <button
          type="button"
          className={styles.ctl}
          onClick={onRemove}
          aria-label={`Remove channel ${guideNumber}`}
        >
          Remove
        </button>
      </div>
    </div>
  )
}
