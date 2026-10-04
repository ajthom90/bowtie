import { useCallback, useEffect, useRef, useState } from 'react'
import { Account } from './account/Account'
import { Admin, type AdminTab } from './admin/Admin'
import type { Recording } from './api/client'
import { AuthProvider, useAuth } from './auth/AuthContext'
import { LinkPage } from './auth/LinkPage'
import { focusMarkOf, restoreFocus, type FocusMark } from './focusReturn'
import { isLinkPath } from './auth/linkModel'
import { Login } from './auth/Login'
import { Guide, type WatchTarget } from './guide/Guide'
import { Multiview } from './multiview/Multiview'
import { isMultiviewPath } from './multiview/multiviewModel'
import { Player } from './player/Player'
import { RecordingPlayer } from './recordings/RecordingPlayer'
import { Recordings } from './recordings/Recordings'
import { parseRoute, pathFor, type Route } from './routes'
import styles from './App.module.css'

type NavigateOptions = { replace?: boolean }

/** The current path, kept in step with back/forward (no router; see routes.ts). */
function usePath(): [string, (to: string, opts?: NavigateOptions) => void] {
  const [path, setPath] = useState(() => window.location.pathname)
  useEffect(() => {
    const onPop = () => setPath(window.location.pathname)
    window.addEventListener('popstate', onPop)
    return () => window.removeEventListener('popstate', onPop)
  }, [])
  const navigate = useCallback((to: string, opts?: NavigateOptions) => {
    if (opts?.replace) window.history.replaceState(null, '', to)
    else window.history.pushState(null, '', to)
    setPath(window.location.pathname)
  }, [])
  return [path, navigate]
}

function Shell() {
  const { user, ready } = useAuth()
  const [path, navigate] = usePath()
  const route = parseRoute(path)
  const go = (r: Route, opts?: NavigateOptions) => navigate(pathFor(r), opts)
  const onLink = isLinkPath(path)
  // Players keep their target in state; their path (/watch/…, /recordings/play/…)
  // gives them a history entry so Back (or a phone's Back gesture) closes them.
  const [watching, setWatchingState] = useState<WatchTarget | null>(null)
  const [playingRecording, setPlayingRecordingState] = useState<Recording | null>(null)
  /** The control that opened a player, focused again when the page returns. */
  const returnFocusRef = useRef<FocusMark | null>(null)
  const [focusEpoch, setFocusEpoch] = useState(0)
  const openPlayer = () => {
    returnFocusRef.current = focusMarkOf(document.activeElement)
  }
  /** A player's Back pops its history entry; the effect below then closes it. */
  const leavePlayer = () => window.history.back()
  const setWatching = (t: WatchTarget) => {
    openPlayer()
    setWatchingState(t)
    go({ view: 'watch', channelId: t.channelId })
  }
  const setPlayingRecording = (rec: Recording) => {
    openPlayer()
    setPlayingRecordingState(rec)
    go({ view: 'playRecording', recordingId: rec.id })
  }
  useEffect(() => {
    if (focusEpoch === 0) return
    const mark = returnFocusRef.current
    returnFocusRef.current = null
    return restoreFocus(mark)
  }, [focusEpoch])
  /** Continue watching: start at the saved position without asking. */
  const [autoResume, setAutoResume] = useState(false)
  const playRecording = (rec: Recording) => {
    setAutoResume(false)
    setPlayingRecording(rec)
  }
  const resumeRecording = (rec: Recording) => {
    setAutoResume(true)
    setPlayingRecording(rec)
  }
  const inMultiview = isMultiviewPath(path)
  useEffect(() => {
    // Multiview replaces any player (e.g. via browser Back): that player must
    // not come back, and start its stream again, when the guide returns.
    if (inMultiview) {
      setWatchingState(null)
      setPlayingRecordingState(null)
    }
  }, [inMultiview])
  // Leaving a player's path (Back, Forward elsewhere) closes it and returns focus.
  const routeView = route.view
  useEffect(() => {
    let closed = false
    if (routeView !== 'watch' && watching) {
      setWatchingState(null)
      closed = true
    }
    if (routeView !== 'playRecording' && playingRecording) {
      setPlayingRecordingState(null)
      closed = true
    }
    if (closed) setFocusEpoch((n) => n + 1)
  }, [routeView, watching, playingRecording])
  // A player path with no player (refresh, Forward): never auto-start a stream
  // (tuners are shared) — fall back to the guide or Recordings instead.
  const orphanPlayer =
    (routeView === 'watch' && !watching) || (routeView === 'playRecording' && !playingRecording)
  useEffect(() => {
    if (!orphanPlayer || !user) return
    navigate(routeView === 'playRecording' ? '/recordings' : '/', { replace: true })
  }, [orphanPlayer, routeView, user, navigate])

  if (!ready) {
    return (
      <div className={styles.centered}>
        <p className={styles.muted}>Loading…</p>
      </div>
    )
  }

  // /link (quick sign-in for a TV): sign in first, then approve.
  if (!user) {
    return <Login subtitle={onLink ? 'Sign in to approve your TV' : undefined} />
  }

  if (onLink) {
    return <LinkPage onDone={() => navigate('/')} />
  }

  const toGuide = () => go({ view: 'guide' })
  const onMultiview = () => go({ view: 'multiview' })

  if (inMultiview) {
    return <Multiview onGuide={toGuide} />
  }

  if (routeView === 'watch' && watching) {
    return <Player target={watching} onBack={leavePlayer} />
  }

  // Recording playback returns to where it started (Recordings tab or guide).
  if (routeView === 'playRecording' && playingRecording) {
    return (
      <RecordingPlayer recording={playingRecording} autoResume={autoResume} onBack={leavePlayer} />
    )
  }

  const isAdmin = user.role === 'admin'
  const openAdmin = (tab: AdminTab) => go({ view: 'admin', tab })
  const onAdmin = isAdmin ? () => openAdmin('tuners') : undefined
  const onAdminEpg = isAdmin ? () => openAdmin('epg') : undefined
  const onAccount = () => go({ view: 'account' })
  const onRecordings = () => go({ view: 'recordings', tab: 'upcoming' })

  if (route.view === 'account') {
    return (
      <Account
        onGuide={toGuide}
        onRecordings={onRecordings}
        onMultiview={onMultiview}
        onAdmin={onAdmin}
        onLink={() => navigate('/link')}
      />
    )
  }

  // An orphaned recording-player path shows Recordings until it is replaced.
  if (route.view === 'recordings' || route.view === 'playRecording') {
    return (
      <Recordings
        tab={route.view === 'recordings' ? route.tab : 'upcoming'}
        // Tabs replace the entry: Back leaves Recordings rather than stepping tabs.
        onTab={(tab) => go({ view: 'recordings', tab }, { replace: true })}
        onGuide={toGuide}
        onMultiview={onMultiview}
        onAdmin={onAdmin}
        onAccount={onAccount}
        onPlay={playRecording}
        onResume={resumeRecording}
      />
    )
  }

  // Role guard: viewers never see the admin area or nav entry (/admin → guide).
  // Preview opens the player; its Back returns to Admin → Channels.
  if (route.view === 'admin' && isAdmin) {
    return (
      <Admin
        onBack={toGuide}
        onPreview={setWatching}
        onRecordings={onRecordings}
        onAccount={onAccount}
        tab={route.tab}
        onTab={(tab) => go({ view: 'admin', tab }, { replace: true })}
      />
    )
  }

  return (
    <Guide
      onWatch={setWatching}
      onResumeRecording={resumeRecording}
      onMultiview={onMultiview}
      onAdmin={onAdmin}
      onAdminEpg={onAdminEpg}
      onRecordings={onRecordings}
      onAccount={onAccount}
    />
  )
}

export default function App() {
  return (
    <AuthProvider>
      <Shell />
    </AuthProvider>
  )
}
