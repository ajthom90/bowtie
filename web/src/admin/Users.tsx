import { useCallback, useEffect, useState, type FormEvent } from 'react'
import {
  ApiError,
  type AdminChannel,
  type PatchUserRequest,
  type User,
  type UserRole,
} from '../api/client'
import { useAuth } from '../auth/AuthContext'
import { LIMIT_OPTIONS, QUALITY_OPTIONS, qualityLabel } from './adminModel'
import { ChannelPicker } from './ChannelPicker'
import {
  RATINGS_NOTE,
  RATING_OPTIONS,
  channelsSummary,
  parentalEditable,
  pickerChannels,
  ratingLabel,
} from './parentalModel'
import styles from './Admin.module.css'

export function Users() {
  const { client } = useAuth()
  const [users, setUsers] = useState<User[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)

  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [role, setRole] = useState<UserRole>('viewer')
  const [maxQuality, setMaxQuality] = useState('')
  const [maxStreams, setMaxStreams] = useState(0)
  const [maxTuners, setMaxTuners] = useState(0)
  const [creating, setCreating] = useState(false)
  const [createError, setCreateError] = useState<string | null>(null)
  const [busyId, setBusyId] = useState<number | null>(null)
  /** For the parental-controls channel picker (best-effort). */
  const [channels, setChannels] = useState<AdminChannel[]>([])
  const [pickerFor, setPickerFor] = useState<User | null>(null)

  useEffect(() => {
    client.getAdminChannels().then(setChannels, () => setChannels([]))
  }, [client])

  const load = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      const data = await client.getAdminUsers()
      setUsers(data)
    } catch (err) {
      setError(err instanceof ApiError ? err.message || 'Failed to load users' : 'Failed to load users')
      setUsers(null)
    } finally {
      setLoading(false)
    }
  }, [client])

  useEffect(() => {
    void load()
  }, [load])

  async function onCreate(e: FormEvent) {
    e.preventDefault()
    setCreateError(null)
    setCreating(true)
    try {
      await client.createUser({
        username: username.trim(),
        password,
        role,
        maxQuality,
        maxStreams,
        maxTuners,
      })
      setUsername('')
      setPassword('')
      setRole('viewer')
      setMaxQuality('')
      setMaxStreams(0)
      setMaxTuners(0)
      await load()
    } catch (err) {
      setCreateError(err instanceof ApiError ? err.message || 'Create failed' : 'Create failed')
    } finally {
      setCreating(false)
    }
  }

  async function patchUser(id: number, body: PatchUserRequest): Promise<boolean> {
    setBusyId(id)
    setError(null)
    try {
      const updated = await client.patchUser(id, body)
      setUsers((prev) => (prev ? prev.map((u) => (u.id === id ? updated : u)) : prev))
      return true
    } catch (err) {
      setError(err instanceof ApiError ? err.message || 'Update failed' : 'Update failed')
      return false
    } finally {
      setBusyId(null)
    }
  }

  const channelTotal = pickerChannels(channels, null).length

  async function onResetPassword(u: User) {
    const next = window.prompt(`New password for ${u.username}:`)
    if (next == null) return
    if (!next.trim()) {
      setError('Password cannot be empty')
      return
    }
    await patchUser(u.id, { password: next })
  }

  async function onDelete(u: User) {
    if (!window.confirm(`Delete user “${u.username}”?`)) return
    setBusyId(u.id)
    setError(null)
    try {
      await client.deleteUser(u.id)
      setUsers((prev) => (prev ? prev.filter((x) => x.id !== u.id) : prev))
    } catch (err) {
      // 409 last-admin: show server message.
      setError(err instanceof ApiError ? err.message || 'Delete failed' : 'Delete failed')
    } finally {
      setBusyId(null)
    }
  }

  return (
    <div>
      <div className={styles.sectionHead}>
        <h2 className={styles.sectionTitle}>Users</h2>
      </div>

      <form className={styles.createForm} onSubmit={(e) => void onCreate(e)}>
        <label className={styles.label}>
          Username
          <input
            className={styles.input}
            name="newUsername"
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            required
            autoComplete="off"
            disabled={creating}
          />
        </label>
        <label className={styles.label}>
          Password
          <input
            className={styles.input}
            name="newPassword"
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            required
            autoComplete="new-password"
            disabled={creating}
          />
        </label>
        <label className={styles.label}>
          Role
          <select
            className={styles.select}
            value={role}
            onChange={(e) => setRole(e.target.value as UserRole)}
            disabled={creating}
          >
            <option value="viewer">Viewer</option>
            <option value="admin">Admin</option>
          </select>
        </label>
        <label className={styles.label}>
          Max quality
          <select
            className={styles.select}
            value={maxQuality}
            onChange={(e) => setMaxQuality(e.target.value)}
            disabled={creating}
          >
            {QUALITY_OPTIONS.map((o) => (
              <option key={o.value || 'unlimited'} value={o.value}>
                {o.label}
              </option>
            ))}
          </select>
        </label>
        <LimitSelect label="Streams" value={maxStreams} onChange={setMaxStreams} disabled={creating} />
        <LimitSelect label="Tuners" value={maxTuners} onChange={setMaxTuners} disabled={creating} />
        <button type="submit" className={`${styles.btn} ${styles.btnPrimary}`} disabled={creating}>
          {creating ? 'Creating…' : 'Create user'}
        </button>
        {createError ? (
          <p className={styles.inlineError} role="alert" style={{ gridColumn: '1 / -1' }}>
            {createError}
          </p>
        ) : null}
      </form>

      <p className={styles.note}>
        <strong>Parental controls</strong> limit what an account can see and watch: its channels,
        the highest rating that plays, and whether unrated programs play. {RATINGS_NOTE}
      </p>

      {loading && !users ? <p className={styles.status}>Loading users…</p> : null}
      {error ? <p className={styles.statusError}>{error}</p> : null}

      {!loading && users && users.length === 0 ? (
        <p className={styles.empty}>No users. Create the first account above.</p>
      ) : null}

      {users && users.length > 0 ? (
        <div className={styles.tableWrap}>
          <table className={styles.table}>
            <thead>
              <tr>
                <th scope="col">Username</th>
                <th scope="col">Role</th>
                <th scope="col">Max quality</th>
                <th scope="col">Streams</th>
                <th scope="col">Tuners</th>
                <th scope="col">Channels</th>
                <th scope="col">Max rating</th>
                <th scope="col">Unrated</th>
                <th scope="col">Actions</th>
              </tr>
            </thead>
            <tbody>
              {users.map((u) => (
                <tr key={u.id}>
                  <td data-label="Username">{u.username}</td>
                  <td data-label="Role">
                    <select
                      className={`${styles.select} ${styles.selectSm}`}
                      value={u.role}
                      disabled={busyId === u.id}
                      onChange={(e) => void patchUser(u.id, { role: e.target.value as UserRole })}
                      aria-label={`Role for ${u.username}`}
                    >
                      <option value="viewer">Viewer</option>
                      <option value="admin">Admin</option>
                    </select>
                  </td>
                  <td data-label="Max quality">
                    <select
                      className={`${styles.select} ${styles.selectSm}`}
                      value={u.maxQuality}
                      disabled={busyId === u.id}
                      onChange={(e) => void patchUser(u.id, { maxQuality: e.target.value })}
                      aria-label={`Max quality for ${u.username}`}
                    >
                      {QUALITY_OPTIONS.map((o) => (
                        <option key={o.value || 'unlimited'} value={o.value}>
                          {o.label}
                        </option>
                      ))}
                      {/* Preserve unknown server values */}
                      {!QUALITY_OPTIONS.some((o) => o.value === u.maxQuality) ? (
                        <option value={u.maxQuality}>{qualityLabel(u.maxQuality)}</option>
                      ) : null}
                    </select>
                  </td>
                  <td data-label="Streams">
                    <LimitSelect
                      small
                      label={`Streams for ${u.username}`}
                      value={u.maxStreams ?? 0}
                      disabled={busyId === u.id || u.role === 'admin'}
                      onChange={(n) => void patchUser(u.id, { maxStreams: n })}
                    />
                  </td>
                  <td data-label="Tuners">
                    <LimitSelect
                      small
                      label={`Tuners for ${u.username}`}
                      value={u.maxTuners ?? 0}
                      disabled={busyId === u.id || u.role === 'admin'}
                      onChange={(n) => void patchUser(u.id, { maxTuners: n })}
                    />
                  </td>
                  <td data-label="Channels">
                    <button
                      type="button"
                      className={`${styles.btn} ${styles.btnSm} ${styles.pickBtn}`}
                      disabled={busyId === u.id || !parentalEditable(u)}
                      onClick={() => setPickerFor(u)}
                      aria-label={`Allowed channels for ${u.username}: ${channelsSummary(u.allowedChannelIds, channelTotal)}`}
                      title={parentalEditable(u) ? undefined : 'Admins are never restricted'}
                    >
                      {channelsSummary(u.allowedChannelIds, channelTotal)}
                    </button>
                  </td>
                  <td data-label="Max rating">
                    <select
                      className={`${styles.select} ${styles.selectSm}`}
                      value={u.maxRating ?? ''}
                      disabled={busyId === u.id || !parentalEditable(u)}
                      onChange={(e) => void patchUser(u.id, { maxRating: e.target.value })}
                      aria-label={`Max rating for ${u.username}`}
                    >
                      {RATING_OPTIONS.map((o) => (
                        <option key={o.value || 'none'} value={o.value}>
                          {o.label}
                        </option>
                      ))}
                      {!RATING_OPTIONS.some((o) => o.value === (u.maxRating ?? '')) ? (
                        <option value={u.maxRating}>{ratingLabel(u.maxRating)}</option>
                      ) : null}
                    </select>
                  </td>
                  <td data-label="Unrated">
                    <label className={styles.checkLabel}>
                      <input
                        className={styles.toggle}
                        type="checkbox"
                        checked={u.blockUnrated === true}
                        disabled={busyId === u.id || !parentalEditable(u)}
                        onChange={(e) => void patchUser(u.id, { blockUnrated: e.target.checked })}
                        aria-label={`Block unrated programs for ${u.username}`}
                      />
                      Block
                    </label>
                  </td>
                  <td data-label="Actions" className={styles.cardActions}>
                    <div className={styles.actions}>
                      <button
                        type="button"
                        className={`${styles.btn} ${styles.btnSm}`}
                        disabled={busyId === u.id}
                        onClick={() => void onResetPassword(u)}
                      >
                        Reset password
                      </button>
                      <button
                        type="button"
                        className={`${styles.btn} ${styles.btnSm} ${styles.btnDanger}`}
                        disabled={busyId === u.id}
                        onClick={() => void onDelete(u)}
                      >
                        Delete
                      </button>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : null}

      {pickerFor ? (
        <ChannelPicker
          user={pickerFor}
          channels={channels}
          busy={busyId === pickerFor.id}
          onClose={() => setPickerFor(null)}
          onSave={(allowed) => {
            // null = every channel (an absent field would keep the old list).
            void patchUser(pickerFor.id, { allowedChannelIds: allowed }).then((ok) => {
              if (ok) setPickerFor(null)
            })
          }}
        />
      ) : null}
    </div>
  )
}

/**
 * Stream/tuner limit picker. In the create form it renders its own label; in
 * the table (small) the label is the accessible name only. Admins are never
 * limited, so their rows are disabled.
 */
function LimitSelect({
  label,
  value,
  onChange,
  disabled,
  small = false,
}: {
  label: string
  value: number
  onChange: (n: number) => void
  disabled: boolean
  small?: boolean
}) {
  const select = (
    <select
      className={small ? `${styles.select} ${styles.selectSm}` : styles.select}
      value={value}
      disabled={disabled}
      onChange={(e) => onChange(Number(e.target.value))}
      aria-label={small ? label : undefined}
      title={label.startsWith('Tuners') ? "Joining a channel someone else is watching doesn't use a tuner" : undefined}
    >
      {LIMIT_OPTIONS.map((o) => (
        <option key={o.value} value={o.value}>
          {o.label}
        </option>
      ))}
    </select>
  )
  if (small) return select
  return (
    <label className={styles.label}>
      {label}
      {select}
    </label>
  )
}
