// Record by time: a manual recording window on one channel.
//
// Times are wall-clock values from <input type="date"> / <input type="time">.
// Dates are always built with the local Date constructor (never by adding
// milliseconds) so a window across a DST change keeps the clock times typed.

/** Longest manual recording allowed, in real hours. */
export const MAX_HOURS = 12

export type ManualRecordForm = {
  channelId: number | null
  /** YYYY-MM-DD */
  date: string
  /** HH:MM (24-hour) */
  start: string
  /** HH:MM; at or before start means the next day. */
  end: string
  title: string
}

export type ManualWindow = { start: Date; stop: Date }

export type ManualRequestBody = { channelId: number; start: string; stop: string; title: string }

const pad = (n: number) => String(n).padStart(2, '0')

/** YYYY-MM-DD of the local calendar day (for a date input). */
export function localDateValue(d: Date): string {
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`
}

function minutesToTime(min: number): string {
  const m = ((min % 1440) + 1440) % 1440
  return `${pad(Math.floor(m / 60))}:${pad(m % 60)}`
}

function parseTime(value: string): number | null {
  const m = /^(\d{1,2}):(\d{2})$/.exec(value.trim())
  if (!m) return null
  const h = Number(m[1])
  const min = Number(m[2])
  if (h > 23 || min > 59) return null
  return h * 60 + min
}

function parseDate(value: string): [number, number, number] | null {
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(value.trim())
  if (!m) return null
  const y = Number(m[1])
  const mo = Number(m[2])
  const d = Number(m[3])
  if (mo < 1 || mo > 12 || d < 1 || d > 31) return null
  // Reject days the month doesn't have (Feb 30 would roll into March).
  if (new Date(y, mo - 1, d).getDate() !== d) return null
  return [y, mo - 1, d]
}

/** The start time to suggest: now, rounded up to the next quarter hour. */
export function defaultStartTime(now: Date): string {
  const min = now.getHours() * 60 + now.getMinutes() + (now.getSeconds() > 0 ? 1 : 0)
  return minutesToTime(Math.ceil(min / 15) * 15)
}

/** HH:MM one hour later on the clock (wraps past midnight). */
export function plusOneHour(time: string): string {
  const min = parseTime(time)
  return min === null ? time : minutesToTime(min + 60)
}

/** Start and stop from the form fields; null when any is missing or invalid. */
export function buildWindow(date: string, start: string, end: string): ManualWindow | null {
  const day = parseDate(date)
  const s = parseTime(start)
  const e = parseTime(end)
  if (!day || s === null || e === null) return null
  const [y, mo, d] = day
  const startAt = new Date(y, mo, d, Math.floor(s / 60), s % 60)
  // An end at or before the start is on the next day (23:00–01:00).
  const stopDay = e <= s ? d + 1 : d
  const stopAt = new Date(y, mo, stopDay, Math.floor(e / 60), e % 60)
  return { start: startAt, stop: stopAt }
}

/** Why the window can't be recorded, or null when it can. */
export function validateWindow(w: ManualWindow | null, now: Date): string | null {
  if (!w) return 'Enter a date, start time and end time.'
  if (w.stop.getTime() <= now.getTime()) return 'That time has already passed.'
  if (w.stop.getTime() - w.start.getTime() > MAX_HOURS * 3_600_000) {
    return `A recording can be at most ${MAX_HOURS} hours.`
  }
  return null
}

/** "KMSP Oct 4 8:00 PM" */
export function defaultTitle(channelName: string, start: Date, locale?: string): string {
  const date = start.toLocaleDateString(locale, { month: 'short', day: 'numeric' })
  const time = start.toLocaleTimeString(locale, { hour: 'numeric', minute: '2-digit' })
  // Some locales put a narrow no-break space before AM/PM; keep plain spaces.
  return `${channelName.trim() || 'Recording'} ${date} ${time}`.replace(/\s+/g, ' ')
}

/** The POST /recordings body, or the message to show. */
export function buildManualRequest(
  form: ManualRecordForm,
  now: Date,
): { ok: true; body: ManualRequestBody } | { ok: false; error: string } {
  if (form.channelId === null) return { ok: false, error: 'Choose a channel.' }
  const w = buildWindow(form.date, form.start, form.end)
  const problem = validateWindow(w, now)
  if (problem || !w) return { ok: false, error: problem ?? 'Enter a date, start time and end time.' }
  const title = form.title.trim()
  if (!title) return { ok: false, error: 'Enter a title.' }
  return {
    ok: true,
    body: { channelId: form.channelId, start: w.start.toISOString(), stop: w.stop.toISOString(), title },
  }
}
