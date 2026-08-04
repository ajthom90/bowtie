import { useCallback, useEffect, useMemo, useState } from 'react'
import { ApiError, type GuideChannel } from '../api/client'
import { useAuth } from '../auth/AuthContext'
import {
  defaultWindow,
  formatGuideTime,
  formatTimeRange,
  halfHourTicks,
  layoutRow,
  nowLinePct,
  shiftWindow,
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

  const load = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      const data = await client.getGuide(start, stop)
      setChannels(data)
    } catch (err) {
      if (err instanceof ApiError) {
        setError(err.message || 'Failed to load guide')
      } else {
        setError('Failed to load guide')
      }
      setChannels(null)
    } finally {
      setLoading(false)
    }
  }, [client, start, stop])

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

      {loading && !channels ? (
        <p className={styles.status}>Loading guide…</p>
      ) : null}

      {error ? (
        <div className={styles.status}>
          <p className={styles.statusError}>{error}</p>
          <button type="button" className={styles.btn} onClick={() => void load()}>
            Try again
          </button>
        </div>
      ) : null}

      {!loading && !error && channels && channels.length === 0 ? (
        <p className={styles.emptyGuide}>
          No guide data yet. Ask your admin to add an EPG source in Settings.
        </p>
      ) : null}

      {channels && channels.length > 0 ? (
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

            {channels.map((ch) => {
              const cells = layoutRow(ch.programs, start, stop)
              const hasPrograms = ch.programs.length > 0

              return (
                <ChannelRow
                  key={ch.channelId}
                  channel={ch}
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
}: {
  channel: GuideChannel
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
  const currentTitle = channel.programs.find((p) => {
    const a = Date.parse(p.start)
    const b = Date.parse(p.stop)
    const n = now.getTime()
    return n >= a && n < b
  })?.title

  return (
    <>
      <button
        type="button"
        className={styles.channelCell}
        onClick={() => watch(currentTitle)}
        aria-label={`Watch channel ${channel.guideNumber} ${channel.name}`}
      >
        <span className={styles.channelNum}>{channel.guideNumber}</span>
        {channel.logoUrl ? (
          <img className={styles.logo} src={channel.logoUrl} alt="" width={24} height={24} />
        ) : null}
        <span className={styles.channelMeta}>
          <span className={styles.callSign}>{channel.name}</span>
        </span>
      </button>

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
            aria-label={`Watch channel ${channel.guideNumber}, no guide data`}
          >
            No guide data
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
