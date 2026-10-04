# Per-account limits (streams and tuners) — Design

Date: 2026-10-03. Requested: "support multiple users on the backend. Allow
admins to create users to share the antenna with. Allow limits like number of
sessions, number of tuners, etc." Decisions taken with the user's standing
instruction to go with recommendations.

## What exists

- Admins already create viewer accounts (Admin → Users) with a quality cap
  (`users.max_quality`, enforced in `transcode.Negotiate`).
- Viewers of the same channel already share one tuner (ingest fan-out) and,
  since 0.8.0, one transcode per quality (or per channel with adaptive
  quality on).
- Nothing limits how many streams or tuners one account uses.

## Goal

An admin can cap, per account, how many streams it may watch at once and how
many tuners it may occupy, so a friend sharing the antenna can't take every
tuner from the household.

## Design

### Limits (per user, 0 = unlimited)
- **Streams** (`max_streams`): concurrent viewers (players) for the account,
  across all channels and devices.
- **Tuners** (`max_tuners`): distinct channels the account is watching that
  **no other account is also watching** — joining a channel someone else
  already has costs no tuner, so it never counts against the limit.
Admins have no limits (fields ignored for role admin).

### Enforcement (server, `stream.Manager.Start`)
Before join-or-create, under `m.mu`, count the user's live viewers
(`Viewer.UserID`, new) and the channels only they are watching. If starting
this viewer would exceed a limit, return `ErrUserLimit{Kind, Limit}`:
- streams: user already has `max_streams` viewers;
- tuners: the channel has no session (or none with other users' viewers) and
  the user's sole-watcher channels already equal `max_tuners`.
A viewer re-created by a quality change or zap first removes its old viewer
(clients already DELETE before POST), so it doesn't count against itself.

### API
- `users` table: `max_streams INTEGER NOT NULL DEFAULT 0`,
  `max_tuners INTEGER NOT NULL DEFAULT 0` (migration `0003_user_limits.sql`).
- Admin user JSON (list/create/patch) gains `maxStreams`, `maxTuners`
  (0–8; 400 outside).
- `POST /api/v1/sessions` → **429** `{"error": "...", "code": "user_limit",
  "kind": "streams"|"tuners", "limit": N}`; message for viewers:
  "You're already watching on N devices — your account allows N." /
  "Your account can use N tuners at a time. Stop another channel first."
- `/api/v1/me` returns `maxStreams`/`maxTuners` (clients may show them later).

### Clients
All apps already show the server's `error` text for a failed start; no client
change is required. Admin web (Users) gets two number fields ("Streams",
"Tuners", blank = no limit).

## Testing
- Store: migration + round-trip of the new columns.
- Manager: streams limit (3rd viewer rejected at 2), tuners limit (joining a
  channel another user watches is allowed at the limit; a new channel is
  not), admin exempt, quality-change re-create under the limit.
- API: 429 body shape; admin PATCH validation; OpenAPI updated.
- Web admin model: form ↔ payload.

## Out of scope
Time-of-day limits, parental controls (separate design), per-user channel
lists, SharePlay (separate design).
