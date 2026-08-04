import { AuthProvider, useAuth } from './auth/AuthContext'
import { Login } from './auth/Login'
import styles from './App.module.css'

function Shell() {
  const { user, ready, logout } = useAuth()

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

  return (
    <div className={styles.app}>
      <header className={styles.header}>
        <strong>Bowtie</strong>
        <div className={styles.headerRight}>
          <span className={styles.muted}>
            {user.username}
            {user.role === 'admin' ? ' · admin' : ''}
          </span>
          <button type="button" className={styles.linkBtn} onClick={() => void logout()}>
            Sign out
          </button>
        </div>
      </header>
      <main className={styles.main}>
        <p className={styles.muted}>Signed in. Guide and player arrive in Task 18.</p>
      </main>
    </div>
  )
}

export default function App() {
  return (
    <AuthProvider>
      <Shell />
    </AuthProvider>
  )
}
