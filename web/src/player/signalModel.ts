import type { ReceptionSignal } from '../api/client'

/** A reading as a whole percent in 0–100. */
function pct(n: number): string {
  return `${Math.min(100, Math.max(0, Math.round(n)))}%`
}

/**
 * The live player's note while the antenna signal is weak, or null. The
 * server only says `weak` after two bad readings in a row, so this never
 * flickers on a single blip; unknown (null, or an older server) shows nothing.
 * Quality, not strength: strength can read 96% while the picture breaks up.
 */
export function weakSignalNote(signal: ReceptionSignal | null | undefined): string | null {
  return signal?.weak === true ? `Weak signal (${pct(signal.quality)}) — the picture may break up.` : null
}

/** The Stats panel's reception line ("error-free" is symbol quality), or null when unknown. */
export function signalStatsLine(signal: ReceptionSignal | null | undefined): string | null {
  if (!signal) return null
  return `Signal quality ${pct(signal.quality)} · strength ${pct(signal.strength)} · error-free ${pct(signal.symbolQuality)}`
}
