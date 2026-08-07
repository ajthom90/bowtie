import { useState } from 'react'
import type { WatchTarget } from '../guide/Guide'
import { useAuth } from '../auth/AuthContext'
import { Channels } from './Channels'
import { Epg } from './Epg'
import { Sessions } from './Sessions'
import { Settings } from './Settings'
import { Tuners } from './Tuners'
import { Users } from './Users'
import styles from './Admin.module.css'

export type AdminTab = 'tuners' | 'channels' | 'epg' | 'settings' | 'users' | 'sessions'

const TABS: { id: AdminTab; label: string }[] = [
  { id: 'tuners', label: 'Tuners' },
  { id: 'channels', label: 'Channels' },
  { id: 'epg', label: 'EPG' },
  { id: 'settings', label: 'Settings' },
  { id: 'users', label: 'Users' },
  { id: 'sessions', label: 'Sessions' },
]

type Props = {
  onBack: () => void
  /** A5: open the player for a channel (admin preview from Channels). */
  onPreview: (target: WatchTarget) => void
}

export function Admin({ onBack, onPreview }: Props) {
  const { user, logout } = useAuth()
  const [tab, setTab] = useState<AdminTab>('tuners')

  // Defense in depth — App already gates on role.
  if (user?.role !== 'admin') {
    return (
      <div className={styles.page}>
        <div className={styles.body}>
          <p className={styles.statusError}>Admin access required.</p>
          <button type="button" className={styles.btn} onClick={onBack}>
            Back to guide
          </button>
        </div>
      </div>
    )
  }

  return (
    <div className={styles.page}>
      <header className={styles.toolbar}>
        <div className={styles.toolbarLeft}>
          <span className={styles.brand}>Bowtie</span>
          <span className={styles.subtitle}>Admin</span>
        </div>
        <div className={styles.toolbarRight}>
          <button type="button" className={styles.btn} onClick={onBack}>
            Guide
          </button>
          <span className={styles.subtitle}>{user.username}</span>
          <button type="button" className={styles.btn} onClick={() => void logout()}>
            Sign out
          </button>
        </div>
      </header>

      <nav className={styles.nav} aria-label="Admin sections">
        {TABS.map((t) => (
          <button
            key={t.id}
            type="button"
            className={`${styles.navBtn}${tab === t.id ? ` ${styles.navBtnActive}` : ''}`}
            aria-current={tab === t.id ? 'page' : undefined}
            onClick={() => setTab(t.id)}
          >
            {t.label}
          </button>
        ))}
      </nav>

      <main className={styles.body}>
        {tab === 'tuners' ? <Tuners /> : null}
        {tab === 'channels' ? <Channels onPreview={onPreview} /> : null}
        {tab === 'epg' ? <Epg /> : null}
        {tab === 'settings' ? <Settings /> : null}
        {tab === 'users' ? <Users /> : null}
        {tab === 'sessions' ? <Sessions /> : null}
      </main>
    </div>
  )
}
