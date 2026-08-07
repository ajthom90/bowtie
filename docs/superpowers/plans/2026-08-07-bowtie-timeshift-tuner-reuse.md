# Bowtie v0.5.0 — Timeshift + Tuner Reuse Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax. **This session: Grok implements sequentially on `main`; Claude reviews each task — and can validate against the REAL HDHomeRun at 192.168.50.32 (use sparingly: family device, 2 tuners; keep test sessions <60s and always release).**

**Goal:** Live pause/rewind within a settings-backed buffer, and one HDHomeRun tuner per channel regardless of concurrent transcode variants.

**Architecture:** Per spec `docs/superpowers/specs/2026-08-07-bowtie-timeshift-tuner-reuse-design.md` — the **Lifecycle (BINDING)** section of part A is the contract for Tasks 4–5; the review-rewritten semantics (sub-per-process-life, single-flight dial, 5s tail, join buffer, dial-503→tuners-busy) are non-negotiable.

**Tech Stack:** existing stacks; no new dependencies.

## Global Constraints

- All prior Global Constraints bind (lint clean, openapi same-task, camelCase, conventional commits).
- Settings key exact: `streaming.bufferMinutes` (default 15, range 2–60); new optional API section `streaming` per v0.4.0 section-merge rules.
- Timings exact: heartbeat interval 15s; `viewerIdleTimeout` 90s; session empty-grace 60s (unchanged); ingest tail 5s; pump chunk 64 KB; per-sub channel cap 64 chunks; stall force-close >2s; join buffer ≤1 MB; reconnect 1s..30s backoff, give-up 60s.
- Copy exact: out-of-window notice "Jumped to live — paused longer than the buffer".
- Heartbeats use the STREAM TOKEN (query param), not Bearer.
- Verification every task: `cd server && CGO_ENABLED=0 go vet ./... && CGO_ENABLED=0 go test ./... && golangci-lint run`; web tasks add `cd web && npx tsc --noEmit && npx vitest run && npm run build`; client tasks add their platform suites.
- The REAL device (192.168.50.32) is for ORCHESTRATOR validation only — implementer tasks never dial it.

## File Structure (target)

```
server/internal/stream/ingest.go(+_test)       # NEW: IngestManager/IngestSub per spec A
server/internal/stream/manager.go(+_test)      # Attach-per-process-life integration; 503 surface
server/internal/stream/token.go                # (unchanged; heartbeat reuses verify)
server/internal/api/stream_handlers.go(+_test) # heartbeat endpoint
server/internal/transcode/ffmpeg.go(+_test)    # Stdin/pipe:0 + discardcorrupt
server/internal/settings/settings.go(+_test)   # Streaming section
server/internal/api/settings_handlers.go(+_test)
web/src/player/{Player.tsx,SeekBar.tsx,seekModel.ts(+test)}
ios/App/Shared/PlayerModel.swift + SharedTests # heartbeat timer
android/core/.../vm/PlayerViewModel.kt(+test)  # heartbeat ticker
android/tv/.../PlayerKeyHandler.kt(+test)      # DPAD ±30s
roku/components/PlayerScene.(xml|bs)           # heartbeat Timer + queue guard
docs (truenas/compose tmpfs 4g, roku-testing additions, CHANGELOG)
```

---

### Task 1: Heartbeat endpoint + 90s viewer timeout

**Files:**
- Modify: `server/internal/api/stream_handlers.go` (+ route `POST /api/v1/sessions/{viewerId}/heartbeat`), `server/internal/api/server.go` (Routes()), `server/internal/stream/manager.go` (`viewerIdleTimeout` 30s→90s const), `docs/api/openapi.yaml`
- Test: `stream_handlers_test.go`, `manager_test.go` (timeout change)

**Interfaces:** endpoint auths exactly like DELETE (stream token query OR Bearer; reuse `authorizeSessionDelete`-equivalent helper — extract shared `authorizeViewerRequest` if that helper is delete-specific). 204 on success; 404 unknown viewer; touches via existing `Manager.Touch`.

- [x] **Step 1: Failing tests** — heartbeat with stream token → 204 + viewer's lastSeen advances (fake clock via manager); with Bearer → 204; bad token → 401 (A5); unknown viewer → 404. Manager: viewer NOT reaped at 60s with beats, reaped after 90s+ without.
- [x] **Step 2:** FAIL → implement → PASS. openapi. **Step 3:** Commit `feat: session heartbeat endpoint; 90s viewer idle timeout`

### Task 2: `streaming.bufferMinutes` setting + window derivation + web settings field

**Files:**
- Modify: `server/internal/settings/settings.go`(+test: `Streaming()/SetStreaming`, seed default 15), `server/internal/api/settings_handlers.go`(+test: optional `streaming` section, validate 2–60, omitted-section round-trip), `server/internal/stream/manager.go`(+test: hls list size from provider at Start → JobSpec/ffmpeg args; nil-provider fallback 30 as today), `server/internal/transcode/ffmpeg.go`(+test: `JobSpec.HLSListSize int` — 0 → 30 default; goldens), `web/src/admin/Settings.tsx` + `settingsModel.ts`(+test: streaming payload section), `docs/api/openapi.yaml`

**Interfaces:** `settings.Streaming struct { BufferMinutes int }`; `JobSpec.HLSListSize int`.

- [x] **Step 1: Failing tests** per file list (incl. manager: SetStreaming(2) → next session's JobSpec.HLSListSize==30, SetStreaming(15)→225). **Step 2:** implement → PASS. **Step 3:** Commit `feat: settings-backed dvr buffer window`

### Task 3: FFmpeg pipe input + discardcorrupt

**Files:**
- Modify: `server/internal/transcode/ffmpeg.go` (+`JobSpec.Stdin io.Reader`; when set: input args `-fflags +discardcorrupt -i pipe:0` replacing the URL, hw flags unchanged; `Command` sets `cmd.Stdin`), `ffmpeg_test.go` (goldens for all five backends with Stdin set + one URL-mode golden preserved for fallback), `ffmpeg_e2e_test.go` (tagged: pipe-fed transcode from a generated TS via stdin)

- [x] **Step 1: Failing goldens.** **Step 2:** implement → PASS incl. tagged run locally. **Step 3:** Commit `feat: ffmpeg stdin pipe input with corrupt-packet discard`

### Task 4: IngestManager (pure; the review's test bar applies)

**Files:**
- Create: `server/internal/stream/ingest.go`, `ingest_test.go`

**Interfaces (exact, consumed by Task 5):**

```go
var ErrTunersBusy = errors.New("all tuners in use")   // typed for handler mapping
type IngestManager struct{ /* ... */ }
func NewIngestManager(dial func(ctx context.Context, url string) (io.ReadCloser, int, error)) *IngestManager
// dial returns body, httpStatus, err; default impl wraps http.Get. Injectable for tests.
func (im *IngestManager) Attach(ctx context.Context, channelID int64, url string) (*IngestSub, error)
// 503 from dial → ErrTunersBusy immediately (wrapped, errors.Is-able).
type IngestSub struct{ R *io.PipeReader /* + internal */ }
func (s *IngestSub) Close() error
func (im *IngestManager) ActiveChannels() []int64      // for admin UI wiring later
func (im *IngestManager) Shutdown()                     // close all (server stop)
```

Implementation per spec A verbatim: per-channel mutex single-flight; pump 64KB chunks; per-sub chan(64) + drain goroutine → io.Pipe; stall>2s force-Close that sub; PAT/PMT join buffer (parse TS sync bytes 0x47, PID 0 = PAT, PMT PID from PAT; keep last tables + packets since last PAT, cap 1MB) served first to new subs; refcount; last-Close → 5s tail then close body; reconnect backoff on non-503 errors without closing subs, give up 60s.

- [x] **Step 1: Failing tests** (fake dial serving synthetic TS with proper PAT/PMT packets at intervals; fake clock where waits matter): `TestSingleFlightConcurrentAttach` (10 goroutines, 1 dial), `TestFanoutDeliversIdenticalBytes`, `TestJoinBufferGivesLateSubTables` (late sub's first bytes contain PAT+PMT), `TestStalledSubForceClosedOthersFlow`, `TestLastCloseTailThenDialClosed` (5s tail; re-Attach within tail reuses), `TestDial503ErrTunersBusy`, `TestReconnectKeepsSubs` (mid-stream EOF → redial → bytes continue), `TestReconnect503ClosesSubs`, `TestGiveUpAfter60s`.
- [x] **Step 2:** FAIL → implement → PASS + `go test -race ./internal/stream/`. **Step 3:** Commit `feat: per-channel ingest fan-out with single-flight dial and join buffer`

### Task 5: Manager integration (tuner reuse live) + e2e bar

**Files:**
- Modify: `server/internal/stream/manager.go` (+`ManagerDeps.Ingest *IngestManager` — nil → legacy URL mode for fixture compat, production always sets it; session holds `sub *IngestSub`; Attach on every process start incl. `restartSessionLocked`; Close in teardown/Terminate/abandon/proc-death; Start maps `ErrTunersBusy`), `server/internal/api/stream_handlers.go` (503 mapping recognizes ErrTunersBusy), `server/cmd/bowtie/main.go` (construct IngestManager), admin tuners payload gains `ingestChannels` (ActiveChannels) — `admin_handlers.go` + openapi
- Test: `manager_test.go` + `stream_handlers_test.go` e2e additions

- [x] **Step 1: Failing tests** (fake HDHR + stub Runner where suitable; REAL ffmpeg not needed): `TestDualProfileOneDial` (e2e: two sessions different profiles one channel → fake `ActiveStreams()==1` AND fake dial-count==1 AND both playlists serve), `TestCrashTwiceReattaches` (proc Done×2 → 3 Attach calls total, playlist path recovers), `TestCoWatcherSurvivesTerminate` (terminate session A; session B playlist still advances), `TestQualityReplaceKeepsRefcount` (DELETE+create same channel inside debounce window → dial count stays 1), `TestTunerFreeBudget` (fake clock: last session ends → dial closed ≤65s), `TestStartDial503SurfacesTunersBusy` (fake with 0 free tuners → 503 payload shape).
- [x] **Step 2:** FAIL → implement → PASS + `-race`. **Step 3:** openapi (tuners payload). Commit `feat: sessions share per-channel ingest; one tuner per channel`
- [ ] **Step 4 (ORCHESTRATOR, post-review):** short real-device check (<60s, single channel, check family tuner load first via status.json): dual-profile sessions on one channel → device status shows ONE tuner for Bowtie; release immediately.

### Task 6: Web trick-play UI + heartbeats

**Files:**
- Create: `web/src/player/SeekBar.tsx`, `web/src/player/seekModel.ts` + `seekModel.test.ts`
- Modify: `web/src/player/Player.tsx` (seek bar wiring; LIVE badge; skip-back 30s; jump-to-live; out-of-window clamp + notice copy exact; 15s heartbeat interval + visibilitychange-hidden immediate beat via `client.heartbeat(viewerId, streamToken)`), `web/src/api/client.ts` (heartbeat method using the stream token from playlistUrl)

**Interfaces:** `seekModel.ts` pure: `windowFromHls(levelDetails) -> {start,end,live}`, `clampSeek(pos, window) -> {pos, clamped}`, `formatBehind(seconds) -> "-mm:ss"`.

- [x] **Step 1: Failing vitest** — seekModel math (window mapping, clamp true when pos<start, behind formatting), heartbeat scheduling logic if extracted (interval + visibility beat as a testable hook).
- [x] **Step 2:** implement UI → tsc/vitest/build green. **Step 3:** Commit `feat: web live seek bar, pause/rewind, session heartbeats`
- [ ] **Step 4 (ORCHESTRATOR gate):** Playwright + real device (brief): play, pause 2 min, resume; rewind 5 min; skip-back; jump-to-live; screenshots to user.

### Task 7: iOS/tvOS + Android/Fire TV heartbeats & scrubbers

**Files:**
- Modify: `ios/App/Shared/PlayerModel.swift` (+heartbeat: 15s task loop using ManualClock-compatible clock; starts on .playing, stops on stop(); uses client `heartbeat(viewerId:)` added to BowtieKit with stream-token URL derived from playlistUrl), `ios/BowtieKit/Sources/BowtieKit/BowtieClient.swift` (+`heartbeat(viewerId:token:)` — token param, no Bearer), `ios/App/iOS/PlayerView.swift` (verify no `requiresLinearPlayback`; AVPlayer scrubber is native; out-of-window contract: observe seekableTimeRanges — when current position falls below range start, seek to live edge and show the exact notice copy via the overlay), SharedTests + BowtieKit tests
- Modify: `android/core/.../BowtieClient.kt` (+`heartbeat(viewerId, token)`), `android/core/.../vm/PlayerViewModel.kt` (+15s ticker in Playing state, virtual-time test), `android/app/.../PlayerScreen.kt` + `android/tv/.../TvPlayerScreen.kt` (enable controller seek/rewind affordances; out-of-window clamp: on BehindLiveWindowException seek to default position + notice copy), `android/tv/.../PlayerKeyHandler.kt`(+test: DPAD_LEFT/RIGHT → SeekBack30/SeekForward30 actions while controls hidden)

- [ ] **Step 1: Failing tests** — BowtieKit heartbeat request shape (token query, no Authorization on that call); PlayerModel beat cadence via ManualClock; PlayerViewModel ticker via virtual time; key handler ±30s mapping.
- [ ] **Step 2:** implement → all platform suites green (ios sim tests + android unit tests). **Step 3:** Commit `feat: native client heartbeats and live scrubbing`

### Task 8: Roku heartbeat + docs + changelog

**Files:**
- Modify: `roku/components/PlayerScene.(xml|bs)` (15s Timer → enqueue heartbeat kind ONLY if ApiTask queue depth < 3 — ApiTask exposes a `queueDepth` interface field it already can maintain; `bowtie.client.buildRequest` gains "heartbeat" kind using stream token in URL), `roku/components/tasks/ApiTask.bs` (queueDepth field), `roku/source/lib/BowtieClient.bs` + fixtures (heartbeat request build test in SelfTestScene fixtures)
- Modify: `docs/deploy/truenas.md` + `deploy/docker-compose.yml` (tmpfs 2g→4g with buffer math), `docs/deploy/roku-testing.md` (+pause-3min step, +buffer-clamp step with temporarily-lowered buffer, +REW-probe experimental step), `CHANGELOG.md` (0.5.0 entry), README feature list line
- Verification adds: `cd roku && npm ci && npx bsc && npx bslint --severity error && npm run package`

- [ ] **Step 1:** implement + fixture; all verifications green. **Step 2:** Commit `feat: roku heartbeats; dvr docs and changelog`
- [ ] **Step 3 (ORCHESTRATOR):** tag v0.5.0 → release watch → assets check.

## Post-plan notes
- Sequential 1→8 on main. Tasks 4 and 5 are the risk core — line review is deep there; `-race` mandatory.
- Real-device etiquette: check `status.json` for family usage BEFORE any live test; sessions <60s; always DELETE.

---

# REVIEW AMENDMENTS (BINDING — override task text above where they conflict)

Incorporated from the Grok plan review, 2026-08-07.

## A1. Ingest clock seam (Tasks 4-5)
`NewIngestManager(dial DialFunc, opts ...IngestOption)` with `WithIngestClock(now func() time.Time, after func(time.Duration) <-chan time.Time)`. ALL timing tests use the fake clock/after — wall sleeps ≥1s in tests are a task failure. Task 5 injects the SAME fake clock into Manager and Ingest; `TestTunerFreeBudget` advances that shared clock.

## A2. Join-buffer falsifiability (Task 4)
The plan requires a small synthetic TS builder (test helper): emits valid 188-byte packets; PAT (PID 0) + PMT emitted ONCE at stream start, then MEDIA-ONLY packets thereafter. `TestJoinBufferGivesLateSubTables`: attach late (after ≥1 MB of media-only), assert the sub's first bytes contain the PAT and PMT packets (byte-compare against the builder's table packets) BEFORE any media — provable prepend, not table-cycle luck. A second variant with cycling tables may exist but is not the falsifier.

## A3. Observability (Tasks 4-5)
Test instrumentation is explicit: a counting dial wrapper exposing `DialCalls()`; ingest test hook `AttachCalls()` (test-only counter via option). `hdhrfake.ActiveStreams()` is used ONLY in e2e where dial is real HTTP to the fake; `hdhrfake.Fake` gains `TotalDials()` (cumulative) for the e2e dial assertions. Crash×2 asserts AttachCalls==3 AND TotalDials==1 (re-attach during tail must not redial).

## A4. Manager integration precision (Task 5)
- Test-suite DEFAULT is non-nil Ingest with the counting dial; nil-Ingest legacy mode is exercised by exactly ONE explicit test and exists only as fixture compatibility during this task — REMOVED at the end of Task 5 (production and tests both always have Ingest; session.inputURL is replaced by session.sub).
- Ordered contract (spelled in code comments): on every process start: Close(old sub if any) → Attach → JobSpec.Stdin=sub.R → runner.Start. On proc-death: Close sub (tail absorbs quick restarts... note: restart backoff can exceed the 5s tail — that redial is CORRECT and expected; the tail exists for sub-5s churn, assert both behaviors). On abandon/waitPlaylist-failure/Terminate/teardown: Close.
- 503 mapping via `errors.Is(err, stream.ErrTunersBusy)` in writeStartError (replace the string-contains check).
- Admin UI for ingest channels: payload-only this cycle (`ingestChannels`); no web UI task.
- IngestSub exposes `R io.ReadCloser` (not *io.PipeReader); Close only via IngestSub.Close. Add tests: double-Close safe; concurrent Close vs force-close safe; Shutdown vs concurrent Attach safe.

## A5. Task 1 corrections
Helper extracted as `authorizeViewerRequest`; heartbeat auth-failure is **401** (mirrors DELETE exactly); update test list and openapi accordingly.

## A6. Heartbeat ownership (Tasks 6-7)
Beats are keyed on SESSION OPEN (viewerId present and not torn down) — they continue through paused AND `.stalled`/`Stalled` states; they stop only on real leave (stop()/unmount/back). Web: the new visibilitychange listener sends ONE beat on hidden and NEVER tears down. Native tests: advance virtual time through a stall transition and assert beats continue.

## A7. Roku queueDepth (Task 8)
`queueDepth` is a NEW ApiTask interface field (xml + task-loop maintenance on push/pop; task thread writes, scenes read). Explicit contract change with fixture coverage, not an existing capability.

## A8. Small precision items
- Task 6 seekModel input is an adapter type `LiveWindow {start, end, liveEdge, current}` built in Player.tsx from hls.js `levelDetails` + `hls.liveSyncPosition` + `video.currentTime`; seekModel functions take LiveWindow (pure, testable) — not raw hls.js objects.
- Task 2: goldens for BOTH default (30) and non-default (225) list sizes across backends; web settingsModel section union gains 'streaming' as a full peer.
