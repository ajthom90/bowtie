import { useState, type FormEvent } from 'react'
import { ApiError } from '../api/client'
import { useAuth } from './AuthContext'
import { BowtieMark } from '../BowtieMark'
import styles from './Login.module.css'

type Props = {
  /** Why they're signing in (default: watching live TV). */
  subtitle?: string
}

export function Login({ subtitle = 'Sign in to watch live TV' }: Props = {}) {
  const { login } = useAuth()
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  async function onSubmit(e: FormEvent) {
    e.preventDefault()
    setError(null)
    setBusy(true)
    try {
      await login(username.trim(), password)
    } catch (err) {
      if (err instanceof ApiError) {
        setError(err.message || 'Login failed')
      } else {
        setError('Login failed')
      }
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className={styles.page}>
      <form className={styles.card} onSubmit={onSubmit}>
        <h1 className={styles.title}>
          <BowtieMark size={34} />
          Bowtie
        </h1>
        <p className={styles.subtitle}>{subtitle}</p>
        <label className={styles.label}>
          Username
          <input
            className={styles.input}
            name="username"
            autoComplete="username"
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            disabled={busy}
            required
          />
        </label>
        <label className={styles.label}>
          Password
          <input
            className={styles.input}
            name="password"
            type="password"
            autoComplete="current-password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            disabled={busy}
            required
          />
        </label>
        {error ? <p className={styles.error}>{error}</p> : null}
        <button className={styles.button} type="submit" disabled={busy}>
          {busy ? 'Signing in…' : 'Sign in'}
        </button>
      </form>
    </div>
  )
}
