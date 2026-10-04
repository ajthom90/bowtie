import { describe, expect, it, vi } from 'vitest'
import { createSessionAttempt } from './sessionLifecycle'

const h = { viewerId: 'v1', playlistUrl: '/hls/v1/index.m3u8?token=t' }

describe('createSessionAttempt', () => {
  it('stops a session that resolves after release, exactly once', () => {
    const stop = vi.fn()
    const a = createSessionAttempt(stop)
    a.release()
    expect(stop).not.toHaveBeenCalled()
    expect(a.resolve(h)).toBe(false)
    expect(stop).toHaveBeenCalledTimes(1)
    expect(stop).toHaveBeenCalledWith(h)
    a.release()
    expect(stop).toHaveBeenCalledTimes(1)
  })

  it('stops the open session on release, exactly once', () => {
    const stop = vi.fn()
    const a = createSessionAttempt(stop)
    expect(a.resolve(h)).toBe(true)
    expect(a.current()).toEqual(h)
    a.release()
    a.release()
    expect(stop).toHaveBeenCalledTimes(1)
    expect(stop).toHaveBeenCalledWith(h)
    expect(a.current()).toBeNull()
  })

  it('never stops when nothing was created', () => {
    const stop = vi.fn()
    const a = createSessionAttempt(stop)
    a.release()
    a.release()
    expect(stop).not.toHaveBeenCalled()
    expect(a.released).toBe(true)
  })

  it('forget drops a session the server already ended, so release sends nothing', () => {
    const stop = vi.fn()
    const a = createSessionAttempt(stop)
    a.resolve(h)
    a.forget()
    a.release()
    expect(stop).not.toHaveBeenCalled()
  })

  it('is not released until release is called', () => {
    const a = createSessionAttempt(() => {})
    expect(a.released).toBe(false)
    a.resolve(h)
    expect(a.released).toBe(false)
  })
})
