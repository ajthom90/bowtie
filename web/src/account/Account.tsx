import { useRef, useState } from 'react'
import { ApiError, type FeedResponse } from '../api/client'
import { useAuth } from '../auth/AuthContext'
import { BowtieMark } from '../BowtieMark'
import { FEED_APPS, FEED_COPY, feedButtons } from './feedModel'
import styles from './Account.module.css'

type Props = {
  onGuide: () => void
  onRecordings: () => void
  onAdmin?: () => void
  /** Opens /link to approve a TV's quick sign-in. */
  onLink: () => void
}

function errorText(err: unknown, fallback: string): string {
  return err instanceof ApiError && err.message ? err.message : fallback
}

/** The signed-in user's page: TV sign-in and links for other apps. */
export function Account({ onGuide, onRecordings, onAdmin, onLink }: Props) {
  const { client, user, logout } = useAuth()
  const [feed, setFeed] = useState<FeedResponse | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [message, setMessage] = useState<string | null>(null)

  const buttons = feedButtons(feed !== null)

  const create = async (rotate: boolean) => {
    if (rotate && !window.confirm(FEED_COPY.rotateConfirm)) return
    setBusy(true)
    setError(null)
    setMessage(null)
    try {
      setFeed(await client.createFeed())
      setMessage(rotate ? 'New links made. The old ones no longer work.' : null)
    } catch (err) {
      setError(errorText(err, 'Could not make the links. Try again.'))
    } finally {
      setBusy(false)
    }
  }

  const turnOff = async () => {
    if (!window.confirm(FEED_COPY.offConfirm)) return
    setBusy(true)
    setError(null)
    setMessage(null)
    try {
      await client.deleteFeed()
      setFeed(null)
      setMessage('Links turned off.')
    } catch (err) {
      setError(errorText(err, 'Could not turn the links off. Try again.'))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className={styles.page}>
      <header className={styles.toolbar}>
        <div className={styles.toolbarLeft}>
          <span className={styles.brand}>
            <BowtieMark size={22} />
            Bowtie
          </span>
          <span className={styles.subtitle}>Account</span>
        </div>
        <div className={styles.toolbarRight}>
          <button type="button" className={styles.btn} onClick={onGuide}>
            Guide
          </button>
          <button type="button" className={styles.btn} onClick={onRecordings}>
            Recordings
          </button>
          {onAdmin ? (
            <button type="button" className={styles.btn} onClick={onAdmin}>
              Admin
            </button>
          ) : null}
          <button type="button" className={styles.btn} onClick={() => void logout()}>
            Sign out
          </button>
        </div>
      </header>

      <main className={styles.body}>
        <div className={styles.column}>
          <section className={styles.section} aria-labelledby="account-me">
            <h2 id="account-me" className={styles.sectionTitle}>
              {user?.username}
            </h2>
            <p className={styles.dim}>{user?.role === 'admin' ? 'Admin' : 'Viewer'}</p>
          </section>

          <section className={styles.section} aria-labelledby="account-tv">
            <h2 id="account-tv" className={styles.sectionTitle}>
              Sign in a TV
            </h2>
            <p className={styles.text}>
              In the Bowtie TV app, choose quick sign-in. Scan the QR code with your phone, or
              enter the code shown on the TV here.
            </p>
            <div className={styles.actions}>
              <button type="button" className={styles.btn} onClick={onLink}>
                Enter a TV code
              </button>
            </div>
          </section>

          <section className={styles.section} aria-labelledby="account-feed">
            <h2 id="account-feed" className={styles.sectionTitle}>
              Use Bowtie in other apps
            </h2>
            <p className={styles.text}>{FEED_COPY.lead}</p>
            <p className={styles.warning}>{FEED_COPY.warning}</p>

            {feed ? (
              <div className={styles.links}>
                <CopyField label="M3U link (channels)" value={feed.m3uUrl} />
                <CopyField label="Guide link (XMLTV)" value={feed.xmltvUrl} />
              </div>
            ) : (
              <p className={styles.dim}>{FEED_COPY.existing}</p>
            )}

            {error ? (
              <p className={styles.error} role="alert">
                {error}
              </p>
            ) : null}
            {message ? (
              <p className={styles.ok} role="status">
                {message}
              </p>
            ) : null}

            <div className={styles.actions}>
              {buttons.create ? (
                <button
                  type="button"
                  className={`${styles.btn} ${styles.btnPrimary}`}
                  disabled={busy}
                  onClick={() => void create(false)}
                >
                  {busy ? 'Working…' : buttons.create}
                </button>
              ) : null}
              {buttons.rotate ? (
                <button type="button" className={styles.btn} disabled={busy} onClick={() => void create(true)}>
                  New link
                </button>
              ) : null}
              <button
                type="button"
                className={`${styles.btn} ${styles.btnDanger}`}
                disabled={busy}
                onClick={() => void turnOff()}
              >
                Turn off
              </button>
            </div>

            <h3 className={styles.appsTitle}>Set up an app</h3>
            <div className={styles.apps}>
              {FEED_APPS.map((app) => (
                <details key={app.name} className={styles.app}>
                  <summary className={styles.appSummary}>
                    <span className={styles.appName}>{app.name}</span>
                    <span className={styles.appNote}>{app.note}</span>
                  </summary>
                  <ol className={styles.steps}>
                    {app.steps.map((step) => (
                      <li key={step}>{step}</li>
                    ))}
                  </ol>
                </details>
              ))}
            </div>
          </section>
        </div>
      </main>
    </div>
  )
}

/** A read-only link with a Copy button (falls back to selecting the text). */
function CopyField({ label, value }: { label: string; value: string }) {
  const inputRef = useRef<HTMLInputElement | null>(null)
  const [copied, setCopied] = useState(false)

  const copy = async () => {
    let ok = false
    try {
      if (navigator.clipboard) {
        await navigator.clipboard.writeText(value)
        ok = true
      }
    } catch {
      ok = false
    }
    if (!ok && inputRef.current) {
      inputRef.current.select()
      try {
        ok = document.execCommand('copy')
      } catch {
        ok = false
      }
    }
    if (ok) {
      setCopied(true)
      window.setTimeout(() => setCopied(false), 2000)
    } else {
      inputRef.current?.select()
    }
  }

  return (
    <label className={styles.copyField}>
      <span className={styles.copyLabel}>{label}</span>
      <span className={styles.copyRow}>
        <input
          ref={inputRef}
          className={styles.copyInput}
          value={value}
          readOnly
          onFocus={(e) => e.target.select()}
          spellCheck={false}
        />
        <button type="button" className={styles.btn} onClick={() => void copy()} aria-label={`Copy ${label}`}>
          {copied ? 'Copied ✓' : 'Copy'}
        </button>
      </span>
    </label>
  )
}
