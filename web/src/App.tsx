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
import { MULTIVIEW_PATH, isMultiviewPath } from './multiview/multiviewModel'
import { Player } from './player/Player'
import { RecordingPlayer } from './recordings/RecordingPlayer'
import { Recordings } from './recordings/Recordings'
import type { RecordingsTab } from './recordings/recordingsModel'
import styles from './App.module.css'

type View = 'guide' | 'admin' | 'recordings' | 'account'

/** The current path, kept in step with back/forward. No router: only /link and /multiview are real routes. */
function usePath(): [string, (to: string) => void] {
  const [path, setPath] = useState(() => window.location.pathname)
  useEffect(() => {
    const onPop = () => setPath(window.location.pathname)
    window.addEventListener('popstate', onPop)
    return () => window.removeEventListener('popstate', onPop)
  }, [])
  const navigate = useCallback((to: string) => {
    window.history.pushState(null, '', to)
    setPath(window.location.pathname)
  }, [])
  return [path, navigate]
}

function Shell() {
  const { user, ready } = useAuth()
  const [path, navigate] = usePath()
  const onLink = isLinkPath(path)
  const [watching, setWatchingState] = useState<WatchTarget | null>(null)
  const [view, setView] = useState<View>('guide')
  /** The Admin section to open on (the guide's "no guide data" hint opens EPG). */
  const [adminTab, setAdminTab] = useState<AdminTab>('tuners')
  const [recordingsTab, setRecordingsTab] = useState<RecordingsTab>('upcoming')
  const [playingRecording, setPlayingRecordingState] = useState<Recording | null>(null)
  /** The control that opened a player, focused again when the page returns. */
  const returnFocusRef = useRef<FocusMark | null>(null)
  const [focusEpoch, setFocusEpoch] = useState(0)
  const openPlayer = () => {
    returnFocusRef.current = focusMarkOf(document.activeElement)
  }
  const closePlayer = () => setFocusEpoch((n) => n + 1)
  const setWatching = (t: WatchTarget | null) => {
    if (t) openPlayer()
    else closePlayer()
    setWatchingState(t)
  }
  const setPlayingRecording = (rec: Recording | null) => {
    if (rec) openPlayer()
    else closePlayer()
    setPlayingRecordingState(rec)
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
    return (
      <LinkPage
        onDone={() => {
          setView('guide')
          navigate('/')
        }}
      />
    )
  }

  const toGuide = () => {
    setView('guide')
    navigate('/')
  }
  const onMultiview = () => navigate(MULTIVIEW_PATH)

  if (inMultiview) {
    return <Multiview onGuide={toGuide} />
  }

  if (watching) {
    return <Player target={watching} onBack={() => setWatching(null)} />
  }

  // Recording playback returns to where it started (Recordings tab or guide).
  if (playingRecording) {
    return (
      <RecordingPlayer
        recording={playingRecording}
        autoResume={autoResume}
        onBack={() => setPlayingRecording(null)}
      />
    )
  }

  const openAdmin = (tab: AdminTab) => {
    setAdminTab(tab)
    setView('admin')
  }
  const onAdmin = user.role === 'admin' ? () => openAdmin('tuners') : undefined
  const onAdminEpg = user.role === 'admin' ? () => openAdmin('epg') : undefined
  const onAccount = () => setView('account')

  if (view === 'account') {
    return (
      <Account
        onGuide={() => setView('guide')}
        onRecordings={() => setView('recordings')}
        onMultiview={onMultiview}
        onAdmin={onAdmin}
        onLink={() => navigate('/link')}
      />
    )
  }

  if (view === 'recordings') {
    return (
      <Recordings
        tab={recordingsTab}
        onTab={setRecordingsTab}
        onGuide={() => setView('guide')}
        onMultiview={onMultiview}
        onAdmin={onAdmin}
        onAccount={onAccount}
        onPlay={playRecording}
        onResume={resumeRecording}
      />
    )
  }

  // Role guard: viewers never see admin route or nav entry.
  // A5: Preview opens the player via setWatching; Player Back returns to Guide
  // (accepted simplification — does not restore the Admin tab).
  if (view === 'admin' && user.role === 'admin') {
    return (
      <Admin
        onBack={() => setView('guide')}
        onPreview={(t) => setWatching(t)}
        onRecordings={() => setView('recordings')}
        onAccount={onAccount}
        initialTab={adminTab}
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
      onRecordings={() => setView('recordings')}
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
