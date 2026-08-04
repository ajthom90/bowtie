import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from 'react'
import { ApiClient, type LoginResponse, type User } from '../api/client'

const ACCESS_KEY = 'bowtie.accessToken'
const REFRESH_KEY = 'bowtie.refreshToken'
const USER_KEY = 'bowtie.user'

type AuthState = {
  user: User | null
  ready: boolean
  login: (username: string, password: string) => Promise<void>
  logout: () => Promise<void>
  client: ApiClient
}

const AuthContext = createContext<AuthState | null>(null)

function readStoredUser(): User | null {
  try {
    const raw = localStorage.getItem(USER_KEY)
    if (!raw) return null
    return JSON.parse(raw) as User
  } catch {
    return null
  }
}

function persistSession(access: string, refresh: string, user: User) {
  localStorage.setItem(ACCESS_KEY, access)
  localStorage.setItem(REFRESH_KEY, refresh)
  localStorage.setItem(USER_KEY, JSON.stringify(user))
}

function clearSession() {
  localStorage.removeItem(ACCESS_KEY)
  localStorage.removeItem(REFRESH_KEY)
  localStorage.removeItem(USER_KEY)
}

export function AuthProvider({ children }: { children: ReactNode }) {
  const [user, setUser] = useState<User | null>(() => readStoredUser())
  const [ready, setReady] = useState(false)

  const onAuthFail = useCallback(() => {
    clearSession()
    setUser(null)
  }, [])

  const client = useMemo(() => {
    return new ApiClient(
      () => localStorage.getItem(ACCESS_KEY),
      onAuthFail,
      {
        getRefreshToken: () => localStorage.getItem(REFRESH_KEY),
        setTokens: (access, refresh) => {
          localStorage.setItem(ACCESS_KEY, access)
          localStorage.setItem(REFRESH_KEY, refresh)
        },
      },
    )
  }, [onAuthFail])

  // On boot: if we have a refresh token, rotate it and load the user.
  useEffect(() => {
    let cancelled = false
    ;(async () => {
      const rt = localStorage.getItem(REFRESH_KEY)
      if (!rt) {
        if (!cancelled) {
          clearSession()
          setUser(null)
          setReady(true)
        }
        return
      }
      try {
        const data: LoginResponse = await client.refresh(rt)
        if (cancelled) return
        persistSession(data.accessToken, data.refreshToken, data.user)
        setUser(data.user)
      } catch {
        if (cancelled) return
        clearSession()
        setUser(null)
      } finally {
        if (!cancelled) setReady(true)
      }
    })()
    return () => {
      cancelled = true
    }
  }, [client])

  const login = useCallback(
    async (username: string, password: string) => {
      const data = await client.login(username, password)
      persistSession(data.accessToken, data.refreshToken, data.user)
      setUser(data.user)
    },
    [client],
  )

  const logout = useCallback(async () => {
    const rt = localStorage.getItem(REFRESH_KEY)
    if (rt) {
      try {
        await client.logout(rt)
      } catch {
        // ignore
      }
    }
    clearSession()
    setUser(null)
  }, [client])

  const value = useMemo(
    () => ({ user, ready, login, logout, client }),
    [user, ready, login, logout, client],
  )

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>
}

export function useAuth(): AuthState {
  const ctx = useContext(AuthContext)
  if (!ctx) {
    throw new Error('useAuth must be used within AuthProvider')
  }
  return ctx
}
