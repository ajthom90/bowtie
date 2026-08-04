export interface User {
  id: number
  username: string
  role: 'admin' | 'viewer'
  maxQuality: string
}

export interface LoginResponse {
  accessToken: string
  refreshToken: string
  user: User
}

export type TokenHooks = {
  /** Return the current refresh token (used for silent refresh on 401). */
  getRefreshToken: () => string | null
  /** Persist rotated tokens after a successful refresh. */
  setTokens: (accessToken: string, refreshToken: string) => void
}

/**
 * Typed fetch wrapper for the Bowtie API.
 *
 * Attaches Bearer access token; on 401, performs one refresh + retry, then
 * calls onAuthFail if still unauthorized.
 */
export class ApiClient {
  private getToken: () => string | null
  private onAuthFail: () => void
  private hooks: TokenHooks
  /** In-flight refresh so concurrent 401s share a single rotate call. */
  private refreshPromise: Promise<boolean> | null = null

  constructor(
    getToken: () => string | null,
    onAuthFail: () => void,
    hooks: TokenHooks = {
      getRefreshToken: () => null,
      setTokens: () => {},
    },
  ) {
    this.getToken = getToken
    this.onAuthFail = onAuthFail
    this.hooks = hooks
  }

  async login(username: string, password: string): Promise<LoginResponse> {
    return this.postJSON<LoginResponse>('/api/v1/auth/login', { username, password }, false)
  }

  async refresh(refreshToken: string): Promise<LoginResponse> {
    return this.postJSON<LoginResponse>('/api/v1/auth/refresh', { refreshToken }, false)
  }

  async me(): Promise<User> {
    return this.request<User>('GET', '/api/v1/me')
  }

  async logout(refreshToken: string): Promise<void> {
    const res = await fetch('/api/v1/auth/logout', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ refreshToken }),
    })
    if (!res.ok && res.status !== 204) {
      // Logout is best-effort; ignore body errors.
    }
  }

  /**
   * Authenticated request. Attaches Bearer token; on 401, refreshes once and
   * retries. If the retry still fails with 401 (or refresh fails), calls
   * onAuthFail and throws.
   */
  async request<T>(method: string, path: string, body?: unknown): Promise<T> {
    const doFetch = async (): Promise<Response> => {
      const headers: Record<string, string> = {}
      const token = this.getToken()
      if (token) {
        headers['Authorization'] = `Bearer ${token}`
      }
      let initBody: string | undefined
      if (body !== undefined) {
        headers['Content-Type'] = 'application/json'
        initBody = JSON.stringify(body)
      }
      return fetch(path, { method, headers, body: initBody })
    }

    let res = await doFetch()
    if (res.status === 401) {
      const ok = await this.tryRefreshOnce()
      if (ok) {
        res = await doFetch()
      }
      if (res.status === 401 || !ok) {
        this.onAuthFail()
        throw new ApiError(401, 'unauthorized')
      }
    }

    return this.parseJSON<T>(res)
  }

  private async tryRefreshOnce(): Promise<boolean> {
    if (this.refreshPromise) {
      return this.refreshPromise
    }
    this.refreshPromise = (async () => {
      const rt = this.hooks.getRefreshToken()
      if (!rt) {
        return false
      }
      try {
        const data = await this.refresh(rt)
        this.hooks.setTokens(data.accessToken, data.refreshToken)
        return true
      } catch {
        return false
      }
    })()
    try {
      return await this.refreshPromise
    } finally {
      this.refreshPromise = null
    }
  }

  private async postJSON<T>(path: string, body: unknown, auth: boolean): Promise<T> {
    const headers: Record<string, string> = { 'Content-Type': 'application/json' }
    if (auth) {
      const token = this.getToken()
      if (token) {
        headers['Authorization'] = `Bearer ${token}`
      }
    }
    const res = await fetch(path, {
      method: 'POST',
      headers,
      body: JSON.stringify(body),
    })
    return this.parseJSON<T>(res)
  }

  private async parseJSON<T>(res: Response): Promise<T> {
    if (res.status === 204) {
      return undefined as T
    }
    let data: unknown = null
    const text = await res.text()
    if (text) {
      try {
        data = JSON.parse(text)
      } catch {
        if (!res.ok) {
          throw new ApiError(res.status, text || res.statusText)
        }
        throw new ApiError(res.status, 'invalid JSON response')
      }
    }
    if (!res.ok) {
      const msg =
        data && typeof data === 'object' && data !== null && 'error' in data
          ? String((data as { error: unknown }).error)
          : res.statusText
      throw new ApiError(res.status, msg)
    }
    return data as T
  }
}

export class ApiError extends Error {
  status: number

  constructor(status: number, message: string) {
    super(message)
    this.name = 'ApiError'
    this.status = status
  }
}
