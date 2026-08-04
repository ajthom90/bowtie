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
