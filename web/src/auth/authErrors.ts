import { ApiError } from '../api/client'
import { viewerErrorText } from '../api/errorText'

/** The sign-in form's error in plain words. */
export function loginErrorText(err: unknown): string {
  if (err instanceof ApiError && (err.status === 401 || /invalid credentials/i.test(err.message))) {
    return 'Wrong username or password.'
  }
  return viewerErrorText(err, 'Login failed')
}

/** The /link page's error in plain words. */
export function linkErrorText(err: unknown, fallback: string): string {
  if (err instanceof ApiError && /expired or doesn.t exist/i.test(err.message)) {
    return "That code has expired or isn't right — check the TV and try again."
  }
  return viewerErrorText(err, fallback)
}
