import Hls from 'hls.js'
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { ApiError, type CommercialBreak, type Recording } from '../api/client'
import { useAuth } from '../auth/AuthContext'
import { canPlayNativeHls } from '../player/caps'
import { loadTrackPrefs, pickAudioIndex } from '../player/tracksModel'
import { createCommercialSkipper, isSkipKey, loadAutoSkip, saveAutoSkip } from './commercialsModel'
import {
  createPositionSaver,
  formatClock,
  formatWhen,
  resumeDecision,
} from './recordingsModel'
import playerStyles from '../player/Player.module.css'
import styles from './RecordingPlayer.module.css'

type Props = {
  recording: Recording
  onBack: () => void
}

type Phase =
  | { kind: 'loading' }
  | { kind: 'ask'; playlistUrl: string; positionSec: number }
  | { kind: 'playing' }
  | { kind: 'error'; message: string }

/** Best-effort position save while the page goes away (keepalive PUT with bearer). */
function keepaliveSavePosition(id: number, positionSec: number) {
  const access = localStorage.getItem('bowtie.accessToken')
  try {
    void fetch(`/api/v1/recordings/${id}/position`, {
      method: 'PUT',
      headers: {
        'Content-Type': 'application/json',
        ...(access ? { Authorization: `Bearer ${access}` } : {}),
      },
      body: JSON.stringify({ positionSec: Math.max(0, Math.floor(positionSec)) }),
      keepalive: true,
    })
  } catch {
    // ignore
  }
}

/**
 * VOD player for a finished recording: hls.js (native HLS on Safari), the
 * browser's own seekable controls, resume from the saved position, and the
 * position saved every 15 s and on close. No live edge, no heartbeat.
 */
export function RecordingPlayer({ recording, onBack }: Props) {
  const { client } = useAuth()
  const videoRef = useRef<HTMLVideoElement | null>(null)
  const hlsRef = useRef<Hls | null>(null)
  /** Playback actually started — positions before that would clobber the saved one. */
  const startedRef = useRef(false)
  /** Last position seen while playing; survives the video element being reset or unmounted. */
  const lastPosRef = useRef<number | null>(null)
  const [phase, setPhase] = useState<Phase>({ kind: 'loading' })
  const [epoch, setEpoch] = useState(0)

  const getPosition = useCallback((): number | null => lastPosRef.current, [])

  // Commercial breaks: a Skip button while inside one, and optional auto-skip
  // (each break once; seeking back into one doesn't skip it again).
  // Keyed on the breaks' content, so a re-fetched copy of the same recording
  // keeps the auto-skip memory.
  const breaksKey = JSON.stringify(recording.commercials ?? [])
  const skipper = useMemo(
    () => createCommercialSkipper(JSON.parse(breaksKey) as CommercialBreak[]),
    [breaksKey],
  )
  const [autoSkip, setAutoSkip] = useState(() => loadAutoSkip())
  const autoSkipRef = useRef(autoSkip)
  const [currentBreak, setCurrentBreak] = useState<CommercialBreak | null>(null)

  const toggleAutoSkip = (on: boolean) => {
    autoSkipRef.current = on
    setAutoSkip(on)
    saveAutoSkip(on)
  }

  const onTimeUpdate = () => {
    const v = videoRef.current
    if (!v) return
    if (startedRef.current) lastPosRef.current = v.currentTime
    // readyState 0: the element is being reset (currentTime jumps to 0).
    if (skipper.breaks.length === 0 || v.readyState === 0) return
    const d = skipper.update(v.currentTime, autoSkipRef.current)
    if (d.seekTo != null) {
      v.currentTime = d.seekTo
      setCurrentBreak(null)
    } else {
      setCurrentBreak(d.current)
    }
  }

  const skipBreak = useCallback((): boolean => {
    const v = videoRef.current
    if (!v || v.readyState === 0) return false
    const to = skipper.skip(v.currentTime)
    if (to == null) return false
    v.currentTime = to
    setCurrentBreak(null)
    return true
  }, [skipper])

  // S skips the break the playhead is in.
  useEffect(() => {
    if (skipper.breaks.length === 0) return
    const onKey = (e: KeyboardEvent) => {
      if (isSkipKey(e) && skipBreak()) e.preventDefault()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [skipper, skipBreak])

  const saverRef = useRef<ReturnType<typeof createPositionSaver> | null>(null)
  if (saverRef.current === null) {
    saverRef.current = createPositionSaver({
      save: (sec) => client.setRecordingPosition(recording.id, sec),
      getPosition,
    })
  }

  const destroy = useCallback(() => {
    // Stop tracking first: resetting the element jumps currentTime to 0.
    startedRef.current = false
    if (hlsRef.current) {
      hlsRef.current.destroy()
      hlsRef.current = null
    }
    const v = videoRef.current
    if (v) {
      v.removeAttribute('src')
      v.load()
    }
  }, [])

  const attach = useCallback(
    (playlistUrl: string, startAt: number) => {
      const video = videoRef.current
      if (!video) return
      destroy()
      setPhase({ kind: 'playing' })

      const onPlaying = () => {
        startedRef.current = true
        lastPosRef.current = video.currentTime
        saverRef.current?.start()
      }
      video.addEventListener('playing', onPlaying, { once: true })

      if (Hls.isSupported()) {
        const hls = new Hls({ enableWorker: true, startPosition: startAt > 0 ? startAt : -1 })
        hlsRef.current = hls
        hls.loadSource(playlistUrl)
        hls.attachMedia(video)
        hls.subtitleDisplay = true
        hls.on(Hls.Events.AUDIO_TRACKS_UPDATED, (_e, data) => {
          const tracks = data.audioTracks.map((t) => ({ name: t.name, lang: t.lang }))
          const want = pickAudioIndex(tracks, loadTrackPrefs().audioLang)
          if (want >= 0 && want !== hls.audioTrack) hls.audioTrack = want
        })
        hls.on(Hls.Events.SUBTITLE_TRACKS_UPDATED, (_e, data) => {
          const on = data.subtitleTracks.length > 0 && loadTrackPrefs().captions === true
          hls.subtitleTrack = on ? 0 : -1
        })
        hls.on(Hls.Events.MANIFEST_PARSED, () => {
          void video.play().catch(() => {
            /* autoplay may be blocked; the controls still work */
          })
        })
        hls.on(Hls.Events.ERROR, (_e, data) => {
          if (data.fatal) {
            setPhase({
              kind: 'error',
              message:
                data.type === Hls.ErrorTypes.NETWORK_ERROR
                  ? 'Playback failed — network error. Try again.'
                  : 'Playback failed. Try again.',
            })
          }
        })
      } else if (canPlayNativeHls()) {
        if (startAt > 0) {
          video.addEventListener(
            'loadedmetadata',
            () => {
              video.currentTime = startAt
            },
            { once: true },
          )
        }
        video.src = playlistUrl
        void video.play().catch(() => {
          /* autoplay may be blocked; the controls still work */
        })
      } else {
        setPhase({ kind: 'error', message: 'This browser cannot play HLS video.' })
      }
    },
    [destroy],
  )

  // Fetch the playback URL and resume position, then ask or start.
  useEffect(() => {
    let cancelled = false
    setPhase({ kind: 'loading' })
    client
      .playRecording(recording.id)
      .then((res) => {
        if (cancelled) return
        const d = resumeDecision(res.positionSec, res.durationSec || recording.durationSec)
        if (d.kind === 'ask') {
          setPhase({ kind: 'ask', playlistUrl: res.playlistUrl, positionSec: d.positionSec })
        } else {
          attach(res.playlistUrl, 0)
        }
      })
      .catch((err: unknown) => {
        if (cancelled) return
        setPhase({
          kind: 'error',
          message:
            err instanceof ApiError && err.message ? err.message : 'Could not start playback.',
        })
      })
    return () => {
      cancelled = true
    }
  }, [attach, client, recording.id, recording.durationSec, epoch])

  // Page hide / unload: save where we are without waiting.
  useEffect(() => {
    const onHide = () => {
      const pos = getPosition()
      if (pos != null) keepaliveSavePosition(recording.id, pos)
    }
    window.addEventListener('pagehide', onHide)
    return () => window.removeEventListener('pagehide', onHide)
  }, [getPosition, recording.id])

  // Unmount: stop the timer, save once more, tear down.
  useEffect(() => {
    const saver = saverRef.current
    return () => {
      saver?.stop()
      void saver?.flush()
      if (hlsRef.current) {
        hlsRef.current.destroy()
        hlsRef.current = null
      }
    }
  }, [])

  const close = async () => {
    const saver = saverRef.current
    saver?.stop()
    await saver?.flush()
    destroy()
    onBack()
  }

  const retry = () => {
    saverRef.current?.stop()
    destroy()
    setEpoch((n) => n + 1)
  }

  return (
    <div className={playerStyles.stage} aria-label={`Playing ${recording.title}`}>
      <header className={styles.bar}>
        <button type="button" className={playerStyles.btn} onClick={() => void close()}>
          Back
        </button>
        <div className={styles.titles}>
          <span className={styles.title}>{recording.title}</span>
          <span className={styles.meta}>
            {[recording.subtitle, recording.channelName, formatWhen(recording.start, recording.stop)]
              .filter(Boolean)
              .join(' · ')}
          </span>
        </div>
        {skipper.breaks.length > 0 ? (
          <label className={styles.autoSkip}>
            <input
              type="checkbox"
              checked={autoSkip}
              onChange={(e) => toggleAutoSkip(e.target.checked)}
            />
            Auto-skip ads
          </label>
        ) : null}
      </header>

      <div className={playerStyles.videoWrap}>
        <video
          ref={videoRef}
          className={playerStyles.video}
          playsInline
          controls
          onTimeUpdate={onTimeUpdate}
        />

        {phase.kind === 'playing' && currentBreak ? (
          <button
            type="button"
            className={`${playerStyles.btn} ${playerStyles.btnPrimary} ${styles.skipAd}`}
            onClick={() => skipBreak()}
            aria-keyshortcuts="S"
            title="Skip to the end of this commercial break (S)"
          >
            Skip ad ▸
          </button>
        ) : null}

        {phase.kind === 'loading' ? <div className={playerStyles.loading}>Loading…</div> : null}

        {phase.kind === 'ask' ? (
          <div className={playerStyles.errorBox} role="dialog" aria-label="Resume playback">
            <p className={playerStyles.errorMsg}>Pick up where you left off?</p>
            <div className={playerStyles.errorActions}>
              <button
                type="button"
                className={`${playerStyles.btn} ${playerStyles.btnPrimary}`}
                autoFocus
                onClick={() => attach(phase.playlistUrl, phase.positionSec)}
              >
                Resume from {formatClock(phase.positionSec)}
              </button>
              <button
                type="button"
                className={playerStyles.btn}
                onClick={() => attach(phase.playlistUrl, 0)}
              >
                Start over
              </button>
            </div>
          </div>
        ) : null}

        {phase.kind === 'error' ? (
          <div className={playerStyles.errorBox}>
            <p className={playerStyles.errorMsg}>{phase.message}</p>
            <div className={playerStyles.errorActions}>
              <button
                type="button"
                className={`${playerStyles.btn} ${playerStyles.btnPrimary}`}
                onClick={retry}
              >
                Try again
              </button>
              <button type="button" className={playerStyles.btn} onClick={() => void close()}>
                Back to recordings
              </button>
            </div>
          </div>
        ) : null}
      </div>
    </div>
  )
}
