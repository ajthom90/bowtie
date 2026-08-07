import { useState } from 'react'
import { Admin } from './admin/Admin'
import { AuthProvider, useAuth } from './auth/AuthContext'
import { Login } from './auth/Login'
import { Guide, type WatchTarget } from './guide/Guide'
import { Player } from './player/Player'
import styles from './App.module.css'

type View = 'guide' | 'admin'

function Shell() {
  const { user, ready } = useAuth()
  const [watching, setWatching] = useState<WatchTarget | null>(null)
  const [view, setView] = useState<View>('guide')

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

  // Role guard: viewers never see admin route or nav entry.
  // A5: Preview opens the player via setWatching; Player Back returns to Guide
  // (accepted simplification — does not restore the Admin tab).
  if (view === 'admin' && user.role === 'admin') {
    return (
      <Admin
        onBack={() => setView('guide')}
        onPreview={(t) => setWatching(t)}
      />
    )
  }

  return (
    <Guide
      onWatch={setWatching}
      onAdmin={user.role === 'admin' ? () => setView('admin') : undefined}
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
