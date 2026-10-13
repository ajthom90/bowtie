import { useEffect, useRef, useState } from 'react'
import { type RecordingRule } from '../api/client'
import { viewerErrorText } from '../api/errorText'
import { useAuth } from '../auth/AuthContext'
import styles from './ManualRecord.module.css'

type Props = {
  rule: RecordingRule
  onClose: () => void
  onSaved: () => Promise<void>
}

export function EditRuleDialog({ rule, onClose, onSaved }: Props) {
  const { client } = useAuth()
  const dialog = useRef<HTMLDialogElement>(null)
  const [newOnly, setNewOnly] = useState(rule.newOnly)
  const [channelId, setChannelId] = useState(rule.channelId)
  const [channels, setChannels] = useState(
    () => new Map(rule.channelId ? [[rule.channelId, rule.channelName]] : []),
  )
  const [keepAll, setKeepAll] = useState(rule.keepLatest === 0)
  const [count, setCount] = useState(String(rule.keepLatest || 5))
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    dialog.current?.showModal()
    let live = true
    void client.listRecordings('upcoming').then((rows) => {
      if (!live) return
      const airingChannels = rows
        .filter((r) => r.ruleId === rule.id)
        .map((r): [number, string] => [r.channelId, r.channelName])
      setChannels((existing) => new Map([...existing, ...airingChannels]))
    }).catch(() => {
      // The rule's current channel remains available.
    })
    return () => { live = false }
  }, [client, rule.id])

  const save = async () => {
    const keepLatest = keepAll ? 0 : Number(count)
    if (!Number.isSafeInteger(keepLatest) || (!keepAll && keepLatest < 1)) {
      setError('Enter a whole number of episodes greater than zero.')
      return
    }
    setBusy(true)
    setError(null)
    try {
      await client.patchRecordingRule(rule.id, { newOnly, channelId, keepLatest })
      await client.listRecordings('upcoming')
      await onSaved()
    } catch (err) {
      setError(viewerErrorText(err, 'Could not update this show.'))
    } finally {
      setBusy(false)
    }
  }

  return (
    <dialog
      ref={dialog}
      className={styles.sheet}
      aria-labelledby="edit-rule-title"
      onCancel={(e) => {
        e.preventDefault()
        if (!busy) onClose()
      }}
    >
      <form onSubmit={(e) => { e.preventDefault(); void save() }}>
        <h2 id="edit-rule-title" className={styles.title}>Edit {rule.title}</h2>
        <fieldset disabled={busy} style={{ border: 0, padding: 0 }}>
          <div className={styles.field}>
            <label htmlFor="rule-episodes">Episodes</label>
            <select
              id="rule-episodes"
              value={String(newOnly)}
              onChange={(e) => setNewOnly(e.target.value === 'true')}
            >
              <option value="true">New episodes only</option>
              <option value="false">All episodes</option>
            </select>
          </div>
          <div className={styles.field}>
            <label htmlFor="rule-channel">Channel</label>
            <select
              id="rule-channel"
              value={channelId}
              onChange={(e) => setChannelId(Number(e.target.value))}
            >
              <option value={0}>Any channel</option>
              {[...channels].map(([id, name]) => <option key={id} value={id}>{name}</option>)}
            </select>
          </div>
          <div className={styles.field}>
            <label htmlFor="rule-keep">Keep</label>
            <select
              id="rule-keep"
              value={String(keepAll)}
              onChange={(e) => setKeepAll(e.target.value === 'true')}
            >
              <option value="true">Keep all</option>
              <option value="false">Keep latest N</option>
            </select>
          </div>
          {!keepAll ? (
            <div className={styles.field}>
              <label htmlFor="rule-count">Episodes to keep</label>
              <input
                id="rule-count"
                type="number"
                min="1"
                step="1"
                required
                value={count}
                onChange={(e) => setCount(e.target.value)}
              />
            </div>
          ) : null}
          <p className={styles.hint}>
            The keep limit applies when new episodes finish recording. Saving deletes no recorded episodes.
          </p>
          {error ? <p role="alert" className={styles.error}>{error}</p> : null}
          <div className={styles.actions}>
            <button type="button" onClick={onClose}>Cancel</button>
            <button type="submit">{busy ? 'Saving…' : 'Save'}</button>
          </div>
        </fieldset>
      </form>
    </dialog>
  )
}
