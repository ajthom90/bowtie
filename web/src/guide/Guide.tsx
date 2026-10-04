import { useCallback, useEffect, useMemo, useState } from 'react'
import { ApiError, type GuideChannel, type RecentChannel } from '../api/client'
import { useAuth } from '../auth/AuthContext'
import {
  GUIDE_COPY,
  currentProgramTitle,
  defaultWindow,
  formatGuideTime,
  formatTimeRange,
  halfHourTicks,
  layoutRow,
  nowLinePct,
  receptionNote,
  selectGuidePageState,
  shiftWindow,
  sortFavoritesFirst,
  supportsFavorites,
  withFavorite,
} from './guideModel'
import styles from './Guide.module.css'

export type WatchTarget = {
  channelId: number
  guideNumber: string
  name: string
  programTitle?: string
}

type Props = {
  onWatch: (target: WatchTarget) => void
  /** Present only for admins — opens the admin area. Viewers never receive this. */
  onAdmin?: () => void
}

export function Guide({ onWatch, onAdmin }: Props) {
  const { client, user, logout } = useAuth()
  const [{ start, stop }, setWindow] = useState(() => defaultWindow())
  const [channels, setChannels] = useState<GuideChannel[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)
  const [now, setNow] = useState(() => new Date())
  const [recents, setRecents] = useState<RecentChannel[]>([])
  /** Failed star / clear: shown above the grid without replacing it. */
  const [actionError, setActionError] = useState<string | null>(null)

  const load = useCallback(async () => {
    setLoading(true)
    setError(null)
    setActionError(null)
    try {
      const data = await client.getGuide(start, stop)
      setChannels(data)
      // Recents ride along with every guide load. Older servers (no
      // `favorite`, or 404 on the GET) simply get no Recent row.
      if (supportsFavorites(data)) {
        client.getRecents(8).then(setRecents, () => setRecents([]))
      } else {
        setRecents([])
      }
    } catch (err) {
      if (err instanceof ApiError) {
        setError(err.message || 'Failed to load guide')
      } else {
        setError('Failed to load guide')
      }
      setChannels(null)
      setRecents([])
    } finally {
      setLoading(false)
    }
  }, [client, start, stop])

  const favoritesOn = useMemo(() => (channels ? supportsFavorites(channels) : false), [channels])
  const rows = useMemo(() => (channels ? sortFavoritesFirst(channels) : null), [channels])

  async function toggleFavorite(channel: GuideChannel) {
    const id = channel.channelId
    const on = channel.favorite !== true
    setActionError(null)
    setChannels((cs) => cs && withFavorite(cs, id, on))
    try {
      await (on ? client.addFavorite(id) : client.removeFavorite(id))
    } catch (err) {
      setChannels((cs) => cs && withFavorite(cs, id, !on))
      setActionError(
        err instanceof ApiError && err.message ? err.message : 'Could not update favorite',
      )
    }
  }

  async function clearRecents() {
    const prev = recents
    setActionError(null)
    setRecents([])
    try {
      await client.clearRecents()
    } catch (err) {
      setRecents(prev)
      setActionError(
        err instanceof ApiError && err.message ? err.message : 'Could not clear recents',
      )
    }
  }

  function watchRecent(r: RecentChannel) {
    const ch = channels?.find((c) => c.channelId === r.channelId)
    onWatch({
      channelId: r.channelId,
      guideNumber: r.guideNumber,
      name: r.name,
      programTitle: ch ? currentProgramTitle(ch.programs, now) : undefined,
    })
  }

  useEffect(() => {
    void load()
  }, [load])

  // Tick "now" every 30s for the NOW line.
  useEffect(() => {
    const id = window.setInterval(() => setNow(new Date()), 30_000)
    return () => window.clearInterval(id)
  }, [])

  const ticks = useMemo(() => halfHourTicks(start, stop), [start, stop])
  const nowPct = useMemo(() => nowLinePct(now, start, stop), [now, start, stop])

  const pageState = useMemo(
    () =>
      selectGuidePageState({
        channels,
        loading,
        error,
        role: user?.role === 'admin' ? 'admin' : 'viewer',
      }),
    [channels, loading, error, user?.role],
  )

  const windowLabel = `${formatGuideTime(start)} – ${formatGuideTime(stop)}`

  function page(dir: -1 | 1) {
    setWindow((w) => shiftWindow(w.start, w.stop, dir))
  }

  return (
    <div className={styles.page}>
      <header className={styles.toolbar}>
        <div className={styles.toolbarLeft}>
          <span className={styles.brand}>Bowtie</span>
          <span className={styles.windowLabel}>{windowLabel}</span>
        </div>
        <div className={styles.toolbarRight}>
          <button type="button" className={styles.btn} onClick={() => page(-1)} aria-label="Previous time window">
            Prev
          </button>
          <button
            type="button"
            className={styles.btn}
            onClick={() => setWindow(defaultWindow(new Date()))}
            aria-label="Jump to now"
          >
            Now
          </button>
          <button type="button" className={styles.btn} onClick={() => page(1)} aria-label="Next time window">
            Next
          </button>
          <span className={styles.userMeta}>
            {user?.username}
            {user?.role === 'admin' ? ' · admin' : ''}
          </span>
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

      {pageState.kind === 'loading' ? (
        <p className={styles.status}>Loading guide…</p>
      ) : null}

      {pageState.kind === 'error' ? (
        <div className={styles.status}>
          <p className={styles.statusError}>{pageState.message}</p>
          <button type="button" className={styles.btn} onClick={() => void load()}>
            Try again
          </button>
        </div>
      ) : null}

      {pageState.kind === 'empty' ? (
        <div className={styles.emptyGuide}>
          <p style={{ margin: 0 }}>{pageState.copy}</p>
          {pageState.showAdminLink && onAdmin ? (
            <p style={{ margin: '0.75rem 0 0' }}>
              <button type="button" className={styles.btn} onClick={onAdmin}>
                Open Admin → Channels
              </button>
            </p>
          ) : null}
        </div>
      ) : null}

      {actionError && pageState.kind === 'ready' ? (
        <p className={styles.actionError} role="alert">
          {actionError}
        </p>
      ) : null}

      {pageState.kind === 'ready' && recents.length > 0 ? (
        <nav className={styles.recents} aria-label="Recently watched">
          <span className={styles.recentsLabel}>Recent</span>
          <div className={styles.recentChips}>
            {recents.map((r) => (
              <button
                key={r.channelId}
                type="button"
                className={styles.chip}
                onClick={() => watchRecent(r)}
                aria-label={`Watch channel ${r.guideNumber} ${r.name}`}
              >
                <span className={styles.chipNum}>{r.guideNumber}</span>
                <span className={styles.chipName}>{r.name}</span>
              </button>
            ))}
          </div>
          <button
            type="button"
            className={styles.chipClear}
            onClick={() => void clearRecents()}
            aria-label="Clear recently watched"
          >
            Clear
          </button>
        </nav>
      ) : null}

      {pageState.kind === 'ready' && rows ? (
        <div className={styles.scroll} tabIndex={0} role="region" aria-label="TV guide">
          <div className={styles.grid}>
            <div className={styles.corner} aria-hidden />
            <div className={styles.timeAxis}>
              <div className={styles.timeAxisInner}>
                {ticks.map((t) => (
                  <span
                    key={t.toISOString()}
                    className={styles.tick}
                    style={{ left: `${nowLinePct(t, start, stop) ?? 0}%` }}
                  >
                    {formatGuideTime(t)}
                  </span>
                ))}
                {nowPct !== null ? (
                  <span className={styles.nowTag} style={{ left: `${nowPct}%` }}>
                    NOW
                  </span>
                ) : null}
              </div>
            </div>

            {rows.map((ch) => {
              const cells = layoutRow(ch.programs, start, stop)
              const hasPrograms = ch.programs.length > 0

              return (
                <ChannelRow
                  key={ch.channelId}
                  channel={ch}
                  onToggleFavorite={favoritesOn ? () => void toggleFavorite(ch) : undefined}
                  cells={cells}
                  hasPrograms={hasPrograms}
                  nowPct={nowPct}
                  ticks={ticks}
                  windowStart={start}
                  windowStop={stop}
                  now={now}
                  onWatch={onWatch}
                />
              )
            })}
          </div>
        </div>
      ) : null}
    </div>
  )
}

function ChannelRow({
  channel,
  cells,
  hasPrograms,
  nowPct,
  ticks,
  windowStart,
  windowStop,
  now,
  onWatch,
  onToggleFavorite,
}: {
  channel: GuideChannel
  /** Absent when the server predates favorites (no star shown). */
  onToggleFavorite?: () => void
  cells: ReturnType<typeof layoutRow>
  hasPrograms: boolean
  nowPct: number | null
  ticks: Date[]
  windowStart: Date
  windowStop: Date
  now: Date
  onWatch: (t: WatchTarget) => void
}) {
  const watch = (programTitle?: string) => {
    onWatch({
      channelId: channel.channelId,
      guideNumber: channel.guideNumber,
      name: channel.name,
      programTitle,
    })
  }

  // Find currently airing title for channel-column click.
  const currentTitle = currentProgramTitle(channel.programs, now)
  const rxNote = receptionNote(channel.reception)
  const fav = channel.favorite === true

  return (
    <>
      <div className={styles.channelCell}>
        <button
          type="button"
          className={`${styles.channelWatch} ${rxNote ? styles.noSignal : ''}`}
          onClick={() => watch(currentTitle)}
          aria-label={`Watch channel ${channel.guideNumber} ${channel.name}${rxNote ? `, ${rxNote.toLowerCase()} last time` : ''}`}
        >
          <span className={styles.channelNum}>{channel.guideNumber}</span>
          {channel.logoUrl ? (
            <img className={styles.logo} src={channel.logoUrl} alt="" width={24} height={24} />
          ) : null}
          <span className={styles.channelMeta}>
            <span className={styles.callSign}>{channel.name}</span>
            {rxNote ? <span className={styles.rxBadge}>{rxNote}</span> : null}
          </span>
        </button>
        {onToggleFavorite ? (
          <button
            type="button"
            className={`${styles.star}${fav ? ` ${styles.starOn}` : ''}`}
            aria-pressed={fav}
            aria-label={`Favorite ${channel.name}`}
            onClick={(e) => {
              e.stopPropagation()
              onToggleFavorite()
            }}
          >
            <span aria-hidden>{fav ? '★' : '☆'}</span>
          </button>
        ) : null}
      </div>

      <div className={styles.rowPrograms}>
        <div className={styles.gridlines} aria-hidden>
          {ticks.map((t) => {
            const pct = nowLinePct(t, windowStart, windowStop)
            if (pct === null) return null
            return <span key={t.toISOString()} className={styles.gridline} style={{ left: `${pct}%` }} />
          })}
        </div>
        {nowPct !== null ? (
          <div className={styles.nowLine} style={{ left: `${nowPct}%` }} aria-hidden />
        ) : null}

        {!hasPrograms ? (
          <button
            type="button"
            className={styles.cellEmpty}
            onClick={() => watch()}
            aria-label={`Watch channel ${channel.guideNumber}, no guide data — press to watch`}
          >
            {GUIDE_COPY.noGuideData}
          </button>
        ) : (
          <div className={styles.cells}>
            {cells.map((cell, i) => {
              if (cell.kind === 'gap') {
                return (
                  <button
                    key={`gap-${i}`}
                    type="button"
                    className={`${styles.cell} ${styles.cellGap}`}
                    style={{ left: `${cell.leftPct}%`, width: `${cell.widthPct}%` }}
                    onClick={() => watch(currentTitle)}
                    aria-label={`Watch channel ${channel.guideNumber}`}
                  />
                )
              }
              const onAir =
                now.getTime() >= cell.start.getTime() && now.getTime() < cell.stop.getTime()
              return (
                <button
                  key={`prog-${i}-${cell.program.start}`}
                  type="button"
                  className={`${styles.cell}${onAir ? ` ${styles.cellOnAir}` : ''}`}
                  style={{ left: `${cell.leftPct}%`, width: `${cell.widthPct}%` }}
                  onClick={() => watch(cell.program.title)}
                  aria-label={`${cell.program.title}, channel ${channel.guideNumber}`}
                >
                  <span className={styles.cellTitle}>{cell.program.title}</span>
                  <span className={styles.cellTime}>
                    {formatTimeRange(new Date(cell.program.start), new Date(cell.program.stop))}
                  </span>
                  {cell.program.description ? (
                    <span className={styles.cellDesc}>{cell.program.description}</span>
                  ) : null}
                </button>
              )
            })}
          </div>
        )}
      </div>
    </>
  )
}
