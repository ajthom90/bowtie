# Parental controls — Design (DRAFT — awaiting decisions)

Date: 2026-10-03. Requested: a family household wants a kid's account limited
to some channels, kept off programs above a rating, and optionally limited to
viewing hours.

## Goal

An admin can restrict a **viewer** account to an allowlist of channels, a
maximum rating (e.g. TV-PG), and optionally a daily viewing window. The
server enforces this when the user lists channels, loads the guide, starts a
stream, and while the stream is playing (a program that changes into a
blocked rating stops). Every app shows "Blocked by parental controls" with
the reason, and an optional household PIN unlocks the current program.
Admins are never restricted.

## What exists today

- Users: `store/users.go` (`role` admin|viewer, `max_quality`); admin CRUD in
  `api/admin_handlers.go` (`PATCH /admin/users/{id}` takes `role`,
  `maxQuality`, `password`); JWT carries only `sub`, `username`, `role`
  (`auth/tokens.go`). Admin UI is web-only: `web/src/admin/Users.tsx`.
- Schema: `store/migrations/0001_init.sql`, `0002_*.sql` (embedded, applied
  in filename order). No foreign keys in use (`refresh_tokens.user_id` has none).
- **No ratings anywhere.** `programs` has no rating column; `xmltv.Programme`
  doesn't decode `<rating>`; `sd.ProgramDetail` decodes only titles,
  descriptions, episode title, genres; `epg/xmltv/testdata/guide.xml` has
  no `<rating>` elements. EPG-less installs and unmapped channels have no
  programs at all.
- Channel list `GET /channels` and `GET /guide` are user-agnostic
  (`Store.ListChannels(true)`, `epg.Service.Guide`).
- Streams: `POST /sessions` → `stream.Manager.Start`; sessions are shared per
  (channel, decision) key and hold several viewers. `stream.Viewer` has
  `Username` only. `StopViewer`/idle reap delete the viewer, after which
  heartbeat and playlist return 404 "viewer not found". DVR window is 15 min
  (`streaming.bufferMinutes`); segments carry no `PROGRAM-DATE-TIME`.
- Start errors (`writeStartError`): 503 tuners-busy with `sessions` filtered
  to enabled channels for non-admins, 404, 422, 502, 500. All clients map
  unknown statuses to a generic error showing the body's `error` string
  (`BowtieClient.swift` default case, Android `mapHttpError` else-branch,
  Roku `mapError`, web `ApiError.message`). Web heartbeats swallow errors
  (`Player.tsx` `.catch(() => {})`).
- No timezone setting exists (`settings/settings.go`).

## Design

### 1. Ratings in the guide
- Migration `0003_parental_controls.sql`: `ALTER TABLE programs ADD COLUMN
  rating TEXT NOT NULL DEFAULT ''` (display code as received, e.g. `TV-14`, `PG-13`).
- XMLTV: decode every `<rating system="…"><value>…</value></rating>`; pick
  US TV first (`VCHIP`, `USA Parental Rating`, or any value matching `TV-*`),
  then `MPAA`, else empty.
- Schedules Direct: add `ContentRating []struct{Body, Code, Country string}`
  (`contentRating[]`) to `ProgramDetail`; prefer `body == "USA Parental
  Rating"`, then MPAA. **Verify the field names against a live SD response
  in the plan's first task — there is no SD fixture with ratings in the repo.**
- One Go table `parental.Level(code) int` (not stored) maps codes onto one
  ladder: TV-Y=1, TV-Y7/TV-Y7-FV=2, TV-G/G=3, TV-PG/PG=4, TV-14/PG-13=5,
  TV-MA/R/NC-17=6; anything else (or empty) = 0 "unrated". Content
  descriptors (V, L, S, D) are ignored.
- `GuideProgram` gains `rating` (all users).

### 2. Data model (same migration)
```sql
CREATE TABLE parental_controls (
    user_id       INTEGER PRIMARY KEY,
    channel_mode  TEXT NOT NULL DEFAULT 'all',   -- 'all' | 'allowlist'
    max_rating    TEXT NOT NULL DEFAULT '',      -- '' = no rating limit
    block_unrated INTEGER NOT NULL DEFAULT 0,
    hours_start   TEXT NOT NULL DEFAULT '',      -- 'HH:MM' local, '' = no window
    hours_end     TEXT NOT NULL DEFAULT ''
);
CREATE TABLE parental_allowed_channels (
    user_id INTEGER NOT NULL, channel_id INTEGER NOT NULL,
    PRIMARY KEY (user_id, channel_id)
);
```
A row in `parental_controls` = the user is restricted. Settings keys:
`parental.pinHash` (Argon2id via `auth.HashPassword`) and
`household.timezone` (IANA name; seeded from `TZ`, else UTC until set).
`DeleteUser` deletes both tables' rows; `SyncLineup` deletes allowlist rows
for removed channels; channels added later are **not** allowed in allowlist
mode until an admin ticks them.

### 3. Policy evaluation (new package `server/internal/parental`)
`Evaluate(policy, channel, program *store.Program, now) Decision` returns
`{Allowed, Reason, Until}` with reason `channel` | `rating` | `unrated` |
`hours`. Pure function; the caller loads the policy (one query, no cache, so
admin edits apply on the next request) and the current program via
`ProgramsInRange([epgID], now, now+1s)`. Unmapped channels / no EPG ⇒
`program == nil` ⇒ treated as unrated. Active PIN grants (§6) are checked first.

### 4. Enforcement points
| Point | Restricted user behavior |
|---|---|
| `GET /channels` | channels outside the allowlist omitted |
| `GET /guide` | same filter in `handleGuide` (`Guide()` stays user-agnostic); blocked programs get `"blocked": true` and `description: ""` |
| `POST /sessions` | evaluate before `Streams.Start`; blocked ⇒ 403 (below). 503 `sessions` additionally filtered to allowed channels |
| Playing | enforcer evicts the viewer (below) |
| Playlist rewrite | drops segments whose mtime falls in a program blocked for this user (closes DVR rewind into a blocked show on a shared session) |

403 body (also returned by heartbeat/playlist/segment after eviction):
```json
{"error": "Blocked by parental controls", "code": "parental_controls",
 "reason": "rating", "rating": "TV-MA", "program": "Late Night Movie",
 "until": "2026-10-03T23:00:00Z", "pinAvailable": true}
```

**Mid-stream.** `stream.Viewer` gains `UserID` (and `ViewerInfo.userId`). A
`parental.Enforcer` goroutine ticks every 30 s and immediately after any
parental-controls admin write: for each viewer whose user is restricted it
evaluates (channel, now); blocked ⇒ `Manager.Evict(viewerID, body)`, which
removes the viewer like `StopViewer` and keeps a 10-minute tombstone so
heartbeat/playlist/segment return the 403 body instead of 404. Evict never
calls `Terminate`: other viewers keep the shared session; an emptied session
uses the normal empty-grace teardown. Worst-case lag after a program
boundary: 30 s tick + one heartbeat (15 s) — EPG times are themselves only
accurate to a minute or two.

### 5. API changes (all in `docs/api/openapi.yaml`; `openapi_test.go` enforces route coverage)
- `PUT /api/v1/admin/users/{id}/parental` (admin) body
  `{"channelMode":"allowlist","channelIds":[3,7],"maxRating":"TV-PG","blockUnrated":false,"hoursStart":"07:00","hoursEnd":"20:00"}`
  → 200 same shape; 400 if the user is an admin or a value is invalid.
  `DELETE …/parental` → 204 (unrestricted). Promoting a restricted user to
  admin deletes the row.
- `User` schema gains `parental` (the object above, or `null`); `/me` returns it
  too so apps can show a "Restricted" badge.
- `PUT /api/v1/admin/parental/pin` `{"pin":"1234"}` (4–8 digits) / `DELETE` → 204.
- `POST /api/v1/parental/unlock` (user) `{"pin":"1234","channelId":7}` → 204
  or 403 `{"error":"wrong PIN"}`; 5 failures per user per 10 min ⇒ 429.
- `GuideProgram`: `rating`, `blocked` (omitted for unrestricted users).
- New schema `ParentalBlockError` for the 403 on `POST /sessions`, heartbeat, playlist, segment.

### 6. PIN override
A correct PIN creates an in-memory grant (user, channel) that expires at the
current program's `stop` (or +1 h when unrated/`hours`). Grants are lost on
restart (acceptable: re-enter PIN). Channel-allowlist blocks are not
PIN-unlockable (the channel isn't listed anyway).

### 7. Admin UI (web only, `web/src/admin/Users.tsx`)
Viewer rows get a "Parental controls" toggle opening a panel: Channels
(All / Only these → checklist of enabled channels), Max rating select (No
limit, TV-Y, TV-Y7, TV-G, TV-PG, TV-14), Block unrated checkbox, Allowed
hours (from/to, shown in the household timezone). `Settings.tsx` gains
"Parental PIN" (set/clear) and Timezone. `Sessions.tsx` shows a lock icon
next to restricted viewers.

### 8. Apps
| App | Start blocked (403 `code`) | Mid-stream (heartbeat/playlist 403) | Guide |
|---|---|---|---|
| Web | blocked panel in `Player.tsx` with reason, `until`, "Enter PIN" | heartbeat stops swallowing 403 → destroy hls, show panel | rating chip; lock on blocked cells |
| iOS / iPadOS / tvOS | `BowtieError.parentalBlock(ParentalBlock)`; `PlayerModel.State.blocked` | heartbeat task treats 403 as terminal | rating chip + lock |
| Android / Android TV | `BowtieError.ParentalBlock`; `PlayerViewModel.State.Blocked` | same in heartbeat loop | rating chip + lock |
| Roku | `kind: "parentalBlock"` in `BowtieClient.bs`; `blockedGroup` in `PlayerScene` modeled on `tunersGroup`; PIN via `PinDialog` | heartbeat 403 → stop video, show group | rating text |

The playlist poll (~4 s) usually hits the 403 before the heartbeat does, and
AVPlayer, Media3 and Roku's Video node report it only as an opaque playback
failure. Client rule: **on any playback error or heartbeat 403, call heartbeat
once and render the parental body if that is what comes back** (web may also
read hls.js `data.response`). Old builds degrade through the same path: the
playback error's retry re-POSTs `/sessions`, gets the 403, and their generic
error UI shows the `error` string.

## Testing
- Unit: rating ladder/normalization table; XMLTV `<rating>` parse with an
  updated `guide.xml` fixture (VCHIP, MPAA, none); SD `contentRating` decode;
  `Evaluate()` table test (channel/rating/unrated/hours incl. a window that
  crosses midnight and DST, PIN grant precedence).
- API: channel/guide filtering per user; `POST /sessions` 403 body; 503 list
  filtering; admin PUT rejects admins; PIN rate limit; new routes in openapi.
- Manager: `Evict` → heartbeat/playlist/segment return 403 tombstone, then
  404 after TTL; shared session keeps its other viewer.
- Enforcer: fake clock + programs crossing a TV-PG→TV-MA boundary evicts
  within one tick; DVR rewrite drops blocked-interval segments.
- Apps: decoding tests for the 403 body in BowtieKit, Android core, Roku
  `ClientFixtures.bs`; iOS Simulator UI test plays 9.1 as a restricted user
  with max rating set below the current program's.

- Risks: rating coverage on real lineups is unknown (plan task 1 counts rated
  vs unrated programs on the production guide); segment mtime is only ±1
  segment accurate for the DVR filter.

## Out of scope
Content descriptors (V/L/S/D), ATSC PSIP content-advisory parsing for EPG-less
installs, daily time quotas, per-device restrictions, viewer self-service,
native-app admin screens, audit log of blocked attempts.

## Decisions for the user
The design above assumes the recommended answers. A "no" removes: (2) §6, the
PIN and unlock routes and `pinAvailable`; (3) the `hours_*` columns, the
timezone setting and the hours fields in §5/§7.

1. **Unrated programs (including every channel when there's no guide): allow
   or block?** OTA news and sports are routinely unrated, so blocking unrated
   blanks them. *Recommend: allow by default; per-user "Block unrated" toggle.*
2. **PIN override in v1?** Without it a parent must edit the account on
   another device and remember to revert. *Recommend: yes — one household PIN,
   unlocks the current program only, rate-limited.*
3. **Viewing hours in v1?** Needs a new household timezone setting (none
   exists) and a midnight-crossing window. *Recommend: yes, one daily
   window per user; no quotas.*
4. **Rating-blocked programs in the guide: hidden or shown locked?** Hiding
   leaves holes in the grid. *Recommend: show title and rating with a lock,
   hide the description; disallowed channels are hidden entirely.*
