import { describe, expect, it } from 'vitest'
import { ApiError } from '../api/client'
import { linkErrorText, loginErrorText } from './authErrors'

describe('loginErrorText', () => {
  it('says the username or password is wrong for 401 / invalid credentials', () => {
    expect(loginErrorText(new ApiError(401, 'invalid credentials'))).toBe('Wrong username or password.')
    expect(loginErrorText(new ApiError(401, ''))).toBe('Wrong username or password.')
  })

  it('keeps other server messages', () => {
    expect(loginErrorText(new ApiError(429, 'too many attempts, try again later'))).toBe(
      'too many attempts, try again later',
    )
  })

  it('falls back for empty or non-API errors', () => {
    expect(loginErrorText(new ApiError(500, ''))).toBe('Login failed')
    expect(loginErrorText(new TypeError('Failed to fetch'))).toBe('Login failed')
  })
})

describe('linkErrorText', () => {
  it('rewrites the expired / unknown code message', () => {
    expect(linkErrorText(new ApiError(404, "that code has expired or doesn't exist"), 'x')).toBe(
      "That code has expired or isn't right — check the TV and try again.",
    )
  })

  it('keeps other messages and falls back when there is none', () => {
    expect(linkErrorText(new ApiError(500, 'database is locked'), 'x')).toBe('database is locked')
    expect(linkErrorText(new ApiError(500, ''), 'Could not check that code. Try again.')).toBe(
      'Could not check that code. Try again.',
    )
    expect(linkErrorText(new Error('boom'), 'fallback')).toBe('fallback')
  })
})
