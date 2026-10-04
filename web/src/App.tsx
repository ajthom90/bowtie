import { useState } from 'react'
import { Admin } from './admin/Admin'
import type { Recording } from './api/client'
import { AuthProvider, useAuth } from './auth/AuthContext'
import { Login } from './auth/Login'
import { Guide, type WatchTarget } from './guide/Guide'
import { Player } from './player/Player'
import { RecordingPlayer } from './recordings/RecordingPlayer'
import { Recordings } from './recordings/Recordings'
import type { RecordingsTab } from './recordings/recordingsModel'
import styles from './App.module.css'

type View = 'guide' | 'admin' | 'recordings'

function Shell() {
  const { user, ready } = useAuth()
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

  if (!user) {
    return <Login />
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

  if (view === 'recordings') {
    return (
      <Recordings
        tab={recordingsTab}
        onTab={setRecordingsTab}
        onGuide={() => setView('guide')}
        onAdmin={onAdmin}
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
      />
    )
  }

  return (
    <Guide
      onWatch={setWatching}
      onAdmin={onAdmin}
      onRecordings={() => setView('recordings')}
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
