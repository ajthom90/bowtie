import { useCallback, useEffect, useRef, useState, type FormEvent } from 'react'
import { ApiError } from '../api/client'
import { BowtieMark } from '../BowtieMark'
import { useAuth } from './AuthContext'
import { linkErrorText as errorText } from './authErrors'
import {
  codeFromSearch,
  deviceLabel,
  formatCodeInput,
  isCompleteCode,
  normalizeCode,
} from './linkModel'
import styles from './LinkPage.module.css'

type Step =
  | { kind: 'enter' }
  | { kind: 'checking' }
  | { kind: 'confirm'; deviceName: string }
  | { kind: 'approving'; deviceName: string }
  | { kind: 'done'; deviceName: string }
  | { kind: 'cancelled' }

type Props = {
  /** Leave /link for the guide. */
  onDone: () => void
}

/** /link?code=…: approve a TV's quick sign-in from a phone or browser. */
export function LinkPage({ onDone }: Props) {
  const { client, user, logout } = useAuth()
  const [code, setCode] = useState(() => codeFromSearch(window.location.search))
  const [input, setInput] = useState(() => formatCodeInput(code))
  const [step, setStep] = useState<Step>(() => (isCompleteCode(code) ? { kind: 'checking' } : { kind: 'enter' }))
  const [error, setError] = useState<string | null>(null)
  const inputRef = useRef<HTMLInputElement | null>(null)

  const lookup = useCallback(
    async (c: string) => {
      setError(null)
      setStep({ kind: 'checking' })
      try {
        const d = await client.lookupDevice(c)
        setStep({ kind: 'confirm', deviceName: d.deviceName })
      } catch (err) {
        setError(errorText(err, 'Could not check that code. Try again.'))
        setStep({ kind: 'enter' })
      }
    },
    [client],
  )

  // A code in the URL (from the TV's QR code) is checked right away.
  const started = useRef(false)
  useEffect(() => {
    if (started.current) return
    started.current = true
    if (isCompleteCode(code)) void lookup(code)
  }, [code, lookup])

  useEffect(() => {
    if (step.kind === 'enter') inputRef.current?.focus()
  }, [step.kind])

  const onSubmitCode = (e: FormEvent) => {
    e.preventDefault()
    const c = normalizeCode(input)
    if (!isCompleteCode(c)) {
      setError('Enter the 8-character code shown on your TV.')
      return
    }
    setCode(c)
    // Keep the URL in step with the code being approved.
    window.history.replaceState(null, '', `/link?code=${c}`)
    void lookup(c)
  }

  const approve = async (deviceName: string) => {
    setError(null)
    setStep({ kind: 'approving', deviceName })
    try {
      await client.approveDevice(code)
      setStep({ kind: 'done', deviceName })
    } catch (err) {
      setError(errorText(err, 'Could not sign in your TV. Try again.'))
      setStep(err instanceof ApiError && err.status === 404 ? { kind: 'enter' } : { kind: 'confirm', deviceName })
    }
  }

  const enterAnother = () => {
    setError(null)
    setCode('')
    setInput('')
    window.history.replaceState(null, '', '/link')
    setStep({ kind: 'enter' })
  }

  return (
    <div className={styles.page}>
      <main className={styles.card}>
        <p className={styles.brand}>
          <BowtieMark size={26} />
          Bowtie
        </p>

        {step.kind === 'enter' ? (
          <form className={styles.form} onSubmit={onSubmitCode}>
            <h1 className={styles.heading}>Sign in a TV</h1>
            <p className={styles.lead}>Enter the code shown on your TV.</p>
            <label className={styles.codeLabel}>
              <span className="visually-hidden">Code</span>
              <input
                ref={inputRef}
                className={styles.codeInput}
                value={input}
                onChange={(e) => {
                  setInput(formatCodeInput(e.target.value))
                  setError(null)
                }}
                placeholder="XXXX-XXXX"
                autoComplete="one-time-code"
                autoCapitalize="characters"
                autoCorrect="off"
                spellCheck={false}
                inputMode="text"
                aria-invalid={error ? true : undefined}
                aria-describedby={error ? 'link-error' : undefined}
              />
            </label>
            {error ? (
              <p id="link-error" className={styles.error} role="alert">
                {error}
              </p>
            ) : null}
            <button type="submit" className={styles.primary} disabled={!isCompleteCode(input)}>
              Continue
            </button>
          </form>
        ) : null}

        {step.kind === 'checking' ? (
          <p className={styles.lead} role="status">
            Checking code…
          </p>
        ) : null}

        {step.kind === 'confirm' || step.kind === 'approving' ? (
          <div className={styles.form}>
            <p className={styles.codeEcho}>{formatCodeInput(code)}</p>
            <h1 className={styles.heading}>
              Sign in <strong className={styles.device}>{deviceLabel(step.deviceName)}</strong> as{' '}
              {user?.username}?
            </h1>
            <p className={styles.lead}>
              The TV will be signed in to your account and can watch what you can.
            </p>
            {error ? (
              <p className={styles.error} role="alert">
                {error}
              </p>
            ) : null}
            <button
              type="button"
              className={styles.primary}
              disabled={step.kind === 'approving'}
              onClick={() => void approve(step.deviceName)}
            >
              {step.kind === 'approving' ? 'Approving…' : 'Approve'}
            </button>
            <button
              type="button"
              className={styles.secondary}
              disabled={step.kind === 'approving'}
              onClick={() => {
                setError(null)
                setStep({ kind: 'cancelled' })
              }}
            >
              Cancel
            </button>
            <p className={styles.switch}>
              Not {user?.username}?{' '}
              <button type="button" className={styles.link} onClick={() => void logout()}>
                Sign in as someone else
              </button>
            </p>
          </div>
        ) : null}

        {step.kind === 'done' ? (
          <div className={styles.form}>
            <p className={styles.check} aria-hidden>
              ✓
            </p>
            <h1 className={styles.heading} role="status">
              Done — your TV is signed in.
            </h1>
            <p className={styles.lead}>You can put your phone away and watch on {deviceLabel(step.deviceName)}.</p>
            <button type="button" className={styles.secondary} onClick={onDone}>
              Go to the guide
            </button>
          </div>
        ) : null}

        {step.kind === 'cancelled' ? (
          <div className={styles.form}>
            <h1 className={styles.heading}>Cancelled</h1>
            <p className={styles.lead}>Your TV wasn't signed in.</p>
            <button type="button" className={styles.secondary} onClick={enterAnother}>
              Enter a code
            </button>
            <button type="button" className={styles.secondary} onClick={onDone}>
              Go to the guide
            </button>
          </div>
        ) : null}
      </main>
    </div>
  )
}
