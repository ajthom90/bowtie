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
