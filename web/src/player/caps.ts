export interface ClientCaps {
  videoCodecs: string[]
  audioCodecs: string[]
  /** 0 = no limit. */
  maxHeight: number
  /** Quality ladder name; empty defaults to original on the server. */
  profile: string
}

const HEVC_MSE = 'video/mp4; codecs="hvc1.1.6.L93.B0"'
const AC3_MSE = 'audio/mp4; codecs="ac-3"'

/**
 * Detect client streaming capabilities for session negotiation.
 * h264 + aac are always advertised; hevc/ac3 only when the browser can play them.
 */
export function detectCaps(profile: string = ''): ClientCaps {
  const videoCodecs = ['h264']
  if (supportsHevc()) {
    videoCodecs.push('hevc')
  }

  const audioCodecs = ['aac']
  if (supportsAc3()) {
    audioCodecs.push('ac3')
  }

  return {
    videoCodecs,
    audioCodecs,
    maxHeight: detectMaxHeight(),
    profile,
  }
}

function supportsHevc(): boolean {
  if (typeof MediaSource !== 'undefined' && typeof MediaSource.isTypeSupported === 'function') {
    if (MediaSource.isTypeSupported(HEVC_MSE)) {
      return true
    }
  }
  // Safari often reports HEVC via video.canPlayType rather than MSE.
  if (typeof document !== 'undefined') {
    try {
      const v = document.createElement('video')
      const r = v.canPlayType('video/mp4; codecs="hvc1.1.6.L93.B0"')
      if (r === 'probably' || r === 'maybe') {
        return true
      }
    } catch {
      // ignore
    }
  }
  return false
}

function supportsAc3(): boolean {
  if (typeof MediaSource !== 'undefined' && typeof MediaSource.isTypeSupported === 'function') {
    if (MediaSource.isTypeSupported(AC3_MSE)) {
      return true
    }
  }
  if (typeof document !== 'undefined') {
    try {
      const v = document.createElement('video')
      const r = v.canPlayType('audio/mp4; codecs="ac-3"')
      if (r === 'probably' || r === 'maybe') {
        return true
      }
    } catch {
      // ignore
    }
  }
  return false
}

function detectMaxHeight(): number {
  if (typeof window === 'undefined' || !window.screen) {
    return 0
  }
  const h = window.screen.height || 0
  const dpr = window.devicePixelRatio || 1
  // Physical pixels give the server a better clamp; 0 means no limit.
  const physical = Math.round(h * dpr)
  return physical > 0 ? physical : 0
}

/** Whether native HLS (Safari) can play without hls.js. */
export function canPlayNativeHls(): boolean {
  if (typeof document === 'undefined') return false
  try {
    const v = document.createElement('video')
    const r = v.canPlayType('application/vnd.apple.mpegurl')
    return r === 'probably' || r === 'maybe'
  } catch {
    return false
  }
}
