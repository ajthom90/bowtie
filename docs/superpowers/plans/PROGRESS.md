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
