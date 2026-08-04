import { useCallback, useEffect, useMemo, useState } from 'react'
import { ApiError, type AdminChannel, type EPGChannel } from '../api/client'
import { useAuth } from '../auth/AuthContext'
import { filterAndSortChannels } from './adminModel'
import styles from './Admin.module.css'

export function Channels() {
  const { client } = useAuth()
  const [channels, setChannels] = useState<AdminChannel[] | null>(null)
  const [epgChannels, setEpgChannels] = useState<EPGChannel[]>([])
  const [filter, setFilter] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)
  const [savedId, setSavedId] = useState<number | null>(null)
  const [busyId, setBusyId] = useState<number | null>(null)

  const load = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      const [chans, epg] = await Promise.all([
        client.getAdminChannels(),
        client.getEPGChannels().catch(() => [] as EPGChannel[]),
      ])
      setChannels(chans)
      setEpgChannels(epg)
    } catch (err) {
      setError(err instanceof ApiError ? err.message || 'Failed to load channels' : 'Failed to load channels')
      setChannels(null)
    } finally {
      setLoading(false)
    }
  }, [client])

  useEffect(() => {
    void load()
  }, [load])

  const visible = useMemo(
    () => (channels ? filterAndSortChannels(channels, filter) : []),
    [channels, filter],
  )

  const epgOptions = useMemo(() => {
    return [...epgChannels].sort((a, b) =>
      (a.displayName || a.callsign || a.id).localeCompare(b.displayName || b.callsign || b.id),
    )
  }, [epgChannels])

  async function patch(id: number, body: { enabled?: boolean; epgChannelId?: string }) {
    setBusyId(id)
    setError(null)
    try {
      const updated = await client.patchChannel(id, body)
      setChannels((prev) => (prev ? prev.map((c) => (c.id === id ? updated : c)) : prev))
      setSavedId(id)
      window.setTimeout(() => {
        setSavedId((cur) => (cur === id ? null : cur))
      }, 1500)
    } catch (err) {
      setError(err instanceof ApiError ? err.message || 'Save failed' : 'Save failed')
    } finally {
      setBusyId(null)
    }
  }

  return (
    <div>
      <div className={styles.sectionHead}>
        <h2 className={styles.sectionTitle}>Channels</h2>
        <label className={styles.label} style={{ margin: 0 }}>
          <span className="visually-hidden">Filter channels</span>
          <input
            className={`${styles.input} ${styles.filterInput}`}
            type="search"
            placeholder="Filter by number or name…"
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
            aria-label="Filter channels"
          />
        </label>
      </div>

      {loading && !channels ? <p className={styles.status}>Loading channels…</p> : null}
      {error ? <p className={styles.statusError}>{error}</p> : null}

      {!loading && channels && channels.length === 0 ? (
        <p className={styles.empty}>
          No channels yet. Add an HDHomeRun on Tuners, then sync lineups.
        </p>
      ) : null}

      {channels && channels.length > 0 && visible.length === 0 ? (
        <p className={styles.empty}>No channels match “{filter}”.</p>
      ) : null}

      {visible.length > 0 ? (
        <div className={styles.tableWrap}>
          <table className={styles.table}>
            <thead>
              <tr>
                <th scope="col">On</th>
                <th scope="col">#</th>
                <th scope="col">Name</th>
                <th scope="col">EPG mapping</th>
                <th scope="col">
                  <span className="visually-hidden">Status</span>
                </th>
              </tr>
            </thead>
            <tbody>
              {visible.map((ch) => (
                <tr key={ch.id}>
                  <td>
                    <input
                      className={styles.toggle}
                      type="checkbox"
                      checked={ch.enabled}
                      disabled={busyId === ch.id}
                      onChange={(e) => void patch(ch.id, { enabled: e.target.checked })}
                      aria-label={`Enable ${ch.guideNumber} ${ch.name}`}
                    />
                  </td>
                  <td>
                    <span className={styles.guideNum}>{ch.guideNumber}</span>
                  </td>
                  <td>{ch.name}</td>
                  <td>
                    <select
                      className={`${styles.select} ${styles.selectSm}`}
                      value={ch.epgChannelId}
                      disabled={busyId === ch.id}
                      onChange={(e) => void patch(ch.id, { epgChannelId: e.target.value })}
                      aria-label={`EPG mapping for ${ch.guideNumber}`}
                    >
                      <option value="">Not mapped</option>
                      {epgOptions.map((epg) => (
                        <option key={epg.id} value={epg.id}>
                          {epg.displayName || epg.callsign || epg.id}
                          {epg.source ? ` (${epg.source})` : ''}
                        </option>
                      ))}
                    </select>
                  </td>
                  <td>
                    {savedId === ch.id ? <span className={styles.savedFlash}>Saved</span> : null}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : null}
    </div>
  )
}
