import { useCallback, useEffect, useState, type FormEvent } from 'react'
import { ApiError, type User, type UserRole } from '../api/client'
import { useAuth } from '../auth/AuthContext'
import { QUALITY_OPTIONS, qualityLabel } from './adminModel'
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
  const [creating, setCreating] = useState(false)
  const [createError, setCreateError] = useState<string | null>(null)
  const [busyId, setBusyId] = useState<number | null>(null)

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
      })
      setUsername('')
      setPassword('')
      setRole('viewer')
      setMaxQuality('')
      await load()
    } catch (err) {
      setCreateError(err instanceof ApiError ? err.message || 'Create failed' : 'Create failed')
    } finally {
      setCreating(false)
    }
  }

  async function patchUser(id: number, body: { role?: UserRole; maxQuality?: string; password?: string }) {
    setBusyId(id)
    setError(null)
    try {
      const updated = await client.patchUser(id, body)
      setUsers((prev) => (prev ? prev.map((u) => (u.id === id ? updated : u)) : prev))
    } catch (err) {
      setError(err instanceof ApiError ? err.message || 'Update failed' : 'Update failed')
    } finally {
      setBusyId(null)
    }
  }

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
        <button type="submit" className={`${styles.btn} ${styles.btnPrimary}`} disabled={creating}>
          {creating ? 'Creating…' : 'Create user'}
        </button>
        {createError ? (
          <p className={styles.inlineError} role="alert" style={{ gridColumn: '1 / -1' }}>
            {createError}
          </p>
        ) : null}
      </form>

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
                <th scope="col">Actions</th>
              </tr>
            </thead>
            <tbody>
              {users.map((u) => (
                <tr key={u.id}>
                  <td>{u.username}</td>
                  <td>
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
                  <td>
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
                  <td>
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
    </div>
  )
}
