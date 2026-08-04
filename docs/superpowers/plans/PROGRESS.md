# Bowtie Phase 1 progress

## Task 1

**Date:** 2026-08-04

### Built

- Apache-2.0 `LICENSE` (copyright Bowtie contributors)
- Brief `README.md` (under construction)
- Go module `github.com/ajthom90/bowtie/server` (Go 1.22), sole dep `gopkg.in/yaml.v3`
- `server/internal/config`: `Config` struct + `Load` (yaml then env overrides; defaults `:8400`, segments under dataDir, `ffmpeg`, encoder `auto`)
- `server/cmd/bowtie/main.go`: `--data-dir` / `BOWTIE_DATA_DIR`, mkdir data+segments, `GET /healthz` → 200 `ok`
- `Makefile`: `build`, `test`, `lint`, `dev` (web steps stub until Task 17)
- `.github/workflows/ci.yml`: `server` job (setup-go, golangci-lint, `go test`); `# web job added in Task 17`
- `docs/api/openapi.yaml` skeleton (title Bowtie API, version 0.1.0, empty paths)
- `.gitignore`: data/, dist paths, web node_modules, local Claude settings
- Step 6 (`gh repo create` / push) skipped — handled by orchestrator

### Notes

- `golangci-lint` is **not installed locally**; CI runs it via `golangci/golangci-lint-action@v6`.

### Verification (evidence)

```
$ cd server && go vet ./...
PASS

$ go test ./...
?   	github.com/ajthom90/bowtie/server/cmd/bowtie	[no test files]
ok  	github.com/ajthom90/bowtie/server/internal/config	(cached)

$ go build -o /tmp/bowtie-verify ./cmd/bowtie
PASS

$ /tmp/bowtie-verify --data-dir "$(mktemp -d)" &
# log: bowtie 0.1.0-dev listening on :8400 (data=/var/folders/.../T/tmp....)
$ curl -sf localhost:8400/healthz
ok

$ make build
web not yet scaffolded
cd server && go build -o ../dist/bowtie ./cmd/bowtie
# dist/bowtie runs; healthz=ok
```

## Task 2

**Date:** 2026-08-04

### Built

- `server/internal/store` package: `Open`/`Close` with embedded SQL migrations (`go:embed migrations/*.sql`), applied in filename order, tracked in `schema_migrations`
- Migration `migrations/0001_init.sql`: tables `users`, `devices`, `channels`, `epg_channels`, `programs`, `refresh_tokens`, `settings`, `schema_migrations`; unique `(device_id, guide_number)` on channels; index `programs(epg_channel_id, start)`; unique `refresh_tokens(token_hash)`
- Domain methods per plan Interfaces: users CRUD + CountUsers; devices Upsert/List/Delete; SyncLineup (preserves Enabled + EPGChannelID); EPG Replace/List/ProgramsInRange/Prune; refresh tokens; settings Get/Set
- Times stored as RFC3339 UTC strings
- Driver: `modernc.org/sqlite` v1.34.5 (pure Go, `CGO_ENABLED=0`); Go module remains `go 1.22`

### Notes

- Pinned `modernc.org/sqlite` to v1.34.5 (go 1.21) rather than `@latest` (v1.56 requires go 1.25) so the module stays on Go 1.22 as specified in Global Constraints.

### Verification (evidence)

```
$ cd server && CGO_ENABLED=0 go vet ./...
# (no output — pass)

$ CGO_ENABLED=0 go test ./... -count=1
?   	github.com/ajthom90/bowtie/server/cmd/bowtie	[no test files]
ok  	github.com/ajthom90/bowtie/server/internal/config	0.074s
ok  	github.com/ajthom90/bowtie/server/internal/store	0.152s
```

## Task 3

**Date:** 2026-08-04

### Built

- `server/internal/auth/password.go`: `HashPassword` / `VerifyPassword` — Argon2id PHC (`t=3`, `m=65536` KiB, `p=2`, salt 16B, key 32B), constant-time compare
- `server/internal/auth/tokens.go`: `Auth`, `Claims`, `NewAccessToken` / `ParseAccessToken` (HS256, 15m, `sub`+username+role; parse honors fixed `now` via `jwt.WithTimeFunc`), `NewRefreshToken` / `Rotate` / `Revoke` (raw base64url 32B, SHA-256 hex stored, 30d expiry)
- Tests: `password_test.go`, `tokens_test.go` (all six plan cases)
- `cmd/bowtie/main.go`: opens `<dataDir>/bowtie.db`; settings key `jwt_secret` (32 random bytes hex on first run); if `CountUsers()==0`, creates `admin` with 16-char hex password printed once via the plan's log line
- Deps: `golang.org/x/crypto v0.31.0`, `github.com/golang-jwt/jwt/v5 v5.2.1` (pinned; Go module stays `go 1.22`)

### Notes / deltas

- DB path for bootstrap: `filepath.Join(dataDir, "bowtie.db")` (not named in Task 3 Interfaces; chosen as conventional and consistent with Open(path) API).
- `auth.Auth` is not yet attached to HTTP handlers (Task 4); JWT secret is only persisted in settings for now.
- Avoided `@latest` for `x/crypto` (would force Go ≥ 1.25).

### Verification (evidence)

```
$ cd server && CGO_ENABLED=0 go vet ./...
# (no output — pass)

$ CGO_ENABLED=0 go test ./... -count=1
?   	github.com/ajthom90/bowtie/server/cmd/bowtie	[no test files]
ok  	github.com/ajthom90/bowtie/server/internal/auth	0.412s
ok  	github.com/ajthom90/bowtie/server/internal/config	0.080s
ok  	github.com/ajthom90/bowtie/server/internal/store	0.151s

# smoke: first run prints admin password once; second start does not
$ bowtie --data-dir "$TMP"
# first run: created admin user "admin" with password "…" — change it after login
# bowtie 0.1.0-dev listening on :8400 …
$ curl -sf localhost:8400/healthz → ok
```

## Task 4

**Date:** 2026-08-04

### Built

- `server/internal/api/server.go`: `Deps{Cfg, Store, Auth}`, `New(deps) http.Handler` — stdlib `http.ServeMux` with Go 1.22 method patterns; no router dependency
- `server/internal/api/auth_handlers.go`: `POST /api/v1/auth/login|refresh|logout`, `GET /api/v1/me`, `POST /api/v1/me/password` — camelCase JSON, errors `{"error":"..."}`
- `server/internal/auth/middleware.go`: `RequireUser`, `RequireAdmin`, `ClaimsFrom(ctx)` — Bearer JWT → context Claims
- `cmd/bowtie/main.go`: wires `auth.Auth` + `api.New`; keeps `GET /healthz` on root mux, mounts API at `/`
- `docs/api/openapi.yaml`: paths + schemas for login, refresh, logout, me, me/password
- Tests: `api/auth_handlers_test.go` (login/me, bad password, refresh rotation, me 401, password change, logout), `auth/middleware_test.go` (`TestAdminRouteForbiddenForViewer`)

### Notes / deltas

- Plan/reality: store and auth APIs matched the plan (no interface changes needed).
- Deps holds only Cfg/Store/Auth as required for this stage; later tasks add Tuners/EPG/Probe/Streams.
- Password change does **not** revoke existing refresh tokens (per plan: “old refresh flow still valid”).
- Middleware test issues JWTs at `time.Now()` because `RequireUser` validates with wall clock (unlike unit token tests that inject fixed `now`).

### Verification (evidence)

```
$ cd server && CGO_ENABLED=0 go vet ./...
# (no output — pass)

$ CGO_ENABLED=0 go test ./... -count=1
?   	github.com/ajthom90/bowtie/server/cmd/bowtie	[no test files]
ok  	github.com/ajthom90/bowtie/server/internal/api	1.191s
ok  	github.com/ajthom90/bowtie/server/internal/auth	0.445s
ok  	github.com/ajthom90/bowtie/server/internal/config	0.065s
ok  	github.com/ajthom90/bowtie/server/internal/store	0.119s
```

## Task 11 (worktree track/transcode)
2026-08-04 — Encoder probe: `transcode.Probe` with fake-ffmpeg-script tests + `ffmpeg`-tagged real test.
Evidence: full suite green; tagged run on Apple Silicon detected videotoolbox with h264+hevc
(`probe_ffmpeg_test.go:51: selected backend: videotoolbox`). API wiring (GET /api/v1/admin/transcode)
deferred to main line — wire it in Task 14/16.

## Task 12 (worktree track/transcode)
2026-08-04 — Profiles + negotiation: `Profile`, `Negotiate`, `SessionKey` with 12-case table tests
covering caps/user-cap/MaxHeight clamping, HEVC fallback, forced-backend errors. Suite green.

## Task 13 (worktree track/transcode)
2026-08-04 — FFmpeg command builder: golden-args tests for all five backends + audio copy/aac.
Tagged e2e transcoded generated MPEG-2/AC-3 → HLS on this machine via libx264 AND h264_videotoolbox;
ffprobe confirmed h264+aac segments (2 segs each). Deviation: e2e generates its own input instead of
using hdhrfake (branch isolation); Task 14's session tests still cover hdhrfake integration.

## Task 5

**Date:** 2026-08-04

### Built

- `server/internal/api/admin_handlers.go`: admin user CRUD
  - `GET /api/v1/admin/users` — list users (no password hashes)
  - `POST /api/v1/admin/users` — create `{username,password,role,maxQuality}` → 201
  - `PATCH /api/v1/admin/users/{id}` — optional `role`, `maxQuality`, `password`
  - `DELETE /api/v1/admin/users/{id}` — 409 when deleting the last admin; also refuse demoting last admin on PATCH
- Routes wired in `server.go` via `auth.RequireAdmin` (viewer → 403)
- `server/internal/hdhr/hdhrfake`: in-process fake HDHomeRun
  - Own `LineupEntry` type (deliberate JSON-level coupling; real `hdhr` arrives Task 6)
  - `GET /discover.json`, `/lineup.json`, `/status.json`, `/auto/v{n}`
  - `/auto/v{n}` loops embedded `testdata/fixture.ts` at ~real-time (~376 KB/s via 50ms ticker); 503 body `all tuners in use` when `ActiveStreams() == TunerCount`
- Fixture copied (not regenerated) from scratchpad: 729,440-byte MPEG-2/AC-3 720x480 interlaced testsrc2; documented in `testdata/README.md`
- `docs/api/openapi.yaml`: admin/users paths + CreateUserRequest/PatchUserRequest + Forbidden/NotFound responses
- Tests: `api/admin_users_test.go` (CRUD, viewer 403, last-admin 409), `hdhrfake/fake_test.go` (discover/lineup/status, dual stream + 503 + ActiveStreams decrement)

### Notes / deltas

- Store/auth APIs matched the plan — no interface changes.
- Create returns **201** (plan listed endpoint shape without status; 201 is conventional for create).
- Last-admin protection also applied to demotion via PATCH (extra safety; delete 409 was the only plan requirement).
- Unique-username collision on create → 409 (string match on sqlite constraint error).

### Verification (evidence)

```
$ cd server && CGO_ENABLED=0 go vet ./...
# (no output — pass)

$ CGO_ENABLED=0 go test ./... -count=1
?   	github.com/ajthom90/bowtie/server/cmd/bowtie	[no test files]
ok  	github.com/ajthom90/bowtie/server/internal/api	2.137s
ok  	github.com/ajthom90/bowtie/server/internal/auth	0.426s
ok  	github.com/ajthom90/bowtie/server/internal/config	0.082s
ok  	github.com/ajthom90/bowtie/server/internal/hdhr/hdhrfake	0.388s
ok  	github.com/ajthom90/bowtie/server/internal/store	0.156s
ok  	github.com/ajthom90/bowtie/server/internal/transcode	0.332s
```

## Task 6

**Date:** 2026-08-04

### Built

- `server/internal/hdhr/client.go`: `DiscoverInfo`, `LineupEntry`, `TunerStatus`; `FetchDiscover` / `FetchLineup` / `FetchStatus` against device HTTP API; helpers `StreamPortFromBaseURL` (port rule), `HostFromBaseURL`, `HTTPBaseURL`, `BaseURLFromManual`
- `server/internal/hdhr/discover.go`: UDP discovery on port 65001 implementing libhdhomerun packet format (type/len BE, TLV payload, CRC LE); `Discover(ctx, timeout)` best-effort (socket failure only is fatal)
- `server/internal/hdhr/discover_live_test.go` (`//go:build hdhr_live`): real-network broadcast for manual verification — not run in CI
- `server/internal/tuner/manager.go`: `Manager` with injectable discover/fetch hooks; `Refresh` aggregates UDP + `cfg.Devices` + stored rows; unreachable devices keep stored row with `Reachable=false`; `Devices()` returns cache + live status; `StreamURL` uses stored `StreamPort`
- Store: `Device.StreamPort` + migration `0002_device_stream_port.sql` (default 5004); `DeviceByID` added for StreamURL lookup
- Tests: `hdhr/client_test.go` (against hdhrfake), `hdhr/discover_test.go` (packet round-trip + CRC reject + port rule), `tuner/manager_test.go` (`TestRefreshAggregatesManualAndStored`, `TestStreamURLPortRule`)

### Discovery packet format verification

Fetched and followed Silicondust libhdhomerun sources:
- https://raw.githubusercontent.com/Silicondust/libhdhomerun/master/hdhomerun_pkt.h — frame layout, tag IDs (`DEVICE_TYPE=0x01`, `DEVICE_ID=0x02`, `TUNER_COUNT=0x10`, `BASE_URL=0x2A`, `LINEUP_URL=0x27`), types `DISCOVER_REQ=0x0002` / `DISCOVER_RPY=0x0003`, var-length encoding
- https://raw.githubusercontent.com/Silicondust/libhdhomerun/master/hdhomerun_pkt.c — `hdhomerun_pkt_seal_frame` / `open_frame` and the custom bit-sliced CRC (`calcCRC`), little-endian CRC footer
- https://raw.githubusercontent.com/Silicondust/libhdhomerun/master/hdhomerun_discover.c — request tags (tuner type + device id), IPv4 broadcast to port 65001, reply TLV parsing

Unit test encodes a request, re-opens it, builds a reply with the same encoder, decodes it, and rejects a corrupted CRC.

### Notes / deltas

- Chose stored `StreamPort` (migration 0002) over re-parsing BaseURL at StreamURL time — matches plan's preferred option.
- `Manager.SetDiscoverFunc` used in tests to suppress real UDP broadcast in CI.
- Request packets always include both DEVICE_TYPE and DEVICE_ID tags (wildcard `0xFFFFFFFF`) for older-firmware compatibility; newer lib sometimes omits wildcard ID.
- API package untouched (Task 7 wires tuners into HTTP).

### Verification (evidence)

```
$ cd server && CGO_ENABLED=0 go vet ./...
# (no output — pass)

$ CGO_ENABLED=0 go test ./... -count=1
?   	github.com/ajthom90/bowtie/server/cmd/bowtie	[no test files]
ok  	github.com/ajthom90/bowtie/server/internal/api	2.113s
ok  	github.com/ajthom90/bowtie/server/internal/auth	0.508s
ok  	github.com/ajthom90/bowtie/server/internal/config	0.106s
ok  	github.com/ajthom90/bowtie/server/internal/hdhr	0.127s
ok  	github.com/ajthom90/bowtie/server/internal/hdhr/hdhrfake	0.408s
ok  	github.com/ajthom90/bowtie/server/internal/store	0.148s
ok  	github.com/ajthom90/bowtie/server/internal/transcode	0.330s
ok  	github.com/ajthom90/bowtie/server/internal/tuner	0.169s
```

## Task 7

**Date:** 2026-08-04

### Built

- `api.Deps.Tuners *tuner.Manager` + routes in `server.go`
- Admin handlers in `admin_handlers.go`:
  - `GET /api/v1/admin/tuners` → device status (camelCase JSON)
  - `POST /api/v1/admin/devices {ip}` → FetchDiscover validate (422 if unreachable), UpsertDevice Manual=true, lineup sync, Refresh cache → 201
  - `DELETE /api/v1/admin/devices/{deviceId}` → 204
  - `POST /api/v1/admin/channels/sync` → FetchLineup + SyncLineup per stored device (skip unreachable) → 204
  - `GET /api/v1/admin/channels` → all channels with mapping state
  - `PATCH /api/v1/admin/channels/{id} {enabled?, epgChannelId?}`
  - `GET /api/v1/channels` (auth) → enabled only, `logoUrl` from mapped EPG channel IconURL ("" if unmapped)
- `cmd/bowtie/main.go`: construct `tuner.New`, background `runTunerRefresh` (immediate + every 60s)
- `docs/api/openapi.yaml`: all new paths + Device/DeviceStatus/AdminChannel/ViewerChannel schemas
- Tests: `admin_channels_test.go` — real `tuner.Manager` + hdhrfake, UDP suppressed via `SetDiscoverFunc`; full flow + 422 + viewer 403

### Notes / deltas

- IP field accepts host:port (and http URL) via existing `hdhr.BaseURLFromManual` so hdhrfake works in tests.
- Viewer logo join uses `ListEPGChannels` map in the handler (no new store method needed).
- Channel rows are not cascade-deleted when a device is removed (plan only required device delete).

### Verification (evidence)

```
$ cd server && CGO_ENABLED=0 go vet ./...
# (no output — pass)

$ CGO_ENABLED=0 go test ./... -count=1
?   	github.com/ajthom90/bowtie/server/cmd/bowtie	[no test files]
ok  	github.com/ajthom90/bowtie/server/internal/api	2.662s
ok  	github.com/ajthom90/bowtie/server/internal/auth	0.489s
ok  	github.com/ajthom90/bowtie/server/internal/config	0.149s
ok  	github.com/ajthom90/bowtie/server/internal/hdhr	0.184s
ok  	github.com/ajthom90/bowtie/server/internal/hdhr/hdhrfake	0.450s
ok  	github.com/ajthom90/bowtie/server/internal/store	0.206s
ok  	github.com/ajthom90/bowtie/server/internal/transcode	0.303s
ok  	github.com/ajthom90/bowtie/server/internal/tuner	0.227s
```


## Task 8 (worktree track/epg)
2026-08-04 — XMLTV parser: streaming element-wise decode, golden-file tests (timezone offset,
UTC-no-offset, bad-time skip case), ParseTime layouts per plan. Suite green.

## Task 9 (worktree track/epg)
2026-08-04 — Schedules Direct client: token flow with re-auth retry, lineup/schedules/programs
(500-batching), ToStore. 7 tests green against httptest fake SD. Wire-shape deltas found against
the SD wiki and adopted: TOKEN_EXPIRED is code 4006 (not 4003); POST /programs returns an array
keyed by embedded programID (not a map); multiple StationSchedule entries per station handled.

## Task 10

**Date:** 2026-08-04

### Built

- `server/internal/epg/service.go`: `Service` with injectable clock/HTTP/sd client
  - `RefreshAll`: XMLTV (file path or http(s) URL → Parse → ToStore → ReplaceEPG("xmltv")); SD when username+password+lineupId set (Token→Lineup→Schedules 14 days→Programs→ReplaceEPG("sd")); `PrunePrograms(now-24h)`; per-source LastSuccess/LastError in settings keys `epg.{xmltv,sd}.last{Success,Error}`
  - `Run`: per-source loops with ±10% jitter (xmltv = RefreshHours, sd = 12h), 15m retry on error; background prune loop
  - `Status()` → `SourceStatus{XMLTV, SD}` with `Stale = configured && lastSuccess older than 2× interval`
  - `Guide(ctx, start, stop)` → enabled channels only; one `ProgramsInRange` for all mapped IDs; unmapped → empty Programs
- `server/internal/api/guide_handlers.go` + `Deps.EPG`:
  - `GET /api/v1/guide?start=&stop=` (defaults now..now+4h; span > 24h → 422; auth)
  - `GET /api/v1/admin/epg/status`, `POST /api/v1/admin/epg/refresh` (202 background), `GET /api/v1/admin/epg/channels`
- `cmd/bowtie/main.go`: construct `epg.NewService`, `go epgSvc.Run(context.Background())`, wire into `api.Deps`
- `docs/api/openapi.yaml`: guide + admin EPG paths/schemas
- Tests: `epg/service_test.go` (RefreshAll+prune, Status stale thresholds, Guide enabled/unmapped), `api/guide_handlers_test.go` (enabled-only, defaults/span 422, admin status/channels/refresh 202, viewer 403)

### Notes / deltas

- SD client wire shapes from Task 9 used as-is (array programs response, TOKEN_EXPIRED 4006).
- Settings persist RFC3339 lastSuccess; zero time when never succeeded → stale when configured.
- `Run` starts per-source refresh immediately then sleeps with jitter (not a shared RefreshAll ticker), matching different XMLTV vs SD intervals.

### Verification (evidence)

```
$ cd server && CGO_ENABLED=0 go vet ./...
# (no output — pass)

$ CGO_ENABLED=0 go test ./... -count=1
?   	github.com/ajthom90/bowtie/server/cmd/bowtie	[no test files]
ok  	github.com/ajthom90/bowtie/server/internal/api	3.161s
ok  	github.com/ajthom90/bowtie/server/internal/auth	0.505s
ok  	github.com/ajthom90/bowtie/server/internal/config	0.166s
ok  	github.com/ajthom90/bowtie/server/internal/epg	0.239s
ok  	github.com/ajthom90/bowtie/server/internal/epg/sd	0.221s
ok  	github.com/ajthom90/bowtie/server/internal/epg/xmltv	0.182s
ok  	github.com/ajthom90/bowtie/server/internal/hdhr	0.201s
ok  	github.com/ajthom90/bowtie/server/internal/hdhr/hdhrfake	0.466s
ok  	github.com/ajthom90/bowtie/server/internal/store	0.223s
ok  	github.com/ajthom90/bowtie/server/internal/transcode	0.401s
ok  	github.com/ajthom90/bowtie/server/internal/tuner	0.243s
```

## Task 17

**Date:** 2026-08-04

### Built

- `web/`: Vite + React 18 + TypeScript scaffold (React pinned to 18 per plan; create-vite had defaulted to 19)
  - `src/api/client.ts`: typed `ApiClient` — Bearer attach, single 401 → refresh → retry, then `onAuthFail`; concurrent 401s share one refresh
  - `src/api/client.test.ts`: vitest (mock fetch) — success after refresh, retry still 401, refresh fail, missing refresh token
  - `src/auth/AuthContext.tsx`: localStorage tokens + user; refresh on boot; login/logout
  - `src/auth/Login.tsx` + CSS modules: minimal dark centered card (no design system; Task 18 owns visual language)
  - `src/App.tsx` / `main.tsx` / `global.css`: signed-in shell placeholder
  - `vite.config.ts`: dev proxy `/api` → `:8400`; build `outDir` → `server/internal/web/dist`
- `server/internal/web/embed.go`: `//go:embed all:dist`, SPA fallback to `index.html`, 404 for `/api*`, "bowtie: web ui not built" when no index
- `server/internal/web/dist/index.html`: committed **placeholder** (gitignored build artifacts via `dist/**` + `!index.html`); `make build-web` overwrites
- `server/internal/web/embed_test.go`: index serve, SPA fallback, /api rejection
- `api/server.go`: `mux.Handle("/", web.Handler())` as non-API catch-all
- Makefile: real `build-web` / `dev` / `dev-server`
- CI: `web` job (node 22, npm ci, tsc, vitest, build); server job unchanged

### Notes / deltas

- `ApiClient` constructor matches plan `(getToken, onAuthFail)` and adds optional `TokenHooks` for refresh-token read/write required by the 401 retry path.
- Placeholder dist kept committed so CI `go test` embeds without a prior web build; production binaries still need `make build-web` first.

### Verification (evidence)

```
$ cd web && npm ci && npx tsc --noEmit && npx vitest run && npm run build
# vitest: 4 passed (client 401 retry suite)
# vite build → server/internal/web/dist/{index.html,assets/...}

$ cd server && CGO_ENABLED=0 go vet ./...
# (no output — pass)

$ CGO_ENABLED=0 go test ./...
ok  	github.com/ajthom90/bowtie/server/internal/api	…
ok  	github.com/ajthom90/bowtie/server/internal/web	…
# (+ all other packages ok)
```

## Task 14 (worktree track/stream)
2026-08-04 — Stream session manager: create/join by SessionKey, viewer reap (30s) + session
grace (60s), crash restart with 1s..30s backoff and healthy-reset, ctx-cancel teardown,
SessionDirOf, injectable StreamURL/Clock/Runner. 13 tests + -race clean.
Review found a duplicate-key race that could register a dead session (proc stopped, dir
deleted, restarts never recreate dir); fixed in a follow-up commit with a bounded 3-attempt
create-or-join retry, MkdirAll on restart, and a deterministic race test.

## Task 15

**Date:** 2026-08-04

### Built

- `server/internal/stream/token.go`: `SignStreamToken` / `VerifyStreamToken` — base64url of
  `viewerID|expUnix|hex(hmacSHA256(secret, viewerID|expUnix))`; constant-time HMAC compare + expiry
- `server/internal/stream/runner.go`: `FFmpegRunner` wrapping `transcode.Command` as `stream.Runner`
- `server/internal/api/stream_handlers.go` + `StreamController` interface on `Deps.Streams`
  - `POST /api/v1/sessions` (Bearer) → `{viewerId, playlistUrl}` with 12h signed token
  - 503 `{"error":"all tuners in use","sessions":[...]}`; 404 unknown/disabled; 422 negotiate
  - `GET .../index.m3u8?token=` — verify + Touch + rewrite `seg#####.ts` lines; `application/vnd.apple.mpegurl`, `Cache-Control: no-store`
  - `GET .../{segment}?token=` — `^seg\d{5}\.ts$` only; `video/mp2t`
  - `DELETE /api/v1/sessions/{viewerId}` — Bearer **or** valid stream token → 204
  - Admin: `GET/DELETE /api/v1/admin/sessions[...]`
- **Task 11 wiring:** `Deps.Probe` + `GET /api/v1/admin/transcode` → `{available, hevc, ffmpegVersion, selected}`
- `main.go`: load/create `stream_token_secret` (32B hex, settings); probe FFmpeg once; construct
  `stream.Manager` with real `FFmpegRunner` + cached caps; `go manager.Run(ctx)`
- `docs/api/openapi.yaml`: sessions, stream playlist/segment, admin sessions + transcode schemas
- Tests: `stream/token_test.go`; `api/stream_handlers_test.go` (stub controller suite +
  keystone `TestE2EStreamLifecycle` with hdhrfake + stub Runner writing realistic m3u8+segs)

### Notes / deltas

- Stream token secret uses the same settings hex pattern as `jwt_secret` (`loadOrCreateHexSecret`).
- Start error mapping uses substring match on manager error strings (`all tuners in use`,
  `unknown channel` / `is disabled`, `negotiate:`) per Task 14 surface notes.
- Playlist path is `/api/v1/stream/{viewerId}/index.m3u8` while on-disk file remains `live.m3u8`.

### Verification (evidence)

```
$ cd server && CGO_ENABLED=0 go vet ./...
# (no output — pass)

$ CGO_ENABLED=0 go test ./... -count=1
?   	github.com/ajthom90/bowtie/server/cmd/bowtie	[no test files]
ok  	github.com/ajthom90/bowtie/server/internal/api	4.372s
ok  	github.com/ajthom90/bowtie/server/internal/auth	0.529s
ok  	github.com/ajthom90/bowtie/server/internal/config	0.177s
ok  	github.com/ajthom90/bowtie/server/internal/epg	0.285s
ok  	github.com/ajthom90/bowtie/server/internal/epg/sd	0.249s
ok  	github.com/ajthom90/bowtie/server/internal/epg/xmltv	0.216s
ok  	github.com/ajthom90/bowtie/server/internal/hdhr	0.221s
ok  	github.com/ajthom90/bowtie/server/internal/hdhr/hdhrfake	0.511s
ok  	github.com/ajthom90/bowtie/server/internal/store	0.242s
ok  	github.com/ajthom90/bowtie/server/internal/stream	0.440s
ok  	github.com/ajthom90/bowtie/server/internal/transcode	0.449s
ok  	github.com/ajthom90/bowtie/server/internal/tuner	0.262s
ok  	github.com/ajthom90/bowtie/server/internal/web	0.218s
```

## Task 16

**Date:** 2026-08-04

### Built

#### Part 1 — API extension (session info on create)

- `stream.Manager.SessionInfoOf(viewerID) (SessionInfo, bool)` — snapshot of the session a viewer joined
- `StreamController` interface + stub updated
- `POST /api/v1/sessions` response now includes:
  `{"viewerId","playlistUrl","session":{"videoCodec","profile","backend","channelName"}}`
- `docs/api/openapi.yaml`: `CreateSessionResponse.session` → `CreateSessionSessionInfo`
- `TestE2EStreamLifecycle` asserts session fields (`h264`/`high`/`software`/`WABC`)

#### Part 2 — Final assembly + graceful shutdown

- `cmd/bowtie/main.go` refactored to testable `run(ctx, cfg) (addr, shutdown, err)`
  - Assembly order: store → auth secrets/bootstrap → tuners (60s refresh) → epg `Run` → probe → stream manager `Run` → API
  - **All** background goroutines (tuner refresh, EPG, stream manager) hang off one root ctx
  - Listen via `net.Listen` so `127.0.0.1:0` returns the real bound address
  - Graceful shutdown (logged sequence):
    1. `http.Server.Shutdown` (10s timeout)
    2. cancel root ctx (stops sessions/ffmpeg via Manager, tickers)
    3. close store
  - `bootstrapAdmin` now returns `(password string, err)` for tests (still logs on first run)
- `cmd/bowtie/main_test.go`:
  - `TestSmokeAssembledServer` — random free port, `/healthz` 200, login as bootstrap admin 200, clean shutdown ≤15s
  - `TestBootstrapAdminIdempotent`

### Notes / deltas

- Plan Step 4 (manual real-hardware validation) is **pending-user** — not attempted (no HDHomeRun in agent environment). User should run `make dev-server`, add device IP, enable a channel, curl a session, open playlist in VLC/Safari.
- Signal handling lives in `main()`; `run` uses an independent root ctx so shutdown can enforce HTTP-first order rather than cancelling workers on SIGTERM before HTTP drain.

### Verification (evidence)

```
$ cd server && CGO_ENABLED=0 go vet ./...
# (no output — pass)

$ CGO_ENABLED=0 go test ./... -count=1
ok  	github.com/ajthom90/bowtie/server/cmd/bowtie	1.065s
ok  	github.com/ajthom90/bowtie/server/internal/api	4.403s
ok  	github.com/ajthom90/bowtie/server/internal/auth	0.454s
ok  	github.com/ajthom90/bowtie/server/internal/config	0.155s
ok  	github.com/ajthom90/bowtie/server/internal/epg	0.177s
ok  	github.com/ajthom90/bowtie/server/internal/epg/sd	0.224s
ok  	github.com/ajthom90/bowtie/server/internal/epg/xmltv	0.126s
ok  	github.com/ajthom90/bowtie/server/internal/hdhr	0.190s
ok  	github.com/ajthom90/bowtie/server/internal/hdhr/hdhrfake	0.455s
ok  	github.com/ajthom90/bowtie/server/internal/store	0.217s
ok  	github.com/ajthom90/bowtie/server/internal/stream	0.308s
ok  	github.com/ajthom90/bowtie/server/internal/transcode	0.332s
ok  	github.com/ajthom90/bowtie/server/internal/tuner	0.229s
ok  	github.com/ajthom90/bowtie/server/internal/web	0.163s

$ go test -race ./cmd/... ./internal/api/ ./internal/stream/ -count=1
ok  	github.com/ajthom90/bowtie/server/cmd/bowtie	4.069s
ok  	github.com/ajthom90/bowtie/server/internal/api	46.978s
ok  	github.com/ajthom90/bowtie/server/internal/stream	1.640s
```


## Task 20

**Date:** 2026-08-04

### Built

- `deploy/Dockerfile` multi-stage:
  1. `node:22-slim` — `npm ci` + Vite build → `server/internal/web/dist`
  2. `golang:1.22` — copy dist, `CGO_ENABLED=0` static binary
  3. `debian:bookworm-slim` — non-free + non-free-firmware enabled; `ffmpeg`, `mesa-va-drivers`, `tini`, `ca-certificates`; `intel-media-va-driver-non-free` only when `TARGETARCH=amd64` (arm64-safe)
  - `ENTRYPOINT ["tini","--","/usr/local/bin/bowtie"]`, `ENV BOWTIE_DATA_DIR=/data`, `EXPOSE 8400`, `VOLUME /data`
- `deploy/docker-compose.yml`: `ghcr.io/ajthom90/bowtie:latest`, `/dev/dri` device pass-through, tmpfs `/data/segments`, host-network option documented for HDHomeRun UDP discovery
- `docs/deploy/remote-access.md`: complete Caddy reverse-proxy, Cloudflare Tunnel, and Tailscale Serve/Funnel examples
- `README.md` rewrite: what/why, docker quickstart, hardware matrix (VideoToolbox / QSV+VAAPI / NVENC community / software), EPG (XMLTV + SD), remote-access pointer, Apache-2.0 badge, v0.1.0 Phase 1 status
- CI: `docker` job on `ci.yml` — buildx linux/amd64, `--load`, no push

### Notes / deltas

- Intel QSV package gated on `TARGETARCH=amd64` so local arm64 Docker Desktop builds succeed (plan packages are amd64-only).
- `mesa-va-drivers` installed on all arches.

### Verification (evidence)

```
$ docker build -f deploy/Dockerfile -t bowtie:dev .
# exit 0 — multi-stage build completes (linux/arm64 locally)

$ docker run --rm -d -p 18400:8400 --name bowtie-smoke bowtie:dev
$ sleep 2 && curl -sf http://localhost:18400/healthz
ok

$ docker exec bowtie-smoke ffmpeg -version | head -1
ffmpeg version 5.1.9-0+deb12u1 Copyright (c) 2000-2026 the FFmpeg developers

$ docker logs bowtie-smoke
# first run: created admin user "admin" with password "…" — change it after login
# encoder probe: available=[software] version=5.1.9-0+deb12u1
# bowtie 0.1.0-dev listening on [::]:8400 (data=/data)

$ docker rm -f bowtie-smoke

$ cd server && CGO_ENABLED=0 go test ./... -count=1
ok  	github.com/ajthom90/bowtie/server/cmd/bowtie	1.165s
ok  	github.com/ajthom90/bowtie/server/internal/api	4.491s
ok  	github.com/ajthom90/bowtie/server/internal/auth	0.473s
ok  	github.com/ajthom90/bowtie/server/internal/config	0.155s
ok  	github.com/ajthom90/bowtie/server/internal/epg	0.207s
ok  	github.com/ajthom90/bowtie/server/internal/epg/sd	0.193s
ok  	github.com/ajthom90/bowtie/server/internal/epg/xmltv	0.154s
ok  	github.com/ajthom90/bowtie/server/internal/hdhr	0.159s
ok  	github.com/ajthom90/bowtie/server/internal/hdhr/hdhrfake	0.453s
ok  	github.com/ajthom90/bowtie/server/internal/store	0.220s
ok  	github.com/ajthom90/bowtie/server/internal/stream	0.366s
ok  	github.com/ajthom90/bowtie/server/internal/transcode	0.384s
ok  	github.com/ajthom90/bowtie/server/internal/tuner	0.229s
ok  	github.com/ajthom90/bowtie/server/internal/web	0.157s
```
## Task 18

**Date:** 2026-08-04

### Built

- Design system in `web/src/global.css`: broadcast tokens (`--bg`, `--surface`, `--accent` amber, etc.), focus ring, reduced-motion
- Self-hosted fonts via `@fontsource/*`: Barlow Condensed, IBM Plex Sans, IBM Plex Mono
- `web/src/guide/guideModel.ts` + tests: `layoutRow` (clip/gap/percent), `halfHourTicks`, `nowLinePct`, window paging helpers
- `web/src/guide/Guide.tsx`: sticky channel column (big condensed numbers), 30-min grid, NOW line, program cells with on-air accent border, prev/next/now paging, click → Player
- `web/src/player/caps.ts` + tests: `detectCaps` (h264 always; hevc via MSE or Safari canPlayType; aac/ac3), `canPlayNativeHls`
- `web/src/player/Player.tsx`: hls.js (or native HLS), session create/delete, quality re-session, overlay controls, stats overlay (tolerates missing `session` meta → "—"), 503 tuner-busy copy
- `ApiClient`: `getGuide`, `getChannels`, `createSession`, `deleteSession` + typed session response with optional `session`
- Deps: `hls.js`, `@fontsource/barlow-condensed`, `@fontsource/ibm-plex-sans`, `@fontsource/ibm-plex-mono`
- App shell routes Guide ↔ Player; Login restyled to design tokens

### Notes / deltas

- `CreateSessionResponse.session` optional (parallel branch may add it); stats show "—" for missing fields.
- Unload cleanup uses `fetch(..., { method: 'DELETE', keepalive: true })` because `sendBeacon` is POST-only and cannot DELETE. Also calls `sendBeacon` with stream-token query as a secondary best-effort signal. Uses `beforeunload` + `pagehide` instead of `visibilitychange-hidden` so alt-tab does not tear down a live session (pagehide covers mobile unload).
- Quality ladder labels: Original / High / Medium / Low; profile sent via `caps.profile`.

### Verification (evidence)

```
$ cd web && npx tsc --noEmit && npx vitest run && npm run build
# vitest: 23 passed (guideModel 12, caps 7, client 4)
# vite build → server/internal/web/dist/

$ cd server && CGO_ENABLED=0 go test ./internal/web/
ok  	github.com/ajthom90/bowtie/server/internal/web	0.122s
```

### Review fix (Task 18)

Task 18's commit had overwritten the committed dist placeholder with a built
index.html referencing gitignored hashed assets; the generic placeholder was
restored in follow-up commit 3b0d17c.

## Task 19 (worktree track/web)

2026-08-04 — Admin UI: Tuners (signal bars, add-device, sync), Channels (toggle + EPG
mapping + numeric-aware sort), EPG source status/refresh, Users CRUD with quality caps,
live Sessions with terminate + encoder status readout. 36 vitest tests total across web,
tsc clean, role-guarded routes. Verified: tsc --noEmit, vitest run, vite build, go test
./internal/web/ all green.

## Task 21

**Date:** 2026-08-04

### Built

- `.github/workflows/release.yml` on tag `v*`:
  - **docker** job: qemu + buildx multi-arch (`linux/amd64`, `linux/arm64`) → push `ghcr.io/ajthom90/bowtie:<version>` and `:latest` (GHCR login via `GITHUB_TOKEN`, `permissions: packages: write` + `contents: write`)
  - **goreleaser** job: Node 22 `npm ci` + Vite build into `server/internal/web/dist`, then GoReleaser v2 release (CGO off) attaching binaries + changelog to a GitHub Release
- `server/.goreleaser.yaml`: project_name `bowtie`, main `./cmd/bowtie`, builds darwin/arm64 + linux/amd64 + linux/arm64, ldflags stamp `main.version`, archives include repo-root LICENSE + README
- `cmd/bowtie/main.go`: `const version` → `var version = "0.1.0-dev"` so `-X main.version=` works
- `README.md`: Install section — GHCR image (recommended) + release binary table + from-source; Quickstart kept for first-run flow

### Notes / deltas

- Step 2 end-to-end (buildx push + real Release) is **(validated at rc tag)** — orchestrator tags `v0.1.0-rc1`; no tags created in this task.
- No `git push` / `gh` / tags (standing rules).

### Verification (evidence)

```
$ cd server && go run github.com/goreleaser/goreleaser/v2@latest check
# checking path=.goreleaser.yaml
# 1 configuration file(s) validated

$ cd server && go build ./cmd/bowtie && ./bowtie --help 2>&1 | head -3 && rm -f bowtie
Usage of ./bowtie:
  -data-dir string
    data directory (env BOWTIE_DATA_DIR) (default "./data")

$ cd server && CGO_ENABLED=0 go test ./... && golangci-lint run
ok  	github.com/ajthom90/bowtie/server/cmd/bowtie	1.170s
ok  	github.com/ajthom90/bowtie/server/internal/api	(cached)
# … all packages ok …
0 issues.
```

## Task 22

**Date:** 2026-08-04

### Built

- `api.Routes() []string` — returns every registered `/api/v1` pattern as `METHOD /path`, built alongside registration via `mountAPI` (single source of truth with `New`)
- `server/internal/api/openapi_test.go`: `TestOpenAPICoversRoutes` — parses `docs/api/openapi.yaml` with `gopkg.in/yaml.v3`, normalizes path params (`{id}` → `{}`), asserts:
  - every `Routes()` entry appears in the spec
  - every `/api/v1` path+method in the spec has a registered route
- `README.md`: dedicated **API** section pointing at `docs/api/openapi.yaml` as the Phase 2/3 client contract
- Plan Task 22 steps 1/2/4 ticked; step 3 tags left for orchestrator

### Spec omissions found + fixed

None. The OpenAPI document already listed all 26 registered `/api/v1` path+method pairs; the test passed on first run after `Routes()` was added. No path/method needed to be added or removed on either side.

### Notes / deltas

- Tagging (`v0.1.0-rc1` / `v0.1.0`) is **(orchestrator tags)** — not done here.
- Commit message uses the user-specified form (`docs: complete openapi spec with route-coverage test`) rather than the plan's older "cut v0.1.0" wording, since tags are out of scope.

### Verification (evidence)

```
$ cd server && CGO_ENABLED=0 go vet ./... && CGO_ENABLED=0 go test ./... && golangci-lint run
ok  	github.com/ajthom90/bowtie/server/cmd/bowtie	…
ok  	github.com/ajthom90/bowtie/server/internal/api	…  (includes TestOpenAPICoversRoutes)
# … all packages ok …
0 issues.
```

## Release build split

**Date:** 2026-08-04

### Built

- Refactored `.github/workflows/release.yml` image publish from one QEMU multi-arch buildx job to native split builds:
  - **`build-image`** matrix: `linux/amd64` → `ubuntu-24.04`, `linux/arm64` → `ubuntu-24.04-arm` (no QEMU)
  - Push by digest (`push-by-digest=true`, `name-canonical=true`); digest artifacts `digests-linux-amd64` / `digests-linux-arm64`
  - GHA layer cache scoped per platform (`cache-from`/`cache-to` type=gha, scope=PLATFORM_PAIR)
  - **`merge-manifest`**: download digests → `docker buildx imagetools create` tags `:VERSION` + `:latest` → `imagetools inspect`
  - VERSION still from tag with leading `v` stripped (`TAG#v`)
- **GoReleaser job unchanged**
- **ci.yml docker job left alone** (already amd64-only with `--load`, no QEMU)

### Verification (evidence)

```
$ python3 -c "import yaml; yaml.safe_load(open('.github/workflows/release.yml'))" && echo YAML_OK
YAML_OK

$ which actionlint
# not installed

$ cd server && CGO_ENABLED=0 go test ./... && golangci-lint run
# all packages ok; 0 issues
```
