import { useCallback, useEffect, useState } from 'react'
import { Account } from './account/Account'
import { Admin } from './admin/Admin'
import type { Recording } from './api/client'
import { AuthProvider, useAuth } from './auth/AuthContext'
import { LinkPage } from './auth/LinkPage'
import { isLinkPath } from './auth/linkModel'
import { Login } from './auth/Login'
import { Guide, type WatchTarget } from './guide/Guide'
import { Player } from './player/Player'
import { RecordingPlayer } from './recordings/RecordingPlayer'
import { Recordings } from './recordings/Recordings'
import type { RecordingsTab } from './recordings/recordingsModel'
import styles from './App.module.css'

type View = 'guide' | 'admin' | 'recordings' | 'account'

/** The current path, kept in step with back/forward. No router: only /link is a real route. */
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
  const [watching, setWatching] = useState<WatchTarget | null>(null)
  const [view, setView] = useState<View>('guide')
  const [recordingsTab, setRecordingsTab] = useState<RecordingsTab>('upcoming')
  const [playingRecording, setPlayingRecording] = useState<Recording | null>(null)

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

  if (watching) {
    return <Player target={watching} onBack={() => setWatching(null)} />
  }

  // Recording playback returns to the Recordings page (same tab).
  if (playingRecording) {
    return (
      <RecordingPlayer recording={playingRecording} onBack={() => setPlayingRecording(null)} />
    )
  }

  const onAdmin = user.role === 'admin' ? () => setView('admin') : undefined
  const onAccount = () => setView('account')

  if (view === 'account') {
    return (
      <Account
        onGuide={() => setView('guide')}
        onRecordings={() => setView('recordings')}
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
        onAdmin={onAdmin}
        onAccount={onAccount}
        onPlay={setPlayingRecording}
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
      />
    )
  }

  return (
    <Guide
      onWatch={setWatching}
      onAdmin={onAdmin}
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
