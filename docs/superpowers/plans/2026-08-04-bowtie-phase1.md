# Bowtie Phase 1 (Server + Web Viewer) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. **This session's division of labor: implementation is delegated to Grok via /grok-build; Claude reviews each task.**

**Goal:** A deployable Go server that streams HDHomeRun channels as HLS with hardware transcoding, a TV guide (XMLTV + Schedules Direct), admin-managed users, and an embedded React web viewer.

**Architecture:** Single Go binary embedding the web UI; FFmpeg as an external supervised process; HLS with shared transcode sessions keyed by (channel, video codec, quality, audio codec); SQLite for state. See spec: `docs/superpowers/specs/2026-08-04-bowtie-server-design.md`.

**Tech Stack:** Go 1.22+, `modernc.org/sqlite`, `golang-jwt/jwt/v5`, `golang.org/x/crypto/argon2`, FFmpeg (external), React 18 + TypeScript + Vite + hls.js, GitHub Actions, GoReleaser, Docker buildx.

## Global Constraints

- License: Apache-2.0. `LICENSE` at repo root. No per-file headers required.
- Go 1.22+, **CGO disabled** (`CGO_ENABLED=0`). SQLite via `modernc.org/sqlite` only.
- FFmpeg is always an external process (`exec.Command`), never linked.
- All API routes under `/api/v1`. JSON request/response bodies use camelCase keys.
- Default listen address `:8400`. Env vars prefixed `BOWTIE_`. Config file `<dataDir>/config.yaml`; env overrides file.
- HLS: 4-second MPEG-TS segments, sliding window of 30 segments, `delete_segments` on.
- Session lifecycle: viewer heartbeat timeout 30s; session teardown grace 60s after last viewer.
- Auth: Argon2id (t=3, m=64MiB, p=2, salt 16B, key 32B). Access JWT HS256, 15 min. Refresh token: 32 random bytes, SHA-256 stored, 30-day expiry, revocable.
- Quality ladder defaults: original=1080p/8000kbps, high=720p/4000kbps, medium=720p/2500kbps, low=480p/1500kbps. Audio 160/160/128/96kbps.
- Commit style: conventional commits (`feat:`, `fix:`, `test:`, `chore:`, `docs:`), `Co-Authored-By` trailer per repo convention.
- Tests: `go test ./...` from `server/` must pass with no real HDHomeRun and no FFmpeg (FFmpeg-dependent tests use build tag `ffmpeg` and run only on dev machines).
- OpenAPI spec `docs/api/openapi.yaml` is updated in the same task that adds/changes any endpoint.
- The GitHub repo is `github.com/ajthom90/bowtie` (public). Go module path `github.com/ajthom90/bowtie/server`.

## File Structure (target)

```
bowtie/
├── LICENSE  README.md  .gitignore  Makefile
├── docs/
│   ├── api/openapi.yaml
│   ├── deploy/remote-access.md
│   └── superpowers/{specs,plans}/...
├── deploy/
│   ├── Dockerfile
│   └── docker-compose.yml
├── .github/workflows/{ci.yml,release.yml}
├── server/
│   ├── go.mod  go.sum
│   ├── cmd/bowtie/main.go
│   └── internal/
│       ├── config/config.go
│       ├── store/{store.go,users.go,devices.go,channels.go,epg.go,tokens.go}
│       ├── store/migrations/0001_init.sql
│       ├── auth/{password.go,tokens.go,middleware.go}
│       ├── hdhr/{discover.go,client.go}
│       ├── hdhr/hdhrfake/fake.go
│       ├── tuner/manager.go
│       ├── epg/xmltv/xmltv.go
│       ├── epg/sd/client.go
│       ├── epg/service.go
│       ├── transcode/{probe.go,profile.go,ffmpeg.go}
│       ├── stream/{session.go,manager.go,token.go}
│       ├── api/{server.go,auth_handlers.go,admin_handlers.go,guide_handlers.go,stream_handlers.go}
│       └── web/embed.go            (+ web/dist committed as build artifact? No — built by Makefile/CI)
└── web/
    ├── package.json  vite.config.ts  tsconfig.json  index.html
    └── src/
        ├── api/client.ts
        ├── auth/{AuthContext.tsx,Login.tsx}
        ├── guide/{Guide.tsx,guideModel.ts}
        ├── player/{Player.tsx,caps.ts}
        ├── admin/{Admin.tsx,Tuners.tsx,Channels.tsx,Epg.tsx,Users.tsx,Sessions.tsx}
        └── main.tsx  App.tsx
```

Testing hinges on `hdhrfake` — an in-process HTTP server emulating an HDHomeRun (discover.json, lineup.json, status.json, and `/auto/v{n}` streaming a looped MPEG-TS fixture). All tuner/session logic is testable in CI with zero hardware.

---

# Milestone A — Foundation

### Task 1: Repo scaffold, config loader, CI skeleton, GitHub repo

**Files:**
- Create: `LICENSE` (Apache-2.0 text), `README.md`, `.gitignore`, `Makefile`
- Create: `server/go.mod`, `server/cmd/bowtie/main.go`
- Create: `server/internal/config/config.go`, Test: `server/internal/config/config_test.go`
- Create: `.github/workflows/ci.yml`, `docs/api/openapi.yaml` (skeleton: info + empty paths)

**Interfaces:**
- Produces: `config.Load(dataDir string) (Config, error)`; struct below. Later tasks read `cfg.<Field>`.

```go
// internal/config/config.go
type Config struct {
    ListenAddr string   `yaml:"listenAddr"` // default ":8400"
    DataDir    string   `yaml:"-"`
    SegmentDir string   `yaml:"segmentDir"` // default filepath.Join(DataDir, "segments")
    FFmpegPath string   `yaml:"ffmpegPath"` // default "ffmpeg"
    Encoder    string   `yaml:"encoder"`    // "auto"|"videotoolbox"|"qsv"|"nvenc"|"vaapi"|"software"
    AllowHEVC  bool     `yaml:"allowHevc"`
    Devices    []string `yaml:"devices"`    // manual HDHomeRun IPs
    XMLTV      struct {
        Source       string `yaml:"source"`       // file path or http(s) URL
        RefreshHours int    `yaml:"refreshHours"` // default 12
    } `yaml:"xmltv"`
    SchedulesDirect struct {
        Username string `yaml:"username"`
        Password string `yaml:"password"` // raw; SHA1 computed at request time (SD API requirement)
        LineupID string `yaml:"lineupId"`
    } `yaml:"schedulesDirect"`
}
```

- [x] **Step 1: Write failing test** — `config_test.go`: `TestLoadDefaults` (empty dir → defaults: `:8400`, segmentDir under dataDir, ffmpeg path "ffmpeg", encoder "auto"); `TestLoadYAMLAndEnvOverride` (write `config.yaml` with `listenAddr: ":9000"`, set `BOWTIE_LISTEN_ADDR=":9100"` via `t.Setenv` → env wins).
- [x] **Step 2: Run** `cd server && go test ./internal/config/` — expect FAIL (package missing).
- [x] **Step 3: Implement** `config.Load`: read `<dataDir>/config.yaml` if present (`gopkg.in/yaml.v3`), then apply env overrides `BOWTIE_LISTEN_ADDR`, `BOWTIE_FFMPEG_PATH`, `BOWTIE_ENCODER`, `BOWTIE_SEGMENT_DIR`, `BOWTIE_DEVICES` (comma-separated). `main.go`: parse `--data-dir` flag (default `./data`, env `BOWTIE_DATA_DIR`), `os.MkdirAll` data+segment dirs, load config, print version, start placeholder `http.Server` on ListenAddr serving 200 on `/healthz`.
- [x] **Step 4: Run** `go test ./...` + `go vet ./...` — PASS. `go run ./cmd/bowtie --data-dir /tmp/bowtie-dev` then `curl localhost:8400/healthz` → 200.
- [x] **Step 5: Scaffold the rest** — Makefile targets: `build` (web build → copy into `server/internal/web/dist` → `go build`), `test`, `lint`, `dev`. `ci.yml`: on PR/push — job `server`: setup-go, `golangci-lint run`, `go test ./...` in `server/`; job `web` added in Task 17 (leave placeholder comment, not a failing job). `.gitignore`: `data/`, `server/internal/web/dist/`, `web/node_modules/`, `web/dist/`, `dist/`.
- [ ] **Step 6: Create GitHub repo and push** (skipped — handled by orchestrator)

```bash
git add -A && git commit -m "feat: scaffold bowtie server, config loader, CI"
gh repo create ajthom90/bowtie --public --description "Open-source live TV streaming for HDHomeRun — share your antenna with your family" --source . --push
```

### Task 2: SQLite store + migrations

**Files:**
- Create: `server/internal/store/store.go`, `store/migrations/0001_init.sql`, `store/users.go`, `store/devices.go`, `store/channels.go`, `store/epg.go`, `store/tokens.go`
- Test: `server/internal/store/store_test.go`

**Interfaces:**
- Consumes: nothing (opens its own DB file).
- Produces:

```go
func Open(path string) (*Store, error)      // runs embedded migrations (go:embed migrations/*.sql, applied in filename order, tracked in schema_migrations)
type Store struct{ db *sql.DB }
func (s *Store) Close() error

type User struct { ID int64; Username string; PasswordHash string; Role string /* "admin"|"viewer" */; MaxQuality string /* profile name or "" = unlimited */; CreatedAt time.Time }
func (s *Store) CreateUser(u User) (int64, error)
func (s *Store) UserByUsername(name string) (User, error)   // sql.ErrNoRows when missing
func (s *Store) UserByID(id int64) (User, error)
func (s *Store) ListUsers() ([]User, error)
func (s *Store) UpdateUser(u User) error                    // username, role, maxQuality
func (s *Store) UpdatePassword(id int64, hash string) error
func (s *Store) DeleteUser(id int64) error
func (s *Store) CountUsers() (int, error)

type Device struct { DeviceID string; IP string; Model string; TunerCount int; Manual bool; LastSeen time.Time }
func (s *Store) UpsertDevice(d Device) error
func (s *Store) ListDevices() ([]Device, error)
func (s *Store) DeleteDevice(deviceID string) error

type Channel struct { ID int64; DeviceID string; GuideNumber string; Name string; Enabled bool; EPGChannelID string /* "" = unmapped */ }
func (s *Store) SyncLineup(deviceID string, chans []Channel) error // upsert by (deviceID,guideNumber); preserve Enabled + EPGChannelID on existing; delete rows absent from new lineup
func (s *Store) ListChannels(enabledOnly bool) ([]Channel, error)
func (s *Store) ChannelByID(id int64) (Channel, error)
func (s *Store) UpdateChannel(id int64, enabled bool, epgChannelID string) error

type EPGChannel struct { ID string; DisplayName string; Callsign string; IconURL string; Source string /* "xmltv"|"sd" */ }
type Program struct { ID int64; EPGChannelID string; Start time.Time; Stop time.Time; Title string; Subtitle string; Description string; Category string; IconURL string }
func (s *Store) ReplaceEPG(source string, chans []EPGChannel, progs []Program) error // transactional: delete rows for source, insert new
func (s *Store) ListEPGChannels() ([]EPGChannel, error)
func (s *Store) ProgramsInRange(epgChannelIDs []string, start, stop time.Time) ([]Program, error)
func (s *Store) PrunePrograms(olderThan time.Time) error

type RefreshToken struct { ID int64; UserID int64; TokenHash string; ExpiresAt time.Time }
func (s *Store) SaveRefreshToken(t RefreshToken) error
func (s *Store) RefreshTokenByHash(hash string) (RefreshToken, error)
func (s *Store) DeleteRefreshToken(hash string) error
func (s *Store) DeleteExpiredRefreshTokens(now time.Time) error

func (s *Store) GetSetting(key string) (string, error)      // "" if missing
func (s *Store) SetSetting(key, value string) error
```

- [x] **Step 1: Write failing tests** — `store_test.go` using `t.TempDir()` DB: `TestMigrateAndCRUDUsers` (create/read/update/delete round-trip; duplicate username errors); `TestSyncLineupPreservesEnabled` (sync 3 channels, enable one + map EPG id, re-sync with same guide numbers + 1 new + 1 removed → enabled/mapping preserved, new present disabled, removed gone); `TestReplaceEPGAndRange` (insert 2 sources; ReplaceEPG for one replaces only that source; ProgramsInRange with fixed times `time.Date(2026, 8, 4, ...)` returns programs overlapping the window, i.e. `Stop > start && Start < stop`); `TestRefreshTokens` (save/get/delete/expire); `TestSettings`.
- [x] **Step 2: Run** — FAIL (types missing).
- [x] **Step 3: Implement** — `0001_init.sql` with tables `users, devices, channels, epg_channels, programs, refresh_tokens, settings, schema_migrations`; indexes: `channels(device_id, guide_number)` unique, `programs(epg_channel_id, start)`, `refresh_tokens(token_hash)` unique. Times stored as RFC3339 strings (UTC).
- [x] **Step 4: Run** `go test ./internal/store/` — PASS.
- [x] **Step 5: Commit** `git add server && git commit -m "feat: sqlite store with migrations for users, devices, channels, epg, tokens"`

### Task 3: Auth core — Argon2id, JWT, refresh tokens, bootstrap admin

**Files:**
- Create: `server/internal/auth/password.go`, `auth/tokens.go`
- Test: `server/internal/auth/password_test.go`, `auth/tokens_test.go`
- Modify: `server/cmd/bowtie/main.go` (bootstrap admin + JWT secret init)

**Interfaces:**
- Consumes: `store.Store` (users, refresh tokens, settings).
- Produces:

```go
func HashPassword(pw string) (string, error) // "$argon2id$v=19$m=65536,t=3,p=2$<salt-b64>$<key-b64>"
func VerifyPassword(pw, encoded string) (bool, error)

type Auth struct{ Secret []byte; Store *store.Store }
type Claims struct { UserID int64; Username string; Role string }
func (a *Auth) NewAccessToken(u store.User, now time.Time) (string, error) // HS256; claims sub(userID as string), "username", "role"; exp now+15m
func (a *Auth) ParseAccessToken(tok string, now time.Time) (Claims, error)
func (a *Auth) NewRefreshToken(userID int64, now time.Time) (string, error) // raw base64url(32 rand bytes); stores sha256 hex, exp now+30d
func (a *Auth) Rotate(raw string, now time.Time) (store.User, string, error) // validate+delete old, issue new; error if unknown/expired
func (a *Auth) Revoke(raw string) error
```

- [x] **Step 1: Failing tests** — `TestHashVerifyRoundTrip`, `TestVerifyWrongPassword`, `TestAccessTokenRoundTrip` (fixed `now`), `TestAccessTokenExpired` (parse with now+16m → error), `TestRefreshRotate` (issue → rotate returns user + new token, old token now invalid), `TestRefreshExpired`.
- [x] **Step 2: Run** — FAIL.
- [x] **Step 3: Implement** — argon2id via `golang.org/x/crypto/argon2.IDKey(pw, salt, 3, 64*1024, 2, 32)`; constant-time compare. JWT via `github.com/golang-jwt/jwt/v5`. In `main.go` startup: JWT secret from settings key `jwt_secret` (create 32 random bytes hex on first run); if `CountUsers()==0`, create `admin` with a generated 16-char password **printed once to stdout** (`log.Printf("first run: created admin user %q with password %q — change it after login", ...)`).
- [x] **Step 4: Run** `go test ./...` — PASS.
- [x] **Step 5: Commit** `git commit -m "feat: argon2id passwords, jwt access + rotating refresh tokens, bootstrap admin"`

### Task 4: HTTP API skeleton + auth endpoints & middleware

**Files:**
- Create: `server/internal/api/server.go`, `api/auth_handlers.go`, `server/internal/auth/middleware.go`
- Test: `server/internal/api/auth_handlers_test.go`
- Modify: `server/cmd/bowtie/main.go` (wire router), `docs/api/openapi.yaml`

**Interfaces:**
- Consumes: `auth.Auth`, `store.Store`.
- Produces: `api.New(deps Deps) http.Handler` where

```go
type Deps struct {
    Cfg   config.Config
    Store *store.Store
    Auth  *auth.Auth
}
// Deps GROWS over the plan — later tasks add fields when their packages exist
// (compiling forward references to not-yet-written packages is impossible):
//   Task 7:  Tuners *tuner.Manager
//   Task 10: EPG *epg.Service
//   Task 11: Probe func() transcode.Capabilities
//   Task 15: Streams StreamController (interface defined in Task 15)
// Each of those tasks modifies server.go to add its field + routes in the same commit.
```

Endpoints (all JSON; errors as `{"error": "message"}` with proper status):
- `POST /api/v1/auth/login {username,password}` → `{accessToken, refreshToken, user:{id,username,role,maxQuality}}` (401 on bad creds)
- `POST /api/v1/auth/refresh {refreshToken}` → same shape (401 invalid/expired)
- `POST /api/v1/auth/logout {refreshToken}` → 204
- `GET /api/v1/me` (Bearer) → `{id,username,role,maxQuality}`
- `POST /api/v1/me/password {currentPassword,newPassword}` → 204 (403 if current wrong)
- Middleware: `auth.RequireUser(a *Auth) func(http.Handler) http.Handler` (Bearer → Claims in context via `auth.ClaimsFrom(ctx)`), `auth.RequireAdmin` wraps RequireUser + role check (403).

- [x] **Step 1: Failing tests** — `httptest` against `api.New` with real store (temp DB) + seeded user: `TestLoginSuccessAndMe`, `TestLoginBadPassword401`, `TestRefreshRotation`, `TestMeRequiresAuth401`, `TestPasswordChange` (old refresh flow still valid; new login with new password works), `TestAdminRouteForbiddenForViewer` (register a dummy admin-only route in test via exported router option or use `/api/v1/admin/users` from Task 5 — instead: test `RequireAdmin` middleware directly with a stub handler).
- [x] **Step 2: Run** — FAIL.
- [x] **Step 3: Implement** — stdlib `net/http` + `http.ServeMux` (Go 1.22 method patterns, e.g. `mux.HandleFunc("POST /api/v1/auth/login", ...)`). No router dependency.
- [x] **Step 4: Run** — PASS.
- [x] **Step 5: Update** `docs/api/openapi.yaml` with these paths/schemas. **Commit** `git commit -m "feat: api skeleton with login, refresh, logout, me endpoints"`

### Task 5: User admin API + fake HDHomeRun test server

**Files:**
- Create: `server/internal/api/admin_handlers.go` (users part), `server/internal/hdhr/hdhrfake/fake.go`, `server/internal/hdhr/hdhrfake/testdata/fixture.ts` (generated, see below)
- Test: `server/internal/api/admin_users_test.go`, `server/internal/hdhr/hdhrfake/fake_test.go`
- Modify: `docs/api/openapi.yaml`

**Interfaces:**
- Consumes: store, auth middleware.
- Produces:
  - Admin endpoints: `GET /api/v1/admin/users`, `POST /api/v1/admin/users {username,password,role,maxQuality}`, `PATCH /api/v1/admin/users/{id} {role?,maxQuality?,password?}`, `DELETE /api/v1/admin/users/{id}` (409 refusing to delete the last admin).
  - `hdhrfake.New(t testing.TB, opts Options) *Fake` with:

```go
type Options struct { DeviceID string; TunerCount int; Lineup []LineupEntry }
type LineupEntry struct { GuideNumber string `json:"GuideNumber"`; GuideName string `json:"GuideName"`; URL string `json:"URL"`; VideoCodec string `json:"VideoCodec"`; AudioCodec string `json:"AudioCodec"` }
// hdhrfake defines its OWN LineupEntry (identical JSON shape to the hdhr package's, Task 6) so this
// task compiles before Task 6 exists; the coupling is over JSON, not Go types — deliberate duplication.
type Fake struct { URL string /* http://127.0.0.1:port */ }
// Serves:
//  GET /discover.json  → {FriendlyName:"HDHomeRun FAKE",ModelNumber:"HDFX-4US",DeviceID,FirmwareVersion:"20260101",TunerCount,BaseURL:URL,LineupURL:URL+"/lineup.json"}
//  GET /lineup.json    → Options.Lineup
//  GET /status.json    → per-tuner: {"Resource":"tuner0","VctNumber":"5.1",...} reflecting active fake streams
//  GET /auto/v{n}      → 200 with Content-Type video/mp2t, loops testdata/fixture.ts at ~1x speed until client disconnects; 503 with body "all tuners in use" when active streams == TunerCount
func (f *Fake) ActiveStreams() int
```

  - Fixture: `testdata/fixture.ts` is a ~2s 480i MPEG-2/AC-3 transport stream ≤ 1 MiB, generated once on a dev machine with `ffmpeg -f lavfi -i "testsrc2=duration=2:size=720x480:rate=29.97" -f lavfi -i "sine=frequency=440:duration=2" -c:v mpeg2video -b:v 2M -flags +ilme+ildct -c:a ac3 -b:a 192k -f mpegts fixture.ts` and committed (binary test fixture; document the command in a sibling README.md).

- [x] **Step 1: Failing tests** — users CRUD via httptest (admin token vs viewer token 403; last-admin delete 409). Fake: `TestFakeServesDiscoverLineupStatus`, `TestFakeStreamsAndCountsTuners` (open 2 readers on 2-tuner fake → both stream bytes; third gets 503; close one → ActiveStreams decrements).
- [x] **Step 2: Run** — FAIL.
- [x] **Step 3: Implement.**
- [x] **Step 4: Run** — PASS.
- [x] **Step 5: Update openapi.yaml (admin/users). Commit** `git commit -m "feat: admin user management api; test fake hdhomerun server"`

### Task 6: HDHomeRun client + discovery + Tuner Manager

**Files:**
- Create: `server/internal/hdhr/client.go`, `hdhr/discover.go`, `server/internal/tuner/manager.go`
- Test: `server/internal/hdhr/client_test.go` (against hdhrfake), `hdhr/discover_test.go`, `tuner/manager_test.go`

**Interfaces:**
- Consumes: `hdhrfake` (tests), `store.Store` (device persistence), `config.Config.Devices`.
- Produces:

```go
// hdhr package
type DiscoverInfo struct { FriendlyName, ModelNumber, DeviceID, FirmwareVersion, BaseURL, LineupURL string; TunerCount int }
type LineupEntry struct { GuideNumber string `json:"GuideNumber"`; GuideName string `json:"GuideName"`; URL string `json:"URL"`; VideoCodec string `json:"VideoCodec"`; AudioCodec string `json:"AudioCodec"` }
type TunerStatus struct { Resource string `json:"Resource"`; VctNumber string `json:"VctNumber"`; VctName string `json:"VctName"`; Frequency int64 `json:"Frequency"`; SignalStrengthPercent int `json:"SignalStrengthPercent"`; SignalQualityPercent int `json:"SignalQualityPercent"`; SymbolQualityPercent int `json:"SymbolQualityPercent"`; TargetIP string `json:"TargetIP"` }
func FetchDiscover(ctx context.Context, baseURL string) (DiscoverInfo, error)
func FetchLineup(ctx context.Context, baseURL string) ([]LineupEntry, error)
func FetchStatus(ctx context.Context, baseURL string) ([]TunerStatus, error)
func Discover(ctx context.Context, timeout time.Duration) ([]DiscoverInfo, error) // UDP broadcast 65001; implement the libhdhomerun discover packet (verify against https://github.com/Silicondust/libhdhomerun hdhomerun_pkt.h/hdhomerun_discover.c); on any error return what was found + nil error unless the socket failed

// tuner package
type DeviceStatus struct { Device store.Device; Reachable bool; Tuners []hdhr.TunerStatus }
type Manager struct{ /* store, cfg, http fetchers injectable for tests */ }
func New(st *store.Store, cfg config.Config) *Manager
func (m *Manager) Refresh(ctx context.Context) error // discovery (UDP, best-effort) + cfg.Devices (manual) + previously stored devices → FetchDiscover each → UpsertDevice; unreachable keeps stored row, Reachable=false
func (m *Manager) Devices() []DeviceStatus            // cached from last Refresh + live FetchStatus
func (m *Manager) StreamURL(ch store.Channel) (string, error) // "http://<ip>:5004/auto/v<guideNumber>"; for hdhrfake (test) the port is the fake's; so: use device BaseURL host + stored StreamPort — add StreamPort int to store.Device (default 5004; hdhrfake reports its own via DiscoverInfo.BaseURL port)
```

**Note:** `StreamURL` must work for both real devices (port 5004) and the fake (same port as its HTTP listener). Rule: if the device's BaseURL port is 80 or empty → use port 5004; otherwise reuse the BaseURL host:port. Encode this rule in a unit test.

- [x] **Step 1: Failing tests** — client fetch trio against `hdhrfake`; `TestDiscoverPacketRoundTrip` (encode discover request → parse it back; parse a hand-built response packet constructed with our own encoder — plus a `//go:build hdhr_live` tagged test that broadcasts on the real network for manual verification); manager: `TestRefreshAggregatesManualAndStored`, `TestStreamURLPortRule` (BaseURL `http://1.2.3.4:80` → `http://1.2.3.4:5004/auto/v5.1`; fake URL `http://127.0.0.1:54321` → same port).
- [x] **Step 2: Run** — FAIL.
- [x] **Step 3: Implement.** Add `StreamPort`/migration `0002_device_stream_port.sql` if simpler than parsing at call time — implementer's choice, but the port rule test must pass.
- [x] **Step 4: Run** — PASS.
- [x] **Step 5: Commit** `git commit -m "feat: hdhomerun client, udp discovery, tuner manager"`

### Task 7: Channel management + tuner/device admin API

**Files:**
- Modify: `server/internal/api/admin_handlers.go`, `server/internal/api/server.go` (wire `Deps.Tuners`), `server/cmd/bowtie/main.go` (construct tuner.Manager, periodic Refresh every 60s)
- Test: `server/internal/api/admin_channels_test.go`
- Modify: `docs/api/openapi.yaml`

**Interfaces:**
- Consumes: `tuner.Manager` (as `Deps.Tuners`), `store` channel methods, `hdhr.FetchLineup`.
- Produces endpoints:
  - `GET /api/v1/admin/tuners` → `[]DeviceStatus` (admin)
  - `POST /api/v1/admin/devices {ip}` → adds manual device (FetchDiscover to validate; 422 if unreachable), triggers lineup sync
  - `DELETE /api/v1/admin/devices/{deviceId}`
  - `POST /api/v1/admin/channels/sync` → re-fetch lineup for every device, `store.SyncLineup` each
  - `GET /api/v1/admin/channels` → all channels with mapping state
  - `PATCH /api/v1/admin/channels/{id} {enabled?, epgChannelId?}`
  - `GET /api/v1/channels` (any authenticated user) → enabled channels only: `[{id, guideNumber, name, logoUrl}]` (logoUrl from mapped EPG channel icon, else "")

- [x] **Step 1: Failing tests** — httptest with hdhrfake behind a real `tuner.Manager`: add device by IP → sync → list → enable one → viewer `GET /api/v1/channels` sees only the enabled channel; viewer hitting admin routes → 403.
- [x] **Step 2: Run** — FAIL.
- [x] **Step 3: Implement.**
- [x] **Step 4: Run** — PASS.
- [x] **Step 5: Update openapi.yaml. Commit** `git commit -m "feat: device and channel admin api, viewer channel list"`

# Milestone B — EPG

### Task 8: XMLTV parser + import

**Files:**
- Create: `server/internal/epg/xmltv/xmltv.go`, Test: `epg/xmltv/xmltv_test.go`, golden file `epg/xmltv/testdata/guide.xml`

**Interfaces:**
- Consumes: `store.EPGChannel`, `store.Program` types.
- Produces:

```go
type TV struct { Channels []Channel; Programmes []Programme }
type Channel struct { ID string `xml:"id,attr"`; DisplayNames []string `xml:"display-name"`; Icon struct{ Src string `xml:"src,attr"` } `xml:"icon"` }
type Programme struct { Start string `xml:"start,attr"`; Stop string `xml:"stop,attr"`; Channel string `xml:"channel,attr"`; Title string `xml:"title"`; SubTitle string `xml:"sub-title"`; Desc string `xml:"desc"`; Categories []string `xml:"category"`; Icon struct{ Src string `xml:"src,attr"` } `xml:"icon"` }
func Parse(r io.Reader) (*TV, error)
func ParseTime(s string) (time.Time, error) // layouts: "20060102150405 -0700", "20060102150405" (assume UTC)
func ToStore(tv *TV) ([]store.EPGChannel, []store.Program) // Source:"xmltv"; Callsign = shortest display-name; skip programmes with unparseable times (count returned via third value: skipped int)
```

- [x] **Step 1: Golden file** — hand-write `testdata/guide.xml`: 2 channels (`ch1.example` "WABC (5.1 ABC)", `ch2.example`), 3 programmes incl. one with timezone offset `-0500`, one UTC-no-offset, one with bad start (skip case).
- [x] **Step 2: Failing tests** — `TestParseGolden` (counts, first programme fields, time equality against `time.Date(...)` values), `TestToStoreSkipsBad` (skipped==1).
- [x] **Step 3: Run** — FAIL. **Implement** with `encoding/xml` streaming decoder (`xml.NewDecoder`, handle large files without slurping: decode `<channel>`/`<programme>` elements one at a time).
- [x] **Step 4: Run** — PASS.
- [x] **Step 5: Commit** `git commit -m "feat: xmltv parser with golden-file tests"`

### Task 9: Schedules Direct client

**Files:**
- Create: `server/internal/epg/sd/client.go`, Test: `epg/sd/client_test.go` (httptest fake SD server)

**Interfaces:**
- Consumes: config SchedulesDirect fields.
- Produces:

```go
type Client struct { BaseURL string /* default https://json.schedulesdirect.org/20141201 */; HTTP *http.Client; Username, Password string; token string }
func (c *Client) Token(ctx context.Context) error                    // POST /token {username, password: sha1hex(password)} → {token}; cache; all later calls send header "token"
func (c *Client) Lineup(ctx context.Context, lineupID string) (Lineup, error) // GET /lineups/{id} → {map:[{stationID,channel}], stations:[{stationID,callsign,name,logo:{URL}}]}
type Lineup struct { Map []struct{ StationID string `json:"stationID"`; Channel string `json:"channel"` } `json:"map"`; Stations []struct{ StationID string `json:"stationID"`; Callsign string `json:"callsign"`; Name string `json:"name"`; Logo struct{ URL string `json:"URL"` } `json:"logo"` } `json:"stations"` }
func (c *Client) Schedules(ctx context.Context, stationIDs []string, dates []string) ([]StationSchedule, error) // POST /schedules [{stationID,date:[...]}]
type StationSchedule struct { StationID string `json:"stationID"`; Programs []struct{ ProgramID string `json:"programID"`; AirDateTime time.Time `json:"airDateTime"`; Duration int `json:"duration"` } `json:"programs"` }
func (c *Client) Programs(ctx context.Context, programIDs []string) (map[string]ProgramDetail, error) // POST /programs; batches of 500
type ProgramDetail struct { Titles []struct{ Title120 string `json:"title120"` } `json:"titles"`; Descriptions struct{ Description1000 []struct{ Description string `json:"description"` } `json:"description1000"` } `json:"descriptions"`; EpisodeTitle150 string `json:"episodeTitle150"`; Genres []string `json:"genres"` }
func (c *Client) ToStore(lineup Lineup, scheds []StationSchedule, details map[string]ProgramDetail) ([]store.EPGChannel, []store.Program)
// EPGChannel.ID = "sd-"+stationID; Source:"sd". Program times = AirDateTime .. +Duration seconds.
```

**Implementation note for Grok:** verify request/response shapes against the SD wiki (https://github.com/SchedulesDirect/JSON-Service/wiki) with WebFetch before coding; the shapes above are the contract our code consumes — adapt JSON tags to reality, keep Go signatures as written.

- [x] **Step 1: Failing tests** — httptest fake SD: token flow (401 without token header afterwards), lineup parse, schedules batch request body assertion, programs batching (600 IDs → 2 POSTs), `ToStore` mapping (fixed times).
- [x] **Step 2: Run** — FAIL. **Implement.**
- [x] **Step 3: Run** — PASS.
- [x] **Step 4: Commit** `git commit -m "feat: schedules direct client"`

### Task 10: EPG service + guide API

**Files:**
- Create: `server/internal/epg/service.go`, Test: `epg/service_test.go`, `server/internal/api/guide_handlers.go`, Test: `api/guide_handlers_test.go`
- Modify: `server/cmd/bowtie/main.go` (construct+start service), `api/server.go` (wire `Deps.EPG`), `docs/api/openapi.yaml`

**Interfaces:**
- Consumes: xmltv, sd, store, config.
- Produces:

```go
type Service struct{ /* store, cfg, sd client, http for xmltv url, clock func() time.Time */ }
func NewService(st *store.Store, cfg config.Config) *Service
func (s *Service) RefreshAll(ctx context.Context) error // xmltv if configured (fetch URL or read file → Parse → ToStore → ReplaceEPG("xmltv")); sd if configured (Token→Lineup→Schedules(14 days)→Programs→ReplaceEPG("sd")); PrunePrograms(now-24h); record per-source LastSuccess/LastError in settings
func (s *Service) Run(ctx context.Context) // ticker: RefreshHours (xmltv) / 12h (sd), jitter ±10%, retry after 15m on error
func (s *Service) Status() SourceStatus
type SourceStatus struct { XMLTV SourceState `json:"xmltv"`; SD SourceState `json:"sd"` }
type SourceState struct { Configured bool `json:"configured"`; LastSuccess time.Time `json:"lastSuccess"`; LastError string `json:"lastError"`; Stale bool `json:"stale"` /* configured && lastSuccess older than 2× interval */ }
type GuideChannel struct { ChannelID int64 `json:"channelId"`; GuideNumber string `json:"guideNumber"`; Name string `json:"name"`; LogoURL string `json:"logoUrl"`; Programs []GuideProgram `json:"programs"` }
type GuideProgram struct { Start time.Time `json:"start"`; Stop time.Time `json:"stop"`; Title string `json:"title"`; Subtitle string `json:"subtitle"`; Description string `json:"description"`; Category string `json:"category"` }
func (s *Service) Guide(ctx context.Context, start, stop time.Time) ([]GuideChannel, error) // enabled channels only; channels with no mapping return empty Programs
```

Endpoints: `GET /api/v1/guide?start=RFC3339&stop=RFC3339` (default now..now+4h, max span 24h → 422), auth required. Admin: `GET /api/v1/admin/epg/status`, `POST /api/v1/admin/epg/refresh` (fires RefreshAll in background, 202), `GET /api/v1/admin/epg/channels` (ListEPGChannels — feeds the mapping dropdown).

- [x] **Step 1: Failing tests** — service with injected clock + file XMLTV fixture: `TestRefreshAllImportsAndPrunes`, `TestStatusStale`; guide API: seed store, `TestGuideReturnsEnabledOnly`, `TestGuideDefaultsAndSpanLimit`.
- [x] **Step 2: Run** — FAIL. **Implement.**
- [x] **Step 3: Run** — PASS.
- [x] **Step 4: Update openapi.yaml. Commit** `git commit -m "feat: epg service with xmltv + schedules direct, guide api"`

---

# Milestone C — Streaming

### Task 11: Encoder probe

**Files:**
- Create: `server/internal/transcode/probe.go`, Test: `transcode/probe_test.go` (fake ffmpeg script), `transcode/probe_ffmpeg_test.go` (build tag `ffmpeg`, real binary)

**Interfaces:**
- Produces:

```go
type Backend string
const (BackendVideoToolbox Backend = "videotoolbox"; BackendQSV Backend = "qsv"; BackendNVENC Backend = "nvenc"; BackendVAAPI Backend = "vaapi"; BackendSoftware Backend = "software")
type Capabilities struct { Available []Backend; HEVC map[Backend]bool; FFmpegVersion string }
func Probe(ctx context.Context, ffmpegPath string) Capabilities
// For each backend in platform order (darwin: videotoolbox; linux: qsv,nvenc,vaapi; always: software):
//   run tiny encode: ffmpeg -hide_banner -f lavfi -i testsrc2=duration=0.5:size=320x240:rate=30 <backend init flags> -c:v <h264 encoder> -f null -
//   backend flags: qsv: -init_hw_device qsv=hw; vaapi: -init_hw_device vaapi=va:/dev/dri/renderD128 -vf format=nv12,hwupload (encoder h264_vaapi); nvenc: none (h264_nvenc); videotoolbox: none (h264_videotoolbox); software: none (libx264)
//   exit 0 → available; repeat with hevc encoder (hevc_videotoolbox/hevc_qsv/hevc_nvenc/hevc_vaapi/libx265) → HEVC[backend]
func (c Capabilities) Select(forced string) (Backend, error) // forced=="auto" → first Available; else that backend if Available, error otherwise
```

- [x] **Step 1: Failing tests** — probe against a fake `ffmpeg` shell script in `t.TempDir()` (exits 0 only for chosen encoder args) → asserts Available/HEVC parsing and Select logic. Tagged test runs real Probe and just logs the result (asserts `software` is always available when real ffmpeg present).
- [x] **Step 2: Run** — FAIL. **Implement** (exec with 10s timeout per probe; cache result; expose via `Deps.Probe` on `GET /api/v1/admin/transcode` (admin) → `{available, hevc, ffmpegVersion, selected}`).
- [x] **Step 3: Run** — PASS. On your Mac, `go test -tags ffmpeg ./internal/transcode/` should report videotoolbox available.
- [x] **Step 4: Update openapi.yaml. Commit** `git commit -m "feat: ffmpeg encoder probing with ranked backends"`

### Task 12: Profiles + codec negotiation

**Files:**
- Create: `server/internal/transcode/profile.go`, Test: `transcode/profile_test.go`

**Interfaces:**
- Produces:

```go
type Profile struct { Name string `json:"name"`; Height int `json:"height"`; VideoKbps int `json:"videoKbps"`; AudioKbps int `json:"audioKbps"` }
func DefaultProfiles() []Profile // original 1080/8000/160, high 720/4000/160, medium 720/2500/128, low 480/1500/96
func ProfileByName(ps []Profile, name string) (Profile, bool)
type ClientCaps struct { VideoCodecs []string `json:"videoCodecs"` /* "h264","hevc","av1" */; AudioCodecs []string `json:"audioCodecs"` /* "aac","ac3","eac3" */; MaxHeight int `json:"maxHeight"`; Profile string `json:"profile"` /* requested quality name; "" = original */ }
type Decision struct { VideoCodec string; VideoEncoder string /* ffmpeg encoder name */; AudioCopy bool; Profile Profile; Backend Backend }
func Negotiate(caps ClientCaps, userMaxQuality string, hw Capabilities, forced string, allowHEVC bool, profiles []Profile) (Decision, error)
// rules: backend = hw.Select(forced). video: "hevc" if allowHEVC && caps has hevc && hw.HEVC[backend]; else "h264" (error if caps lacks h264 too).
// encoder name = codec+backend map (h264_videotoolbox, h264_qsv, h264_nvenc, h264_vaapi, libx264; hevc_* / libx265).
// profile: requested (or "original"), clamped by userMaxQuality (if set) and by caps.MaxHeight (drop to highest profile with Height<=MaxHeight; MaxHeight 0 = no limit).
// audio: AudioCopy = caps includes "ac3"; else transcode aac.
func SessionKey(channelID int64, d Decision) string // fmt: "ch%d|%s|%s|%s" channelID, VideoCodec, Profile.Name, "copy"|"aac"
```

- [x] **Step 1: Failing tests** — table-driven: h264-only web client → h264+aac; hevc TV client with ac3 + allowHEVC → hevc+copy; user capped "medium" requesting "original" → medium; MaxHeight 480 → low; no common video codec → error; forced backend unavailable → error; SessionKey stability.
- [x] **Step 2: Run** — FAIL. **Implement.**
- [x] **Step 3: Run** — PASS.
- [x] **Step 4: Commit** `git commit -m "feat: quality profiles and codec negotiation"`

### Task 13: FFmpeg command builder

**Files:**
- Create: `server/internal/transcode/ffmpeg.go`, Test: `transcode/ffmpeg_test.go`, tagged e2e `transcode/ffmpeg_e2e_test.go` (`ffmpeg` tag)

**Interfaces:**
- Consumes: `Decision`, `Backend`.
- Produces:

```go
type JobSpec struct { InputURL string; OutDir string; D Decision }
func BuildArgs(s JobSpec) []string
// Shape (exact order matters for tests):
//  global: -hide_banner -loglevel warning -nostats
//  input: [backend hwaccel flags] -i <InputURL>
//    qsv:   -init_hw_device qsv=hw -hwaccel qsv -hwaccel_output_format qsv -c:v mpeg2_qsv
//    nvenc: -hwaccel cuda -hwaccel_output_format cuda
//    vaapi: -init_hw_device vaapi=va:/dev/dri/renderD128 -hwaccel vaapi -hwaccel_output_format vaapi
//    videotoolbox/software: (no input accel; sw decode)
//  filters (-vf), scale only when Profile.Height < input assumed 1080 — always emit scale, cheap no-op protection is fine:
//    qsv:   vpp_qsv=deinterlace=2:scale_mode=hq:w=-1:h=<H>
//    nvenc: yadif_cuda=0:-1:0,scale_cuda=-2:<H>
//    vaapi: deinterlace_vaapi=rate=frame,scale_vaapi=w=-2:h=<H>
//    videotoolbox/software: yadif=0:-1:0,scale=-2:<H>
//  video: -c:v <VideoEncoder> -b:v <VideoKbps>k -maxrate <VideoKbps*1.2>k -bufsize <VideoKbps*2>k -g 120 -force_key_frames expr:gte(t,n_forced*4)
//    plus per-encoder: libx264: -preset veryfast -profile:v high; h264_qsv: -preset veryfast; h264_nvenc: -preset p4; videotoolbox: -realtime 1 -profile:v high (h264 only); vaapi: (none)
//  audio: AudioCopy ? -c:a copy : -c:a aac -ac 2 -b:a <AudioKbps>k
//  mux: -f hls -hls_time 4 -hls_list_size 30 -hls_flags delete_segments+temp_file -hls_segment_type mpegts -hls_segment_filename <OutDir>/seg%05d.ts <OutDir>/live.m3u8
func Command(ctx context.Context, ffmpegPath string, s JobSpec) *exec.Cmd // exec.CommandContext, Stdout/Stderr to a prefixed logger
```

- [x] **Step 1: Failing tests** — golden-args tests for software/videotoolbox/qsv/vaapi/nvenc (compare full `[]string`), audio copy vs aac.
- [x] **Step 2: Run** — FAIL. **Implement.**
- [x] **Step 3: Run** — PASS.
- [x] **Step 4: Tagged e2e (dev machine):** run Command with software backend against `hdhrfake` stream URL for ~10s into `t.TempDir()`; assert `live.m3u8` exists and ≥1 `seg*.ts`; then probe first segment with `ffprobe -show_streams` → h264 + aac. On the Mac also run with videotoolbox.
- [x] **Step 5: Commit** `git commit -m "feat: ffmpeg command builder for all hardware backends"`

### Task 14: Stream session manager

**Files:**
- Create: `server/internal/stream/session.go`, `stream/manager.go`, Test: `stream/manager_test.go`

**Interfaces:**
- Consumes: `tuner.Manager.StreamURL`, `transcode.Negotiate/BuildArgs/Command`, `store.User`, config.
- Produces:

```go
type Viewer struct { ID string; SessionID string; Username string; LastSeen time.Time }
type SessionInfo struct { ID string `json:"id"`; ChannelID int64 `json:"channelId"`; ChannelName string `json:"channelName"`; Key string `json:"key"`; VideoCodec string `json:"videoCodec"`; Profile string `json:"profile"`; Backend string `json:"backend"`; Viewers []ViewerInfo `json:"viewers"`; StartedAt time.Time `json:"startedAt"` }
type ViewerInfo struct { ID string `json:"id"`; Username string `json:"username"`; LastSeen time.Time `json:"lastSeen"` }
type Manager struct{ /* deps injected; clock injectable; ffmpeg runner injectable (interface Runner{ Start(ctx, JobSpec) (Process, error) }) for tests */ }
func NewManager(deps ManagerDeps) *Manager
type ManagerDeps struct { Cfg config.Config; Store *store.Store; Tuners *tuner.Manager; StreamURL func(store.Channel) (string, error) /* nil → Tuners.StreamURL; injectable for tests */; Caps transcode.Capabilities; Runner Runner; Clock func() time.Time }
func (m *Manager) Start(ctx context.Context, user store.User, channelID int64, caps transcode.ClientCaps) (ViewerHandle, error)
type ViewerHandle struct { ViewerID string; SessionID string; SessionDir string } // SessionDir contains live.m3u8
// Start: Negotiate → SessionKey → join existing session (add viewer) or create: mkdir <SegmentDir>/<sessionID>, Runner.Start ffmpeg, wait until live.m3u8 exists (poll ≤15s; error on timeout/exit)
func (m *Manager) Touch(viewerID string) bool     // heartbeat; false if unknown
func (m *Manager) StopViewer(viewerID string)
func (m *Manager) Sessions() []SessionInfo
func (m *Manager) SessionDirOf(viewerID string) (string, bool) // consumed by Task 15 handlers
func (m *Manager) Terminate(sessionID string)     // admin kill: stops ffmpeg, removes dir, drops viewers
func (m *Manager) Run(ctx context.Context)        // 5s ticker: reap viewers idle >30s; sessions with 0 viewers >60s → stop ffmpeg, rm dir; restart crashed ffmpeg with backoff 1s,2s,4s..30s cap (same dir, append)
type Process interface { Done() <-chan error; Stop() }
type Runner interface { Start(ctx context.Context, spec transcode.JobSpec) (Process, error) }
```

- [x] **Step 1: Failing tests** — with a stub Runner (writes a fake `live.m3u8` immediately; Done channel controllable) + fake clock: `TestStartCreatesSession`, `TestSecondViewerSharesSession` (same caps → 1 runner start, 2 viewers), `TestDifferentQualityNewSession`, `TestViewerReapAndSessionGrace` (advance clock: viewer reaped at >30s, session torn down 60s after empty), `TestCrashRestartBackoff` (Done fires error → runner restarted; assert 1s then 2s delays via fake clock), `TestTerminate`.
- [x] **Step 2: Run** — FAIL. **Implement** (mutex-guarded maps; per-session goroutine supervising Process).
- [x] **Step 3: Run** — PASS.
- [x] **Step 4: Commit** `git commit -m "feat: shared stream sessions with reaping and crash restart"`

### Task 15: Stream HTTP endpoints + signed tokens

**Files:**
- Create: `server/internal/stream/token.go`, `server/internal/api/stream_handlers.go`
- Test: `stream/token_test.go`, `api/stream_handlers_test.go`
- Modify: `api/server.go` (Deps.Streams interface), `cmd/bowtie/main.go` (wire), `docs/api/openapi.yaml`

**Interfaces:**
- Consumes: `stream.Manager`, auth middleware, `transcode.ClientCaps`.
- Produces:

```go
// stream/token.go
func SignStreamToken(secret []byte, viewerID string, exp time.Time) string // base64url(viewerID|expUnix|hex(hmacSHA256(secret, viewerID|expUnix)))
func VerifyStreamToken(secret []byte, token string, now time.Time) (viewerID string, err error)

// api: StreamController interface consumed by server.go
type StreamController interface { Start(ctx context.Context, user store.User, channelID int64, caps transcode.ClientCaps) (stream.ViewerHandle, error); Touch(string) bool; StopViewer(string); Sessions() []stream.SessionInfo; Terminate(string); SessionDirOf(viewerID string) (string, bool) }
```

Endpoints:
- `POST /api/v1/sessions {channelId, caps:{videoCodecs,audioCodecs,maxHeight,profile}}` (Bearer) → `{viewerId, playlistUrl}` where playlistUrl=`/api/v1/stream/<viewerId>/index.m3u8?token=<signed exp=now+12h>`; 503 with `{"error":"all tuners in use","sessions":[...]}` when tuner acquisition fails; 404 unknown/disabled channel.
- `GET /api/v1/stream/{viewerId}/index.m3u8?token=` — verify token (viewerID match), `Touch(viewerID)`, read `<sessionDir>/live.m3u8`, rewrite each `seg*.ts` line to `/api/v1/stream/<viewerId>/<seg>?token=<same>`, serve `application/vnd.apple.mpegurl`, `Cache-Control: no-store`.
- `GET /api/v1/stream/{viewerId}/{segment}?token=` — verify, validate segment name `^seg\d{5}\.ts$` (reject traversal), serve `video/mp2t` from session dir.
- `DELETE /api/v1/sessions/{viewerId}` (Bearer or token) → StopViewer, 204.
- Admin: `GET /api/v1/admin/sessions` → `[]SessionInfo`; `DELETE /api/v1/admin/sessions/{sessionId}` → Terminate, 204.

- [x] **Step 1: Failing tests** — token round-trip/expiry/tamper; handlers with a stub StreamController: playlist rewrite (feed a real ffmpeg-style live.m3u8 fixture with `#EXTINF` lines), token-viewer mismatch 403, segment name traversal 400, heartbeat calls Touch, 503 shape, admin terminate.
- [x] **Step 2: Run** — FAIL. **Implement.**
- [x] **Step 3: Run** — PASS.
- [x] **Step 4: E2E CI test (no ffmpeg):** full server with hdhrfake + stub Runner: login → list channels → POST sessions → GET playlist (200, rewritten) → DELETE. This is the integration keystone test — name it `TestE2EStreamLifecycle` in `api/stream_handlers_test.go`.
- [x] **Step 5: Update openapi.yaml. Commit** `git commit -m "feat: hls delivery endpoints with signed stream tokens"`

### Task 16: Wire it together — main.go assembly + dev loop against real hardware

**Files:**
- Modify: `server/cmd/bowtie/main.go` (final assembly: store→auth→tuners→epg→probe→stream manager→api; graceful shutdown on SIGTERM: stop sessions, close store)
- Create: `server/cmd/bowtie/main_test.go` (smoke: start on random port with temp dir, hit /healthz and /api/v1/auth/login)

- [ ] **Step 1: Failing smoke test.**
- [ ] **Step 2: Implement assembly + graceful shutdown.**
- [ ] **Step 3: Run** `go test ./...` — ALL PASS.
- [ ] **Step 4 (manual, requires the user's LAN):** `make dev-server`, add the real HDHomeRun IP, enable a channel, `curl` a session, open playlist in VLC/Safari. Document findings in the PR/commit message. This step is best-effort for Grok (no hardware in its environment) — the user validates.
- [ ] **Step 5: Commit** `git commit -m "feat: full server assembly with graceful shutdown"`

# Milestone D — Web Viewer

*UI tasks: use the frontend-design skill at implementation time for visual direction. Keep the design simple, dark-theme-first (it's a TV app), responsive.*

### Task 17: Web scaffold, API client, login, embedding

**Files:**
- Create: `web/package.json`, `web/vite.config.ts` (dev proxy `/api` → `http://localhost:8400`; build outDir `../server/internal/web/dist`), `web/tsconfig.json`, `web/index.html`, `web/src/{main.tsx,App.tsx}`, `web/src/api/client.ts`, `web/src/auth/{AuthContext.tsx,Login.tsx}`
- Create: `server/internal/web/embed.go`
- Test: `web/src/api/client.test.ts` (vitest), embed test in `server/internal/web/embed_test.go`
- Modify: `Makefile` (`build-web`, `dev`), `.github/workflows/ci.yml` (web job: npm ci, typecheck, vitest, build)

**Interfaces:**
- Consumes: auth endpoints from Task 4.
- Produces:

```ts
// api/client.ts — typed fetch wrapper
export interface User { id: number; username: string; role: 'admin'|'viewer'; maxQuality: string }
export interface LoginResponse { accessToken: string; refreshToken: string; user: User }
export class ApiClient {
  constructor(getToken: () => string | null, onAuthFail: () => void)
  login(username: string, password: string): Promise<LoginResponse>
  refresh(refreshToken: string): Promise<LoginResponse>
  // request<T>(method, path, body?) — attaches Bearer, one retry after refresh on 401, then onAuthFail
}
```

```go
// embed.go
//go:embed all:dist
var dist embed.FS
func Handler() http.Handler // serves dist; SPA fallback: unknown non-/api paths → index.html; if dist empty (dev build without web), serve 200 "bowtie: web ui not built"
```

- [x] **Step 1: Scaffold** `npm create vite@latest` (react-ts), prune boilerplate.
- [x] **Step 2: Failing tests** — vitest: ApiClient retry-on-401-once logic (mock fetch); Go: embed handler SPA fallback.
- [x] **Step 3: Implement** client, AuthContext (tokens in localStorage; refresh on boot), Login page; wire `web.Handler()` into `api/server.go` as the non-/api fallback.
- [x] **Step 4: Run** `npm run build && cd ../server && go test ./...` + manual: `make dev` (vite) login against dev server.
- [x] **Step 5: Commit** `git commit -m "feat: web scaffold with auth flow, embedded spa serving"`

### Task 18: Guide + player

**Files:**
- Create: `web/src/guide/{Guide.tsx,guideModel.ts}`, `web/src/player/{Player.tsx,caps.ts}`
- Test: `web/src/guide/guideModel.test.ts`, `web/src/player/caps.test.ts`

**Interfaces:**
- Consumes: `GET /api/v1/guide`, `GET /api/v1/channels`, `POST /api/v1/sessions`, `DELETE /api/v1/sessions/{viewerId}`.
- Produces:

```ts
// caps.ts
export function detectCaps(): ClientCaps
// videoCodecs: h264 always; hevc if MediaSource.isTypeSupported('video/mp4; codecs="hvc1.1.6.L93.B0"') or Safari
// audioCodecs: aac always; ac3 if audio/mp4; codecs="ac-3" supported (rare in browsers — usually just aac)
export interface ClientCaps { videoCodecs: string[]; audioCodecs: string[]; maxHeight: number; profile: string }

// guideModel.ts — pure functions, unit tested
export function layoutRow(programs: GuideProgram[], windowStart: Date, windowStop: Date): Cell[]
// clips programs to window, computes % offsets/widths, inserts gap cells for holes
```

- Guide: sticky channel column, 30-min gridlines, now marker, click program/channel → Player. Player: hls.js (native HLS on Safari via `canPlayType('application/vnd.apple.mpegurl')`), quality picker re-creates session with `profile` set, stats overlay (profile/codec from session response + hls.js bandwidth estimate), Back to guide stops session (`DELETE`), `beforeunload`/`visibilitychange` best-effort stop via `navigator.sendBeacon`.

- [ ] **Step 1: Failing tests** — guideModel layout (clip/gap/percent math with fixed dates); caps detection with mocked `MediaSource`.
- [ ] **Step 2: Run** — FAIL. **Implement** model + components.
- [ ] **Step 3: Run** vitest + typecheck + build — PASS. Manual dev check against server + hdhrfake or real device.
- [ ] **Step 4: Commit** `git commit -m "feat: guide grid and hls player with capability detection"`

### Task 19: Admin UI

**Files:**
- Create: `web/src/admin/{Admin.tsx,Tuners.tsx,Channels.tsx,Epg.tsx,Users.tsx,Sessions.tsx}`
- Test: `web/src/admin/adminModel.test.ts` (any pure logic, e.g. channel-list filtering/sorting)

**Interfaces:** consumes the admin endpoints from Tasks 5, 7, 10, 11, 15 exactly as specified in openapi.yaml.

Pages (admin role only; route guard): **Tuners** (device cards: tuners, signal bars, add-device-by-IP form, sync lineup button), **Channels** (table: enable toggle, EPG mapping dropdown fed by `/admin/epg/channels`, filter box), **EPG** (source status incl. stale warnings + refresh-now), **Users** (CRUD, role, max quality dropdown, reset password), **Sessions** (live sessions w/ viewers, encoder backend from `/admin/transcode`, terminate button; poll every 5s).

- [ ] **Step 1: Failing tests** for pure logic; **Step 2: Implement**; **Step 3: vitest/typecheck/build PASS + manual pass**; **Step 4: Commit** `git commit -m "feat: admin ui for tuners, channels, epg, users, sessions"`

---

# Milestone E — Packaging & Release

### Task 20: Docker + compose + deploy docs

**Files:**
- Create: `deploy/Dockerfile`, `deploy/docker-compose.yml`, `docs/deploy/remote-access.md`
- Modify: `.github/workflows/ci.yml` (docker build job, no push on PR), `README.md`

Dockerfile (multi-stage): 1) `node:22-slim` build web; 2) `golang:1.22` build server with dist copied in, `CGO_ENABLED=0`; 3) runtime `debian:bookworm-slim` + `apt-get install -y --no-install-recommends ffmpeg intel-media-va-driver-non-free mesa-va-drivers tini` (enable `non-free non-free-firmware` components), `ENTRYPOINT ["tini","--","/usr/local/bin/bowtie"]`, `ENV BOWTIE_DATA_DIR=/data`, `EXPOSE 8400`, volume `/data`. compose: `devices: [/dev/dri:/dev/dri]` for QSV/VAAPI, tmpfs mount for `/data/segments`, comments explaining each line. remote-access.md: Caddy reverse-proxy block, Cloudflare Tunnel, Tailscale — copy-pasteable configs.

- [ ] **Step 1: Build** `docker build -f deploy/Dockerfile .` locally (arm64) — image runs, `/healthz` 200, `ffmpeg -version` works inside.
- [ ] **Step 2: CI job** builds (no push) on PR for amd64 only (speed).
- [ ] **Step 3: README:** what/why, screenshot placeholder comment `<!-- TODO screenshot after user runs it -->` is FORBIDDEN by plan rules — instead: quickstart (docker compose up, first-run admin password from logs, add device, enable channels, watch), hardware transcode matrix, EPG setup, remote access pointer, license badge.
- [ ] **Step 4: Commit** `git commit -m "feat: docker packaging with vaapi/qsv support and deploy docs"`

### Task 21: Release automation

**Files:**
- Create: `.github/workflows/release.yml`, `server/.goreleaser.yaml`
- Modify: `README.md` (install section: ghcr image + binaries)

- [ ] **Step 1:** `release.yml` on tag `v*`: (a) buildx multi-arch (linux/amd64, linux/arm64) push `ghcr.io/ajthom90/bowtie:{version,latest}`; (b) GoReleaser: `bowtie` binaries darwin/arm64, linux/amd64, linux/arm64 (CGO off), attach to GitHub Release with changelog.
- [ ] **Step 2:** Validate: `goreleaser check` + `act`-free dry run isn't possible for buildx — rely on tagging `v0.1.0-rc1` after merge to test the pipeline end-to-end.
- [ ] **Step 3: Commit** `git commit -m "feat: release workflow with multi-arch images and binaries"`

### Task 22: OpenAPI completeness + final review sweep

**Files:**
- Modify: `docs/api/openapi.yaml`, `README.md`
- Test: `server/internal/api/openapi_test.go`

- [ ] **Step 1: Failing test** — `TestOpenAPICoversRoutes`: walk the mux route table (export route list from `api.New` as `func Routes() []string` built alongside registration) and assert every `/api/v1/...` route appears in openapi.yaml (parse yaml, compare paths with `{param}` normalization).
- [ ] **Step 2:** Fix omissions. Run full suite: `go test ./...`, `npm test`, `npm run build`, docker build.
- [ ] **Step 3:** Tag `v0.1.0-rc1`, verify release workflow artifacts, then `v0.1.0`.
- [ ] **Step 4: Commit** `git commit -m "docs: complete openapi spec; cut v0.1.0"`

---

## Post-plan notes

- **Parallelization guide:** Tasks 2→3→4→5(users) are sequential. After Task 5: {6,8,9,11,12} are independent of each other. 13 needs 12; 14 needs 6+13; 15 needs 14; 10 needs 8+9. Milestone D needs the API tasks it consumes (17 after 4; 18 after 15+10; 19 after all admin APIs). 20-22 last. Use git worktrees per parallel track; merge to main frequently.
- **Review protocol:** each task lands as a commit (or short branch) reviewed by Claude before the next dependent task starts.
- Phase 2 (iOS/tvOS, Android) and Phase 3 (Roku, Fire TV) get their own plans once the v0.1.0 API is tagged.


