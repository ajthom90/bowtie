import { useEffect, useRef, useState, type FormEvent } from 'react'
import { useAuth } from './AuthContext'
import { loginErrorText } from './authErrors'
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
  const passwordRef = useRef<HTMLInputElement | null>(null)

  // After a failed sign-in, keep the cursor in the password field (it was
  // disabled while busy, which drops focus).
  useEffect(() => {
    if (error && !busy) passwordRef.current?.focus()
  }, [error, busy])

  async function onSubmit(e: FormEvent) {
    e.preventDefault()
    setError(null)
    setBusy(true)
    try {
      await login(username.trim(), password)
    } catch (err) {
      setError(loginErrorText(err))
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
            ref={passwordRef}
            className={styles.input}
            name="password"
            type="password"
            autoComplete="current-password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            disabled={busy}
            required
            aria-invalid={error ? true : undefined}
            aria-describedby={error ? 'login-error' : undefined}
          />
        </label>
        {error ? (
          <p id="login-error" className={styles.error} role="alert">
            {error}
          </p>
        ) : null}
        <button className={styles.button} type="submit" disabled={busy}>
          {busy ? 'Signing in…' : 'Sign in'}
        </button>
      </form>
    </div>
  )
}
