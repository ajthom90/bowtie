import { ApiError } from '../api/client'

/** The sign-in form's error in plain words. */
export function loginErrorText(err: unknown): string {
  if (!(err instanceof ApiError)) return 'Login failed'
  if (err.status === 401 || /invalid credentials/i.test(err.message)) {
    return 'Wrong username or password.'
  }
  return err.message || 'Login failed'
}

/** The /link page's error in plain words. */
export function linkErrorText(err: unknown, fallback: string): string {
  if (!(err instanceof ApiError) || !err.message) return fallback
  if (/expired or doesn.t exist/i.test(err.message)) {
    return "That code has expired or isn't right — check the TV and try again."
  }
  return err.message
}
