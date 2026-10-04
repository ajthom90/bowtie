# Favorites and Recents — Design

Date: 2026-10-03. Requested: star channels and see recently watched ones in
every app, with phone, TV and web agreeing.

## Goal

A signed-in viewer can star a channel on any client; starred channels sit at
the top of the guide / channel list on web, iOS/iPadOS, Apple TV, Android,
Android TV/Fire TV and Roku. A "Recent" row shows the last few channels that
user actually watched. Both follow the **user**, not the device.

## What exists today

- **Users and auth.** `users` table (`store/migrations/0001_init.sql`),
  `store.User{ID, Username, Role, MaxQuality}` (`store/users.go`); JWT
  `auth.Claims{UserID, Username, Role}` (`auth/tokens.go`), reachable in any
  `auth.RequireUser` handler via `auth.ClaimsFrom`. Per-user server state is a
  natural fit: every viewer request already carries a user id.
- **Store.** SQLite, one connection, `PRAGMA foreign_keys = ON` set once in
  `store.Open` (`store/store.go:33`). Embedded, ordered migrations
  (`0001_init.sql`, `0002_device_stream_port.sql`). No table declares a
  foreign key; `DeleteUser` (`store/users.go`) deletes only the user row
  (it leaves `refresh_tokens` orphaned too).
- **Channel ids are stable.** `SyncLineup` (`store/channels.go`) keeps a row's
  `id` across rescans for an existing `(device_id, guide_number)` and deletes
  rows that left the lineup. `DeleteDevice` (`store/devices.go`) leaves its
  channel rows in place.
- **Viewer reads.** `GET /api/v1/channels` → `handleListChannels` /
  `viewerChannelJSON` (`api/admin_handlers.go:508`); `GET /api/v1/guide` →
  `handleGuide` (`api/guide_handlers.go`) returning `epg.GuideChannel`
  (`epg/service.go:86`). Both already annotate a per-request field
  (`reception`) in the API layer — the precedent for `favorite`.
- **Sessions are not persisted.** `handleCreateSession`
  (`api/stream_handlers.go`) writes nothing to the store; `stream.Manager` is
  in-memory and `SessionInfo.StartedAt` (`stream/session.go`) only exists
  while a session is live. Recents therefore need a new table.
- `Manager.Touch` (`stream/manager.go:379`) runs on every heartbeat (15 s on
  iOS/Android/web) **and** every playlist fetch, so it sees all clients
  including Roku.
- `api/openapi_test.go` fails if a registered route is missing from
  `docs/api/openapi.yaml`.

## Design

### 1. Data model — `store/migrations/0004_favorites_recents.sql`
```sql
-- Per-user favorites and recently watched channels (Favorites/Recents).
CREATE TABLE IF NOT EXISTS user_favorites (
    user_id INTEGER NOT NULL, channel_id INTEGER NOT NULL,
    created_at TEXT NOT NULL, PRIMARY KEY (user_id, channel_id));
CREATE TABLE IF NOT EXISTS user_recents (
    user_id INTEGER NOT NULL, channel_id INTEGER NOT NULL,
    watched_at TEXT NOT NULL, PRIMARY KEY (user_id, channel_id));
CREATE INDEX IF NOT EXISTS idx_user_recents_user_watched
    ON user_recents (user_id, watched_at);
```
Keyed on `channels.id` because `SyncLineup` keeps it stable. No FKs (repo
convention; a reconnect would silently drop the pragma): cleanup is explicit —
`DeleteUser` deletes the user's rows in the same transaction, `SyncLineup`
deletes rows for channels it removes. Reads `JOIN channels … WHERE enabled = 1`,
so disabling a channel hides a favorite without losing it.
New file `store/favorites.go`: `SetFavorite(userID, channelID, on bool)`,
`FavoriteIDs(userID) (map[int64]bool, error)`, `RecordWatch(userID, channelID,
at)` (upsert `watched_at`, then trim to the newest 20 in one tx),
`Recents(userID, limit)`, `ClearRecents(userID)`.

### 2. Recording a watch
`stream.Viewer` (already has `UserID` since 0.9.0) gains `JoinedAt time.Time` (`Start` already
receives `store.User`) plus `recorded bool`. `ManagerDeps` gains
`OnWatched func(userID, channelID int64, at time.Time)` (nil = no-op, so the
e2e harness and existing tests are unchanged). In `Touch`, when
`now - JoinedAt >= 30s` and `!recorded`, set `recorded` and call `OnWatched`
after releasing `m.mu`. `server/cmd/bowtie/main.go:130` wires it to `store.RecordWatch`. A 400 ms Roku
zap or an Android TV DPAD zap never reaches 30 s, so it is not recorded.

### 3. API (all `auth.RequireUser`)
- `GET /api/v1/channels`, `GET /api/v1/guide`: each item gains
  `"favorite": true|false` for the caller. Server order is unchanged; clients
  sort. `/channels` item: `{"id": 7, "guideNumber": "9.1", "name": "FOX9",
  "logoUrl": "", "reception": "ok", "favorite": true}` (guide items add it
  beside `channelId`).
- `PUT /api/v1/me/favorites/{channelId}` → `204`; idempotent; `404
  {"error":"channel not found"}` if unknown or disabled.
- `DELETE /api/v1/me/favorites/{channelId}` → `204` (idempotent).
- `GET /api/v1/me/recents?limit=8` (default 8, max 20) → `200`, newest first,
  enabled channels only:
  ```json
  [{"channelId": 7, "guideNumber": "9.1", "name": "FOX9",
    "logoUrl": "", "watchedAt": "2026-10-03T19:42:10Z"}]
  ```
- `DELETE /api/v1/me/recents` → `204` (clear history).

`openapi.yaml`: `favorite` (required boolean) on `ViewerChannel` and
`GuideChannel`; new `RecentChannel` schema; three new paths under tag `me`.

**Compatibility.** Android decodes with `ignoreUnknownKeys = true`
(`core/Models.kt`). New fields are optional-with-default in clients
(`favorite: Bool?` in `BowtieKit/Models.swift`, like `reception`); a missing
`favorite` means an older server → hide the star and the Recent row.

### 4. Clients
Toggles are optimistic (flip locally, PUT/DELETE, revert + toast on error).
No push: other devices pick changes up on their existing refresh (iOS/tvOS
`refreshIfStale` 5 min / foreground; Android `refreshIfStale`; Roku
`refreshTimer` 300 s; web on guide reload).

| Client | Files | Star / unstar | Favorites | Recent |
|---|---|---|---|---|
| Web | `web/src/guide/Guide.tsx` (`ChannelRow`), `guideModel.ts`, `api/client.ts` | ☆/★ button in the channel cell (`aria-pressed`) | rows sorted favorites-first | chip strip above the grid |
| iOS / iPadOS | `ios/App/iOS/ChannelListView.swift`; `BowtieKit/ChannelListModel.swift`, `BowtieClient.swift`, `Models.swift` | leading swipe action + context menu | `Section("Favorites")` then `Section("Channels")` | horizontal chip row at top |
| Apple TV | `ios/App/tvOS/ChannelRailView.swift` | `.contextMenu` on the row `Button` (click-and-hold Select) | star glyph; favorites first in rail | card row in its own `.focusSection()` above rail |
| Android phone | `android/app/.../ui/ChannelListScreen.kt`; `core/vm/ChannelListViewModel.kt`, `BowtieClient.kt`, `Models.kt` | trailing star `IconButton`; long-press | sticky header "Favorites" | `LazyRow` at top |
| Android TV / Fire TV | `android/tv/.../ui/ChannelRailScreen.kt` | long-press DPAD_CENTER, or `KEYCODE_MENU` (Fire TV ☰) | favorites first; zap (`PlayerKeyHandler`) follows rail order | `LazyRow` above rail |
| Roku | `roku/components/HomeScene.bs/.xml`, `ChannelRailItem.xml`, `tasks/ApiTask.xml` | `*` (options) on focused rail item — add `onKeyEvent` to HomeScene (none today) | favorites first; `PlayerScene` zap follows passed rail | second `MarkupList` `recentList`; explicit up/down focus handoff |

Shared models (`ChannelListModel`, `ChannelListViewModel`) own the sort:
favorites in guide-number order, then the rest. Recent row hidden when empty.

## Testing
- Store (`store_test.go`): toggle idempotency; recents upsert, order, trim to
  20; `DeleteUser` and a `SyncLineup` removal clear dependent rows; disabled
  channels filtered from reads.
- API: user A's star is invisible to user B on `/channels` and `/guide`; PUT
  on unknown/disabled channel → 404; recents order/limit; `openapi_test` passes.
- Manager (`manager_test.go`, injected `now`): `OnWatched` fires once at
  ≥ 30 s, never for a viewer removed at 10 s, and not under `m.mu`.
- Clients: `guideModel.test.ts` sort; BowtieKit `ChannelListModel` tests;
  Android `ChannelListViewModel` tests; Roku via `SelfTestScene`.
- Real: star on web → iOS Simulator relaunch shows it first (UI test); watch
  9.1 for 40 s on Android TV emulator → appears in web Recent row.

## Out of scope
Manual favorite reordering; favorites on the tvOS Top Shelf; admin view of
other users' favorites/recents; push/live sync; per-device profiles; cleaning
up channel rows orphaned by `DeleteDevice` (existing behavior).

## Decisions (taken 2026-10-03 under the user's "go with your recommendations")
1. **Float to top vs a Favorites filter.** Recommend: favorites float to the
   top (no duplication, no filter toggle). Consequence: on Android TV and Roku,
   up/down zap order follows the list, so zapping cycles favorites first. A
   filter adds a focus target on every TV screen for little gain.
2. **When does a channel become "recent"?** Recommend: after 30 s of watching,
   not on tune — otherwise zapping fills the row with channels you skipped.
3. **Shared household logins.** Per-user storage means a family that shares
   one viewer account shares one set of favorites and recents. Recommend:
   accept it; separate people should use separate viewer accounts (admins
   can already create them). Per-device profiles stay out of scope.
