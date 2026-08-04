import { useState } from 'react'
import { AuthProvider, useAuth } from './auth/AuthContext'
import { Login } from './auth/Login'
import { Guide, type WatchTarget } from './guide/Guide'
import { Player } from './player/Player'
import styles from './App.module.css'

function Shell() {
  const { user, ready } = useAuth()
  const [watching, setWatching] = useState<WatchTarget | null>(null)

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

  return <Guide onWatch={setWatching} />
}

export default function App() {
  return (
    <AuthProvider>
      <Shell />
    </AuthProvider>
  )
}
