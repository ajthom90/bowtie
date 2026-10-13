import { useCallback, useEffect, useMemo, useRef, useState, type CSSProperties } from 'react'
import { type GuideChannel } from '../api/client'
import { viewerErrorText } from '../api/errorText'
import { useAuth } from '../auth/AuthContext'
import { BowtieMark } from '../BowtieMark'
import { currentProgramTitle } from '../guide/guideModel'
import {
  EMPTY_MULTIVIEW,
  MAX_TILES,
  addTile,
  layoutFor,
  loadSavedChannels,
  removeTile,
  replaceTile,
  restoreTiles,
  saveChannels,
  setAudio,
  tileIndexForKey,
  type MultiviewState,
  type TileChannel,
} from './multiviewModel'
import { createRecheckController, withWatchable } from '../guide/watchableModel'
import { MultiviewPicker } from './MultiviewPicker'
import { MultiviewTile } from './MultiviewTile'
import styles from './Multiview.module.css'

type Props = {
  onGuide: () => void
}

/** Guide data for the picker and program titles: now to three hours on. */
const GUIDE_SPAN_MS = 3 * 60 * 60 * 1000

type Picking = { mode: 'add' } | { mode: 'replace'; key: string }

/** Up to four live channels at once; one plays sound. */
export function Multiview({ onGuide }: Props) {
  const { client } = useAuth()
  const [mv, setMv] = useState<MultiviewState>(EMPTY_MULTIVIEW)
  const [picking, setPicking] = useState<Picking | null>(null)
  const [channels, setChannels] = useState<GuideChannel[] | null>(null)
  const [channelsError, setChannelsError] = useState<string | null>(null)
  const [saved, setSaved] = useState<TileChannel[]>(() => loadSavedChannels())
  const [now, setNow] = useState(() => new Date())
  const keySeq = useRef(0)
  const nextKey = useCallback(() => `t${++keySeq.current}`, [])

  useEffect(() => {
    let cancelled = false
    const start = new Date()
    client
      .getGuide(start, new Date(start.getTime() + GUIDE_SPAN_MS))
      .catch(() =>
        // No guide: the plain channel list still fills the picker.
        client.getChannels().then((list) =>
          list.map(
            (c): GuideChannel => ({
              channelId: c.id,
              guideNumber: c.guideNumber,
              name: c.name,
              logoUrl: c.logoUrl,
              reception: c.reception,
              favorite: c.favorite,
              watchable: c.watchable,
              programs: [],
            }),
          ),
        ),
      )
      .then(
        (list) => {
          if (!cancelled) setChannels(list)
        },
        (err: unknown) => {
          if (cancelled) return
          setChannelsError(
            viewerErrorText(err, 'Could not load channels.'),
          )
        },
      )
    return () => {
      cancelled = true
    }
  }, [client])

  useEffect(() => {
    const id = window.setInterval(() => setNow(new Date()), 30_000)
    return () => window.clearInterval(id)
  }, [])

  // While the picker is open, ask which channels a tuner can take now, then
  // every 30 s (and on return to the tab), so busy channels come back.
  const pickerOpen = picking !== null
  useEffect(() => {
    if (!pickerOpen) return
    let cancelled = false
    const check = () => {
      client.getChannels().then(
        (list) => {
          if (!cancelled) setChannels((cs) => cs && withWatchable(cs, list))
        },
        () => {},
      )
    }
    const ctrl = createRecheckController({ check })
    check()
    ctrl.start()
    const onVis = () => ctrl.handleVisibilityChange(document.visibilityState)
    document.addEventListener('visibilitychange', onVis)
    return () => {
      cancelled = true
      document.removeEventListener('visibilitychange', onVis)
      ctrl.stop()
    }
  }, [client, pickerOpen])

  // Remember the set for "Restore last"; an emptied page keeps the previous set.
  useEffect(() => {
    if (mv.tiles.length === 0) return
    const list = mv.tiles.map((t) => t.channel)
    saveChannels(list)
    setSaved(list)
  }, [mv.tiles])

  useEffect(() => {
    if (picking) return
    const onKey = (e: KeyboardEvent) => {
      if (e.altKey || e.ctrlKey || e.metaKey || e.repeat) return
      const el = e.target as HTMLElement | null
      if (el && (el.tagName === 'INPUT' || el.tagName === 'SELECT' || el.tagName === 'TEXTAREA')) {
        return
      }
      const i = tileIndexForKey(e.key, mv.tiles.length)
      if (i === null) return
      e.preventDefault()
      setMv((s) => (s.tiles[i] ? setAudio(s, s.tiles[i].key) : s))
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [picking, mv.tiles.length])

  const titles = useMemo(() => {
    const out = new Map<number, string | undefined>()
    for (const c of channels ?? []) out.set(c.channelId, currentProgramTitle(c.programs, now))
    return out
  }, [channels, now])

  /** The saved set, minus channels no longer listed (names refreshed). */
  const restorable = useMemo(() => {
    if (!channels) return saved
    return saved.flatMap((s) => {
      const c = channels.find((x) => x.channelId === s.channelId)
      return c ? [{ channelId: c.channelId, guideNumber: c.guideNumber, name: c.name }] : []
    })
  }, [channels, saved])

  const onPick = (channel: TileChannel) => {
    const p = picking
    setPicking(null)
    if (!p) return
    setMv((s) => (p.mode === 'add' ? addTile(s, channel, nextKey()) : replaceTile(s, p.key, channel, nextKey())))
  }

  const closePicker = useCallback(() => setPicking(null), [])

  const count = mv.tiles.length
  const layout = layoutFor(count)
  const full = count >= MAX_TILES
  const gridStyle = {
    '--mv-cols': layout.columns,
    '--mv-rows': layout.rows,
  } as CSSProperties

  return (
    <div className={styles.page}>
      <header className={styles.toolbar}>
        <div className={styles.toolbarLeft}>
          <span className={styles.brand}>
            <BowtieMark size={22} />
            Bowtie
          </span>
          <span className={styles.subtitle}>Multiview</span>
          {count > 1 ? <span className={styles.hint}>Press 1–{count} or tap a tile for sound</span> : null}
        </div>
        <div className={styles.toolbarRight}>
          <button
            type="button"
            className={`${styles.btn} ${styles.btnPrimary}`}
            onClick={() => setPicking({ mode: 'add' })}
            disabled={full}
            title={full ? `Up to ${MAX_TILES} channels` : undefined}
          >
            Add channel
          </button>
          {count === 0 && restorable.length > 0 ? (
            <button
              type="button"
              className={styles.btn}
              onClick={() => setMv(restoreTiles(restorable, nextKey))}
              title={restorable.map((c) => `${c.guideNumber} ${c.name}`).join(', ')}
            >
              Restore last ({restorable.length})
            </button>
          ) : null}
          <button type="button" className={styles.btn} onClick={onGuide}>
            Guide
          </button>
        </div>
      </header>

      <main className={styles.grid} style={gridStyle} data-count={count}>
        {mv.tiles.map((t, i) => (
          <MultiviewTile
            key={t.key}
            tile={t}
            index={i}
            audio={mv.audioKey === t.key}
            programTitle={titles.get(t.channel.channelId)}
            onAudio={() => setMv((s) => setAudio(s, t.key))}
            onChange={() => setPicking({ mode: 'replace', key: t.key })}
            onRemove={() => setMv((s) => removeTile(s, t.key))}
          />
        ))}
        {Array.from({ length: layout.emptySlots }, (_, i) => (
          <div key={`empty-${i}`} className={styles.emptySlot}>
            <button type="button" className={styles.addBtn} onClick={() => setPicking({ mode: 'add' })}>
              <span className={styles.addPlus} aria-hidden>
                +
              </span>
              Add channel
            </button>
            {count === 0 ? (
              <p className={styles.emptyHint}>
                Watch up to {MAX_TILES} channels at once — each different channel uses one of your tuners.
              </p>
            ) : null}
          </div>
        ))}
      </main>

      {picking ? (
        <MultiviewPicker
          title={picking.mode === 'add' ? 'Add channel' : 'Change channel'}
          channels={channels}
          error={channelsError}
          now={now}
          onScreen={mv.tiles.map((t) => t.channel.channelId)}
          onPick={onPick}
          onClose={closePicker}
        />
      ) : null}
    </div>
  )
}
