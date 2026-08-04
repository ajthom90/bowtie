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
