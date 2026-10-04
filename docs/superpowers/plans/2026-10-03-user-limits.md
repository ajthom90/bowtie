# Per-account limits Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Admins can cap how many streams and how many tuners each viewer account may use at once.

**Architecture:** Two integer columns on `users` (0 = unlimited). `stream.Manager.Start` checks them under `m.mu` before join-or-create, counting the account's live viewers and the channels only that account is watching, plus in-flight starts (reservations) so two simultaneous starts can't both slip under the limit. A breach returns `*stream.UserLimitError`, which the API maps to 429 with `code: "user_limit"`. Apps already show the server's `error` text.

**Tech Stack:** Go (server, SQLite migrations), React/TypeScript (web admin), OpenAPI 3.

**Spec:** docs/superpowers/specs/2026-10-03-user-limits-design.md

## Global Constraints

- Limits are per user; `0` = unlimited; valid range 0–8 (400 outside).
- Admin accounts are never limited (fields ignored for role `admin`).
- Joining a channel another account is already watching costs no tuner.
- 429 body: `{"error": <message>, "code": "user_limit", "kind": "streams"|"tuners", "limit": N}`.
- Messages: streams — "You're already watching on N device(s) — your account allows N."; tuners — "Your account can use N tuner(s) at a time. Stop another channel first."
- Migration file: `server/internal/store/migrations/0003_user_limits.sql`.

## Review Focus

1. **Force-quit app** (no DELETE): its viewer lives up to 90 s; at a 1-stream limit the reopened app must not be blocked — viewers not seen for 30 s don't count.
2. **Channel zap / quality change** at the limit: clients DELETE first, so the new start must succeed.
3. **Concurrent starts** by one account (two devices at once) must not both pass a limit of 1 — reservations count.
4. **Same channel, switch off** (several per-profile sessions of one channel): one tuner, not one per session.
5. **Failed start** must release its reservation (a failed start never consumes a slot).

---

### Task 1: Store columns

**Files:**
- Create: `server/internal/store/migrations/0003_user_limits.sql`
- Modify: `server/internal/store/users.go`
- Test: `server/internal/store/store_test.go`

**Interfaces:**
- Produces: `store.User{..., MaxStreams int, MaxTuners int}`; Create/Update/scan round-trip both.

- [ ] Step 1: failing test `TestUserLimitsRoundTrip` (create with 2/1, read back; UpdateUser to 0/3, read back).
- [ ] Step 2: run → FAIL (unknown fields).
- [ ] Step 3: migration (`ALTER TABLE users ADD COLUMN max_streams INTEGER NOT NULL DEFAULT 0;` and `max_tuners`), struct fields, SQL.
- [ ] Step 4: `go test ./internal/store` → PASS. Commit.

### Task 2: Enforcement in the stream manager

**Files:**
- Modify: `server/internal/stream/manager.go`, `server/internal/stream/session.go`
- Create: `server/internal/stream/limits.go`, `server/internal/stream/manager_limits_test.go`

**Interfaces:**
- Consumes: `store.User.MaxStreams/MaxTuners/ID/Role`.
- Produces: `type UserLimitError struct{ Kind string; Limit int }` (`Error()` returns the viewer message); `Viewer.UserID int64`; `const limitStaleAfter = 30 * time.Second`.

Rules (in `limits.go`, called from `Start` after negotiation, before the attempt loop):
- `reserveLocked(user, channelID) (release func(), error)`: skip entirely for admins or when both limits are 0.
- Live viewers of the user = viewers with `UserID == user.ID` and `now - LastSeen < limitStaleAfter`; plus pending reservations of the user.
- streams: `live + pending >= MaxStreams` → `&UserLimitError{"streams", MaxStreams}`.
- tuners: channels C where the user has a live viewer/reservation and no other user has a live viewer on C (any session of C). Starting on channel X costs a tuner iff no other user is watching X and the user isn't already on X. If it costs one and `soleChannels >= MaxTuners` → `&UserLimitError{"tuners", MaxTuners}`.
- Reservation is released when `Start` returns (success: the viewer now counts itself; failure: slot freed).

Tests (VideoToolbox-free: use existing `setupEnv`/`newTestManagerWithDial` helpers):
- `TestStreamLimitRejectsThirdViewer` (limit 2: two starts ok, third → UserLimitError streams/2).
- `TestTunerLimitAllowsJoiningOthersChannel` (limit 1: user B watching ch2; A on ch1 ok; A on ch2 ok — shared; A on ch3 → tuners/1).
- `TestTunerLimitCountsChannelOnceAcrossSessions` (limit 1, streams 0: A watches ch1 at two qualities → both ok).
- `TestAdminIgnoresLimits`.
- `TestStaleViewerDoesNotCount` (limit 1: start, advance clock 31 s, start again ok).
- `TestStopFreesSlot` (limit 1: start, Stop viewer, start ok).
- `TestFailedStartReleasesReservation` (limit 1: runner fails start → error; next start ok).
- `TestConcurrentStartsRespectLimit` (limit 1: two goroutines Start at once → exactly one succeeds).

- [ ] Steps: write tests → RED → implement → GREEN → full `go test ./...` → commit.

### Task 3: API, OpenAPI

**Files:**
- Modify: `server/internal/api/auth_handlers.go` (userJSON gains `maxStreams`, `maxTuners`), `admin_handlers.go` (create/patch accept and validate 0–8), `stream_handlers.go` (`writeStartError`: `errors.As(*stream.UserLimitError)` → 429 body), `docs/api/openapi.yaml`.
- Test: `admin_users_test.go`, `stream_handlers_test.go`.

Tests: `TestAdminUserLimitsCreatePatch` (create 2/1 → JSON echoes; patch maxTuners 3; patch 9 → 400; -1 → 400), `TestCreateSessionUserLimit429` (viewer limit 1, second start → 429 body code/kind/limit/error). OpenAPI test stays green with fields documented.

- [ ] Steps: RED → implement → GREEN → `go test ./... && golangci-lint run` → commit.

### Task 4: Web admin

**Files:**
- Modify: `web/src/api/client.ts` (User, CreateUserRequest, PatchUserRequest gain `maxStreams`, `maxTuners`), `web/src/admin/adminModel.ts` (+ `LIMIT_OPTIONS`, `limitLabel`), `web/src/admin/Users.tsx` (Streams/Tuners selects in create form and table), `web/src/admin/adminModel.test.ts`.

Tests: `limitLabel(0) === 'No limit'`, `limitLabel(1) === '1'`, `LIMIT_OPTIONS` = 0..8.

- [ ] Steps: RED → implement → GREEN → `npm test && npm run lint && npm run build` → commit.

### Task 5: Docs and real check

- CHANGELOG Unreleased entry; README admin mention if users are described there.
- Real check on the Mac test server: viewer with Streams 1 — second browser tab start shows the 429 message; zap within the tab works.
