import type { CreateRecordingRuleRequest, RecordingRule } from '../api/client'

/** The Record series form (keepLatest is the raw input text). */
export type SeriesForm = {
  anyChannel: boolean
  newOnly: boolean
  keepLatest: string
}

/** This channel, new episodes only, keep all. */
export const DEFAULT_SERIES_FORM: SeriesForm = { anyChannel: false, newOnly: true, keepLatest: '0' }

/** Whole number ≥ 0 (blank = 0), or null when invalid. */
export function parseKeepLatest(raw: string): number | null {
  const t = raw.trim()
  if (t === '') return 0
  if (!/^\d+$/.test(t)) return null
  return Number(t)
}

export type RulePayloadResult =
  | { ok: true; body: CreateRecordingRuleRequest }
  | { ok: false; error: string }

/** POST /recording-rules body for a guide program, or the form error. */
export function buildRulePayload(
  program: { channelId: number; start: string },
  form: SeriesForm,
): RulePayloadResult {
  const keepLatest = parseKeepLatest(form.keepLatest)
  if (keepLatest === null) {
    return { ok: false, error: 'Keep latest must be a whole number (0 keeps all).' }
  }
  return {
    ok: true,
    body: {
      channelId: program.channelId,
      programStart: program.start,
      anyChannel: form.anyChannel,
      newOnly: form.newOnly,
      keepLatest,
    },
  }
}

/** Confirmation after a rule is saved. */
export function scheduledText(n: number): string {
  if (n === 0) return 'Scheduled 0 episodes. New airings are added when they show up in the guide.'
  return `Scheduled ${n} ${n === 1 ? 'episode' : 'episodes'}`
}

/** "KMSP · New episodes · Keep latest 5" for the Shows list. */
export function ruleSummary(
  rule: Pick<RecordingRule, 'channelId' | 'channelName' | 'newOnly' | 'keepLatest'>,
): string {
  const channel = rule.channelId === 0 ? 'Any channel' : rule.channelName || 'This channel'
  const episodes = rule.newOnly ? 'New episodes' : 'All episodes'
  const keep = rule.keepLatest > 0 ? `Keep latest ${rule.keepLatest}` : 'Keep all'
  return `${channel} · ${episodes} · ${keep}`
}

/** Record series is offered until the program ends. */
export function seriesAvailable(program: { stop: string }, now: Date): boolean {
  return Date.parse(program.stop) > now.getTime()
}

export function stopShowConfirmText(rule: Pick<RecordingRule, 'title'>): string {
  return `Stop recording “${rule.title}”? Upcoming episodes are cancelled; recorded ones stay.`
}
