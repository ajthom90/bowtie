import { useCallback, useEffect, useMemo, useState } from 'react'
import { ApiError, type GuideChannel, type RecentChannel } from '../api/client'
import { useAuth } from '../auth/AuthContext'
import { guideMarkText, guideRecLabel } from '../recordings/recordingsModel'
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
  type GuideProgram,
} from './guideModel'
import {
  GUIDE_FILTERS,
  channelMatchesFilter,
  filterEmptyCopy,
  filterLabel,
  loadGuideFilter,
  programMatchesFilter,
  saveGuideFilter,
  type GuideFilter,
} from './guideFilterModel'
import { GuideSearch } from './GuideSearch'
import { ProgramSheet, type SheetChannel, type SheetConflict } from './ProgramSheet'
import { lockText } from './searchModel'
import { BowtieMark } from '../BowtieMark'
import styles from './Guide.module.css'

export type WatchTarget = {
  channelId: number
  guideNumber: string
  name: string
  programTitle?: string
}

type Props = {
  onWatch: (target: WatchTarget) => void
  /** Opens Multiview (several live channels at once). */
  onMultiview?: () => void
  /** Present only for admins — opens the admin area. Viewers never receive this. */
  onAdmin?: () => void
  /** Opens the Recordings page. */
  onRecordings?: () => void
  /** Opens the Account page (from the username in the header). */
  onAccount?: () => void
}

type Selected = {
  channel: SheetChannel
  program: GuideProgram
  initialView?: 'details' | 'series'
  initialConflict?: SheetConflict
}

export function Guide({ onWatch, onMultiview, onAdmin, onRecordings, onAccount }: Props) {
  const { client, user, logout } = useAuth()
  const [{ start, stop }, setWindow] = useState(() => defaultWindow())
  const [channels, setChannels] = useState<GuideChannel[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)
  const [now, setNow] = useState(() => new Date())
  const [recents, setRecents] = useState<RecentChannel[]>([])
  /** Failed star / clear: shown above the grid without replacing it. */
  const [actionError, setActionError] = useState<string | null>(null)
  const [selected, setSelected] = useState<Selected | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  /** Bumped after a recording change so open search results refresh. */
  const [searchEpoch, setSearchEpoch] = useState(0)
  /** Category chip; remembered per browser. */
  const [filter, setFilter] = useState<GuideFilter>(() => loadGuideFilter())

  function chooseFilter(next: GuideFilter) {
    setFilter(next)
    saveGuideFilter(next)
  }

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
  /** Rows with something matching the chip in the visible window. */
  const visibleRows = useMemo(
    () => rows?.filter((ch) => channelMatchesFilter(ch, filter, start, stop)) ?? null,
    [rows, filter, start, stop],
  )

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
          <span className={styles.brand}>
            <BowtieMark size={22} />
            Bowtie
          </span>
          <span className={styles.windowLabel}>{windowLabel}</span>
        </div>
        <GuideSearch
          onWatch={onWatch}
          onOpenSheet={(req) => setSelected(req)}
          onNotice={(text) => setNotice(text)}
          onChanged={() => void load()}
          refreshKey={searchEpoch}
          suspended={selected !== null}
        />
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
          {onAccount ? (
            <button
              type="button"
              className={`${styles.btn} ${styles.userBtn}`}
              onClick={onAccount}
              aria-label={`Account (${user?.username ?? ''})`}
              title="Account"
            >
              {user?.username}
              {user?.role === 'admin' ? <span className={styles.userRole}> · admin</span> : null}
            </button>
          ) : (
            <span className={styles.userMeta}>
              {user?.username}
              {user?.role === 'admin' ? ' · admin' : ''}
            </span>
          )}
          {onMultiview ? (
            <button type="button" className={styles.btn} onClick={onMultiview}>
              Multiview
            </button>
          ) : null}
          {onRecordings ? (
            <button type="button" className={styles.btn} onClick={onRecordings}>
              Recordings
            </button>
          ) : null}
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

      {notice ? (
        <div className={styles.notice} role="status">
          <span>{notice}</span>
          <button type="button" className={styles.btn} onClick={() => setNotice(null)}>
            Dismiss
          </button>
        </div>
      ) : null}

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

      {pageState.kind === 'ready' ? (
        <div className={styles.filters} role="group" aria-label="Show programs">
          {GUIDE_FILTERS.map((f) => (
            <button
              key={f}
              type="button"
              className={`${styles.filterChip}${filter === f ? ` ${styles.filterChipOn}` : ''}`}
              aria-pressed={filter === f}
              onClick={() => chooseFilter(f)}
            >
              {filterLabel(f)}
            </button>
          ))}
        </div>
      ) : null}

      {pageState.kind === 'ready' && filter !== 'all' && visibleRows?.length === 0 ? (
        <div className={styles.filterEmpty} role="status">
          <p className={styles.filterEmptyText}>{filterEmptyCopy(filter)}</p>
          <div className={styles.filterEmptyActions}>
            <button type="button" className={styles.btn} onClick={() => page(1)}>
              Later
            </button>
            <button type="button" className={styles.btn} onClick={() => chooseFilter('all')}>
              Show all
            </button>
          </div>
        </div>
      ) : null}

      {pageState.kind === 'ready' && visibleRows && visibleRows.length > 0 ? (
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

            {visibleRows.map((ch) => {
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
                  filter={filter}
                  onWatch={onWatch}
                  onSelect={(program) =>
                    setSelected({
                      channel: { channelId: ch.channelId, guideNumber: ch.guideNumber, name: ch.name },
                      program,
                    })
                  }
                />
              )
            })}
          </div>
        </div>
      ) : null}

      {selected ? (
        <ProgramSheet
          channel={selected.channel}
          program={selected.program}
          initialView={selected.initialView}
          initialConflict={selected.initialConflict}
          now={new Date()}
          onClose={() => setSelected(null)}
          onWatch={() => {
            const { channel, program } = selected
            setSelected(null)
            onWatch({
              channelId: channel.channelId,
              guideNumber: channel.guideNumber,
              name: channel.name,
              programTitle: program.title,
            })
          }}
          onChanged={(warning) => {
            setSelected(null)
            setNotice(warning ?? null)
            setSearchEpoch((n) => n + 1)
            void load()
          }}
          onRecordings={onRecordings}
        />
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
  filter,
  onWatch,
  onToggleFavorite,
  onSelect,
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
  /** Programs outside the chosen category are dimmed. */
  filter: GuideFilter
  onWatch: (t: WatchTarget) => void
  onSelect: (program: GuideProgram) => void
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
              const mark = cell.program.recording
              const rec = guideRecLabel(mark)
              const recWords = mark ? guideMarkText(mark.state) : ''
              const dimmed = !programMatchesFilter(cell.program, filter)
              return (
                <button
                  key={`prog-${i}-${cell.program.start}`}
                  type="button"
                  className={`${styles.cell}${onAir ? ` ${styles.cellOnAir}` : ''}${dimmed ? ` ${styles.cellDimmed}` : ''}`}
                  style={{ left: `${cell.leftPct}%`, width: `${cell.widthPct}%` }}
                  onClick={() => onSelect(cell.program)}
                  aria-haspopup="dialog"
                  aria-label={`${cell.program.title}, channel ${channel.guideNumber}${cell.program.locked ? ', blocked by parental controls' : ''}${recWords ? `, ${recWords.toLowerCase()}` : ''}`}
                >
                  <span className={styles.cellTitle}>
                    {rec ? (
                      <span
                        className={`${styles.recMark}${mark?.state === 'recording' ? ` ${styles.recLive}` : ''}`}
                        aria-hidden
                      >
                        {rec}
                      </span>
                    ) : null}
                    {cell.program.title}
                  </span>
                  <span className={styles.cellTime}>
                    {formatTimeRange(new Date(cell.program.start), new Date(cell.program.stop))}
                    {cell.program.locked ? (
                      <span className={styles.lockMark}> {lockText(cell.program.rating)}</span>
                    ) : null}
                  </span>
                  {cell.program.description && !cell.program.locked ? (
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
