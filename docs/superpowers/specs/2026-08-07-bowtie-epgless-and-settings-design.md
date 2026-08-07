# Bowtie — v0.3.x Design: EPG-less Watching + Admin-UI Settings

**Date:** 2026-08-07
**Status:** Approved (pending implementation)
**Motivation:** First real-hardware testing (TrueNAS) surfaced two gaps: you cannot
smoke-test tuners/transcoding before EPG exists, and EPG/transcode settings require
editing container config instead of the admin UI.

## A. EPG-less watching

1. **Web guide empty states split:**
   - Zero ENABLED channels → copy: "No channels enabled yet. Add your HDHomeRun
     and enable channels in Admin → Channels." (admins get a link; viewers get
     "Ask your admin to enable some channels.")
   - Enabled channels with no programs → rows render normally; the program area
     shows one dim full-width cell: "No guide data — press to watch", clickable
     to play (same handler as the channel cell). Channel number/name cell remains
     click-to-play as today.
2. **Native/TV clients:** verified during review — NO client filters
   program-less channels; all rows stay playable. Remaining client work is copy
   alignment only: iOS/tvOS show "Nothing on now" where every other platform
   says "No guide data" — align iOS/tvOS to "No guide data".
3. **Admin channel Preview:**
   - Role bypass lives in `stream.Manager.Start` (which already receives the
     `store.User`): the `!ch.Enabled` rejection is skipped when `user.Role ==
     "admin"`. The enabled check runs BEFORE session-key join today, so viewers
     can never join an admin's disabled-channel session — reviewed and confirmed,
     no share leak.
   - 503 tuners-busy responses currently return ALL session summaries to any
     caller; with disabled-channel previews that would leak unlisted channel
     names to viewers. Change: non-admin 503 payloads include only sessions on
     ENABLED channels (admins see all).
   - Web Admin → Channels: every row gets a ▶ Preview control opening the normal
     player regardless of enabled state. This is the tuner/transcode smoke test.

## B. Runtime settings: DB-backed, admin-editable, restart-free

1. **Provider:** `server/internal/settings` package — typed accessors over the
   existing `settings` table for: `xmltv.source`, `xmltv.refreshHours`,
   `sd.username`, `sd.password`, `sd.lineupId`, `transcode.encoder`,
   `transcode.allowHevc`. Thread-safe; cheap reads (single-row lookups are fine
   at this scale — no cache invalidation cleverness).
2. **Seeding & precedence:** PRESENCE-BASED seeding only — `store` gains
   `HasSetting(key) (bool, error)` (or seed via `INSERT ... ON CONFLICT DO
   NOTHING`); a stored EMPTY STRING is a real value (e.g. "XMLTV disabled") and
   must NEVER be re-seeded. `GetSetting`'s ""-for-missing ambiguity is exactly
   why: `if GetSetting(k)=="" {seed}` is a specified BUG. On startup, seed each
   absent key from loaded config — one-way, first-boot only. Afterwards the DB
   is the sole source of truth; log a notice when a live config value differs
   from DB. Required test: set XMLTV source in config.yaml, disable it in the
   UI, restart → stays disabled. Infra keys stay env/config-only: listen addr,
   data dir, segment dir, ffmpeg path, `devices` (device-table seed, as today).
3. **Consumers go dynamic — EPG Run is REDESIGNED as an always-on supervisor**
   (the as-built loop only spawns per-source loops when configured at boot and
   never re-reads intervals — it cannot honor runtime changes):
   - One supervisor loop per source (xmltv, sd), ALWAYS running from startup.
   - Each tick: re-read the provider. Not configured → sleep a short poll
     interval (60s) WITHOUT logging errors. Configured → refresh, then sleep
     `withJitter(refreshHours)` re-read fresh each cycle (SD uses its settings
     interval the same way).
   - Clearing a source stops refreshes at the next tick — no error spam from
     empty sources.
   - `stream.Manager` reads `encoder`/`allowHevc` from the provider per session
     start. `GET /api/v1/admin/transcode` `selected` is wired to the SAME
     provider (today it reads boot-time cfg — leaving it would create a second,
     stale source of truth).
   - No restart required for any change; "Refresh now" applies new sources
     immediately.
4. **API:**
   - `GET /api/v1/admin/settings` → `{xmltv: {source, refreshHours}, schedulesDirect:
     {username, passwordConfigured, lineupId}, transcode: {encoder, allowHevc,
     available: [backends], hevcCapable: {backend: bool}}}` — SD password is NEVER
     returned.
   - `PUT /api/v1/admin/settings` is an explicit SECTION MERGE: each top-level
     section (`xmltv`, `schedulesDirect`, `transcode`) is optional (pointer
     types in Go — nil section = untouched); within a PRESENT section every
     field is required (the web UI round-trips the section it's saving from
     GET). Exception: `schedulesDirect.password` — absent or empty = keep
     existing. Clearing SD entirely: submit `schedulesDirect` with empty
     username → username, password, AND lineupId are all cleared (documented in
     the UI copy). Validation: encoder "auto" or probed-available; refreshHours
     1–168; xmltv.source empty (=disabled) or http(s) URL or absolute path.
   - `GET /api/v1/admin/epg/lineups` → requires a NEW sd client method
     `Lineups(ctx)` (GET /lineups — account lineup list; the client only has
     `Lineup(id)` today). Error taxonomy (fixed contract for the UI):
     422 `{"error":"schedules direct credentials not configured"}` when
     username/password absent; 401 `{"error":"schedules direct rejected the
     credentials"}` on auth failure; 502 `{"error":"schedules direct is
     unreachable"}` on network/5xx (never echoing secrets).
5. **Web Admin:** EPG tab grows source configuration (XMLTV url+interval; SD
   username/password/lineup with a "Load lineups" picker) above the existing
   status cards; new Transcode section (encoder dropdown from `available`, HEVC
   toggle) lives on the same Settings surface as tuners today — final layout:
   admin nav gains a **Settings** tab holding EPG sources + Transcode; the EPG
   tab keeps status/refresh. Copy in sentence case; saving shows "Saved."
6. **Docs:** TrueNAS + compose docs trim the env explanation to: data dir mount,
   device IP seed, everything else in Admin → Settings.

## Compatibility

Existing deployments: first boot after upgrade presence-seeds DB from their
current config/env (including defaults encoder=auto, refreshHours=12,
allowHevc=false) — zero behavior change until they edit the UI. Release notes
MUST state: `BOWTIE_ENCODER` and yaml EPG/transcode keys become one-shot seeds,
not live overrides; Admin → Settings is the control plane. README/compose/
TrueNAS docs updated in the same change (their "env wins" language becomes
load-bearing-wrong otherwise). OpenAPI gains the new routes and the
createSession disabled-channel-for-admin semantics (TestOpenAPICoversRoutes
enforces).

## Testing

- settings provider: seed-when-absent, DB-wins-after-seed, typed round-trips.
- API: password write-only semantics; encoder validation against probe; lineups
  endpoint success/no-creds/bad-creds.
- epg.Service: source change between cycles takes effect without restart
  (injectable clock test).
- stream.Manager: encoder setting change affects next session (existing stub
  runner tests extended).
- Web: empty-state split logic; preview button routing (vitest on pure logic).
- E2E (fake HDHR): admin previews a DISABLED channel end-to-end; viewer gets 404
  for the same channel.

## Out of scope

Per-user channel lists, multi-server, editing infra keys (ports/paths) from the
UI, quality-ladder editing (profiles stay code defaults).

## Review history

- 2026-08-07: Initial draft (Claude).
- 2026-08-07: Grok review — 8 findings, all incorporated: EPG Run redesigned as
  always-on supervisor (blocker — as-built loops can't hot-start sources);
  presence-based seeding with HasSetting (blocker — ""-ambiguity would fight UI
  clears); PUT semantics pinned to section-merge with password-keep + clear-via-
  empty-username; /admin/transcode.selected wired to provider (second-truth
  trap); preview share-leak reviewed-safe + 503 payload filtered for non-admins;
  SD Lineups() method + error table; client work shrunk to copy alignment
  (review verified no client filters program-less channels); release-notes/docs
  obligations made explicit.
