import { act, useRef, useState } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import {
  SLOW_START_NOTE,
  showsStarting,
  startingText,
  useAwaitingFirstFrame,
  useSlowStart,
} from './startingNote'
import playerSource from './Player.tsx?raw'

;(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

let host: HTMLDivElement
let root: Root

beforeEach(() => {
  vi.useFakeTimers()
  host = document.createElement('div')
  document.body.appendChild(host)
  root = createRoot(host)
})

afterEach(() => {
  act(() => root.unmount())
  host.remove()
  vi.useRealTimers()
})

const text = () => host.textContent ?? ''

function Probe({ initial }: { initial: { active: boolean; k: number } }) {
  const [s, set] = useState(initial)
  ;(Probe as unknown as { set: typeof set }).set = set
  const slow = useSlowStart(s.active, s.k)
  return <span>{slow ? 'slow' : 'not'}</span>
}
const setProbe = (v: { active: boolean; k: number }) =>
  act(() => (Probe as unknown as { set: (v: { active: boolean; k: number }) => void }).set(v))

describe('useSlowStart', () => {
  it('turns on after 5 s, restarts on a new key, and goes off when the start ends', () => {
    act(() => root.render(<Probe initial={{ active: true, k: 1 }} />))
    act(() => vi.advanceTimersByTime(4999))
    expect(text()).toBe('not')
    act(() => vi.advanceTimersByTime(1))
    expect(text()).toBe('slow')

    setProbe({ active: true, k: 2 }) // zap / quality / Try again
    expect(text()).toBe('not')
    act(() => vi.advanceTimersByTime(5000))
    expect(text()).toBe('slow')

    setProbe({ active: false, k: 2 }) // first frame
    expect(text()).toBe('not')
    act(() => vi.advanceTimersByTime(10_000))
    expect(text()).toBe('not')
  })
})

/** The Player's starting overlay, wired the same way (see the source check below). */
let answer: () => void = () => {}
function Stage() {
  const videoRef = useRef<HTMLVideoElement | null>(null)
  const [loading, setLoading] = useState(true)
  const [awaitingFrame, setAwaitingFrame] = useAwaitingFirstFrame(videoRef)
  answer = () => {
    setAwaitingFrame(true)
    setLoading(false)
  }
  const starting = showsStarting(loading, awaitingFrame, false)
  const slow = useSlowStart(starting, 'ch9')
  return (
    <div>
      <video ref={videoRef} />
      {starting ? <div>{startingText(slow)}</div> : null}
    </div>
  )
}

describe('starting overlay', () => {
  it('stays up (with the slow-start note) until the first frame loads', () => {
    act(() => root.render(<Stage />))
    expect(text()).toBe('Starting stream…')
    act(() => vi.advanceTimersByTime(5000))
    expect(text()).toBe(SLOW_START_NOTE)

    // The server answers: the picture isn't in yet, so the note stays.
    act(() => answer())
    expect(text()).toBe(SLOW_START_NOTE)

    // First frame: it goes.
    act(() => {
      host.querySelector('video')!.dispatchEvent(new Event('loadeddata'))
    })
    expect(text()).toBe('')
  })

  it('a failed start hides it', () => {
    expect(showsStarting(true, false, true)).toBe(false)
    expect(showsStarting(false, true, true)).toBe(false)
    expect(showsStarting(false, false, false)).toBe(false)
  })

  it('the Player uses this wiring', () => {
    const src = playerSource
    expect(src).toMatch(/useAwaitingFirstFrame\(videoRef\)/)
    expect(src).toMatch(/setAwaitingFrame\(true\)\n\s*attachPlayback\(res\.playlistUrl\)\n\s*setLoading\(false\)/)
    expect(src).toMatch(/const starting = showsStarting\(loading, awaitingFrame, !!error\)/)
    expect(src).toMatch(/useSlowStart\(starting, `\$\{target\.channelId\}\|\$\{profile\}\|\$\{sessionEpoch\}`\)/)
    expect(src).toMatch(/\{starting \? \([^]*?startingText\(slowStart\)/)
  })
})
