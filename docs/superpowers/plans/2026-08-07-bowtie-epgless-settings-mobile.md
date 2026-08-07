# Bowtie v0.4.0 — EPG-less Watching + Admin Settings + Mobile Web Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax. **This session: Grok implements sequentially on `main`; Claude reviews each task.**

**Goal:** Channels are watchable and testable with zero EPG configured; every product-level setting is editable in Admin → Settings (DB-backed, restart-free); the web app works properly on phones.

**Architecture:** Per spec `docs/superpowers/specs/2026-08-07-bowtie-epgless-and-settings-design.md` (Grok-review-amended sections are BINDING: always-on EPG supervisor, presence-based seeding, PUT section-merge, provider-wired /admin/transcode, 503 filtering, SD Lineups + error table, Settings-tab layout, mobile section C).

**Tech Stack:** existing Go/React stacks; no new dependencies server-side or web-side.

## Global Constraints

- All Phase 1 Global Constraints still bind (CGO off, camelCase JSON, conventional commits, golangci-lint clean, openapi.yaml updated with any route/behavior change in the same task).
- Settings keys (exact strings): `xmltv.source`, `xmltv.refreshHours`, `sd.username`, `sd.password`, `sd.lineupId`, `transcode.encoder`, `transcode.allowHevc`.
- Presence-based seeding ONLY (`HasSetting`); stored "" is a value, never re-seeded.
- Copy (exact): zero-enabled-channels admin: "No channels enabled yet. Add your HDHomeRun and enable channels in Admin → Channels." viewer: "No channels enabled yet. Ask your admin to enable some channels." program-less cell: "No guide data — press to watch". Save toast: "Saved."
- Mobile: breakpoint 640px; touch targets ≥ 44px; no page-level horizontal scroll (wide content scrolls in its own container).
- Verification every task: `cd server && CGO_ENABLED=0 go vet ./... && CGO_ENABLED=0 go test ./... && golangci-lint run` plus, for web tasks, `cd web && npx tsc --noEmit && npx vitest run && npm run build`.

## File Structure (target)

```
server/internal/settings/settings.go(+_test)   # NEW: typed provider + seeding
server/internal/store/store.go                 # + HasSetting
server/internal/epg/service.go(+_test)         # supervisor redesign; provider-driven
server/internal/epg/sd/client.go(+_test)       # + Lineups(ctx)
server/internal/stream/manager.go(+_test)      # provider-driven encoder/allowHevc; admin bypass
server/internal/api/settings_handlers.go(+_test)  # NEW: GET/PUT settings, lineups
server/internal/api/stream_handlers.go(+_test) # 503 filtering; admin-disabled-channel e2e
server/internal/api/admin_handlers.go          # transcode endpoint → provider
web/src/admin/Settings.tsx(+ model/test)       # NEW tab
web/src/admin/{Admin,Epg,Channels}.tsx         # nav, slimdown, Preview button
web/src/guide/Guide.tsx(+ test)                # empty-state split; watchable no-data cell
web/src/global.css + admin/guide/player css    # mobile breakpoint work
ios/.../ChannelListView.swift, ChannelRailView.swift  # copy alignment only
docs/deploy/{truenas.md,remote-access.md}, deploy/docker-compose.yml, README.md, CHANGELOG.md
```

---

### Task 1: Settings provider + presence-based seeding

**Files:**
- Create: `server/internal/settings/settings.go`, `settings_test.go`
- Modify: `server/internal/store/store.go` (+ `HasSetting`), `store_test.go`, `server/cmd/bowtie/main.go` (construct provider + seed after store open)

**Interfaces (Produces — exact):**

```go
// store
func (s *Store) HasSetting(key string) (bool, error)   // true even when value == ""

// settings
type Provider struct{ /* store-backed; no in-memory cache */ }
func NewProvider(st *store.Store) *Provider
type XMLTV struct { Source string; RefreshHours int }
type SD struct { Username, Password, LineupID string }
type Transcode struct { Encoder string; AllowHEVC bool }
func (p *Provider) XMLTV() (XMLTV, error)
func (p *Provider) SD() (SD, error)
func (p *Provider) Transcode() (Transcode, error)
func (p *Provider) SetXMLTV(v XMLTV) error
func (p *Provider) SetSD(v SD) error                    // full section write
func (p *Provider) SetTranscode(v Transcode) error
func (p *Provider) SeedFromConfig(cfg config.Config) error
// Seed: for each key, if !HasSetting → SetSetting from cfg (defaults: refreshHours 12,
// encoder "auto", allowHevc false). If HasSetting and cfg value differs → log.Printf notice.
```

- [x] **Step 1: Failing tests** — store: `TestHasSettingDistinguishesEmptyFromAbsent` (set key to "" → Has=true; unknown → false). settings: `TestSeedOnlyWhenAbsent` (seed cfg with xmltv source; call SeedFromConfig twice; SetXMLTV(Source:"") between → second seed must NOT restore source — the spec's disable-survives-restart test); `TestTypedRoundTrips` (ints/bools through string storage); `TestDefaultsSeeded` (empty cfg → encoder "auto", refreshHours 12, allowHevc false present in DB).
- [x] **Step 2:** FAIL → implement → PASS. Wire `SeedFromConfig` into main.go right after store open (before epg/stream construction).
- [x] **Step 3:** Commit `feat: db-backed settings provider with presence-based seeding`

### Task 2: EPG always-on supervisor

**Files:**
- Modify: `server/internal/epg/service.go`, `service_test.go`, `server/cmd/bowtie/main.go` (pass provider into NewService; REMOVE cfg XMLTV/SD plumbing from epg)

**Interfaces:**
- Consumes: `settings.Provider` (XMLTV()/SD()).
- Produces: `NewService(st *store.Store, prov *settings.Provider, /* keep clock/http/sd injection as-is */)`. `Run(ctx)` becomes two always-on supervisor loops:

```
loop(source):
  for {
    cfg := provider.<source>()            // re-read EVERY iteration
    if !configured(cfg): sleepOrDone(60s); continue
    refresh(source)                        // errors recorded in Status as today
    sleepOrDone(withJitter(interval(cfg))) // interval re-read next iteration anyway
  }
```

`Status()` computes `Configured` from the provider LIVE (not boot state). `RefreshAll` reads the provider (already per-call after this change).

- [x] **Step 1: Failing tests** (injectable clock; provider over a temp store): `TestSupervisorStartsSourceEnabledAtRuntime` (boot unconfigured; advance past a poll tick; SetXMLTV(source) → next tick refreshes — assert via stub fetcher); `TestSupervisorStopsWhenCleared` (configured → refresh happens; clear → subsequent ticks fetch nothing, NO error status accumulation); `TestIntervalChangeApplies` (refreshHours 12→1 → next sleep is ~1h with jitter bounds).
- [x] **Step 2:** FAIL → implement → PASS (all existing epg tests updated to construct via provider).
- [x] **Step 3:** Commit `feat: epg supervisor honors runtime settings without restart`

### Task 3: Stream manager + transcode endpoint on the provider; admin preview + 503 filtering

**Files:**
- Modify: `server/internal/api/server.go` (**Deps gains `Settings *settings.Provider` in THIS task** — Task 4 only adds routes), `server/internal/stream/manager.go`, `manager_test.go` (ManagerDeps: + `Settings *settings.Provider`; Negotiate uses provider Encoder/AllowHEVC per Start; `Start` skips the `!ch.Enabled` rejection when `user.Role == "admin"`), `server/internal/api/admin_handlers.go` (transcode endpoint `selected` from provider), `server/internal/api/stream_handlers.go` + test (503 payload: non-admin gets only enabled-channel sessions — needs channel-enabled lookup; add `EnabledOnly bool` filtering helper in handler using store), `docs/api/openapi.yaml`
- Test: extend `manager_test.go` + `stream_handlers_test.go`

- [x] **Step 1: Failing tests** — manager: `TestEncoderSettingAppliesPerSession` (stub runner; SetTranscode(encoder software→forced-different) between two Starts → second JobSpec differs); `TestAdminCanStartDisabledChannel` + `TestViewerDisabledChannel404` (fake HDHR + disabled channel). handlers: `TestTunersBusyFilteredForViewers` (two sessions, one on disabled channel; viewer 503 lists 1, admin 503 lists 2); e2e `TestAdminPreviewDisabledChannelE2E` (login admin → POST session for disabled channel → playlist 200).
- [x] **Step 2:** FAIL → implement → PASS.
- [x] **Step 3:** Update openapi (sessions POST description: admin-may-preview-disabled; 503 shape note). Commit `feat: runtime transcode settings; admin preview of disabled channels`

### Task 4: Settings API + SD Lineups

**Files:**
- Create: `server/internal/api/settings_handlers.go`, `settings_handlers_test.go`
- Modify (Deps.Settings already exists from Task 3): `server/internal/epg/sd/client.go` + test (+ `Lineups(ctx) ([]LineupSummary, error)`; `type LineupSummary struct { LineupID string `+"`json:\"lineup\"`"+`; Name string; Location string; Transport string }` — verify wire keys against the SD wiki as in Phase 1), `server/internal/api/server.go` (routes + Deps.Settings), `docs/api/openapi.yaml`

Endpoints per spec EXACTLY (section-merge PUT with pointer sections; password keep-on-empty; clear-SD-via-empty-username clears username+password+lineupId; encoder validated against probe `available` + "auto"; refreshHours 1–168; xmltv.source "" | http(s) URL | absolute path; lineups error table 422/401/502).

- [x] **Step 1: Failing tests** — GET shape (passwordConfigured true/false, never password; available from injected probe); PUT merge (transcode-only PUT leaves SD untouched; xmltv-only PUT leaves transcode); password semantics (empty keeps; new value replaces; empty username clears trio); validation each rule; lineups: no-creds 422, SD 401 → 401, SD down → 502 (httptest fake SD).
- [x] **Step 2:** FAIL → implement → PASS. **Step 3:** openapi. Commit `feat: admin settings api with sd lineup listing`

### Task 5: Web — Settings tab, EPG slimdown, Preview, empty states, watchable no-data cells

**Files:**
- Create: `web/src/admin/Settings.tsx`, `web/src/admin/settingsModel.ts` + `settingsModel.test.ts`
- Modify: `web/src/admin/Admin.tsx` (nav + route), `web/src/admin/Epg.tsx` (remove nothing yet except ensure ONLY status+refresh remain), `web/src/admin/Channels.tsx` (▶ Preview per row → navigate to player with channelId), `web/src/api/client.ts` (settings GET/PUT, lineups), `web/src/guide/Guide.tsx` + `guideModel.test.ts` (empty-state split per exact copy; program-less rows render one full-width clickable cell "No guide data — press to watch")

settingsModel.ts (pure, tested): form state ↔ API payload mapping incl. section-merge payload construction (only the section being saved), password-field rules (empty → omit), lineup option mapping, validation mirrors (client-side hints only; server is authority).

- [x] **Step 1: Failing tests** — settingsModel: payload-per-section, password omit/include, clear-SD path (empty username sends section with empty strings). guideModel/Guide logic: empty-state selector (channels null vs [] vs programs-empty) returns the right variant + copy.
- [x] **Step 2:** Implement UI; typecheck/vitest/build green.
- [x] **Step 3:** Commit `feat: web settings tab, channel preview, epg-less guide affordances`

### Task 6: Web mobile pass (spec section C)

**Files:**
- Modify: `web/src/global.css`, `web/src/admin/Admin.module.css` (+ any per-screen module css), `web/src/guide/Guide.module.css`, `web/src/player/Player.tsx/.module.css` (quality bottom sheet on narrow), `web/src/admin/*.tsx` where card-collapse needs data-label attributes

Approach (CSS-driven): tables get `data-label` on cells; `@media (max-width: 640px)` turns rows into cards (`display:block`, label::before). Admin nav becomes overflow-x auto pill row. Guide: verify sticky column + `-webkit-overflow-scrolling: touch`; header wraps. Player quality menu renders as bottom sheet under 640px. All buttons/inputs min-height 44px under 640px.

- [x] **Step 1:** Implement; typecheck/vitest/build green (logic untouched — this is CSS + small markup).
- [x] **Step 2:** Commit `feat: mobile-friendly web viewer and admin`
- [ ] **Step 3 (orchestrator, post-commit):** Playwright pass at 390×844 and 1280×800 against a live dev server: Login, Guide (empty + populated states), Player chrome, all five admin tabs + Settings; screenshots sent to user; defects → fix round.

### Task 7: Docs, iOS copy alignment, release

**Files:**
- Modify: `docs/deploy/truenas.md`, `deploy/docker-compose.yml` comments, `README.md` (settings now in Admin → Settings; env keys = first-boot seeds), Create `CHANGELOG.md` (v0.4.0 entry incl. the seeds-not-overrides breaking note), `ios/App/iOS/ChannelListView.swift` + `ios/App/tvOS/ChannelRailView.swift` ("Nothing on now" → "No guide data"), plan checkboxes/PROGRESS.md
- Verification adds: `cd ios && xcodegen generate && swift test --package-path BowtieKit` + iOS sim test suite.

- [x] **Step 1:** Docs + copy changes; full multi-platform verification (server, web, ios tests; android/roku untouched).
- [x] **Step 2:** Commit `docs: settings control plane docs; ios guide copy alignment; v0.4.0 changelog`
- [ ] **Step 3 (orchestrator):** tag v0.4.0 → release watch → asset check.

## Post-plan notes
- Sequential on `main` (each task consumes the previous; no parallel tracks needed at this size).
- Orchestrator Playwright mobile review after Task 6 is a GATE before Task 7.

---

# REVIEW AMENDMENTS (BINDING — override task text above where they conflict)

Incorporated from the Grok plan review, 2026-08-07.

## A1. Task 2 — testable supervisor seam (replaces "keep clock injection as-is")
`Service` gains an injectable `after func(time.Duration) <-chan time.Time` (default `time.After`) AND records each computed wait (`lastWait[source]` guarded, or a test hook channel emitting the chosen duration). Supervisor tests drive loops via the injected after-channel (close/send to advance) and assert: refresh call counts via stub fetchers, and recorded wait durations within jitter bounds (interval±10%) — never real sleeps. Add test `TestRefreshAllReadsProviderLive` (RefreshAll picks up a source set AFTER service construction) — covers the hot-read path separately from the loops.

## A2. Task 4 — SD error mapping (replaces "SD 401 → 401")
Admin-API mapping table: creds missing → 422; SD AUTH-CLASS failure → 401 — auth-class means: `apiError` with code 4003/INVALID_USER, or Token() rejection (including HTTP-200-with-nonzero-code token responses); transport errors/timeouts/SD 5xx → 502. Tests use the as-built fake SD's 400+code-4003 behavior for the 401 case.

## A3. Task 4 — write atomicity
Validate ALL present sections fully BEFORE any write. Then apply all keys of the request in ONE store transaction: store gains `SetSettings(map[string]string) error` (single tx upsert-all); provider section setters build maps and call it; the PUT handler composes one map across sections. Partial application is a specified bug with a test (invalid transcode + valid xmltv in one PUT → NOTHING written).

## A4. Task 3 — wiring completeness
- `ManagerDeps.Settings` is nil-safe: nil → fall back to `m.cfg.Encoder`/`m.cfg.AllowHEVC` (keeps every existing fixture compiling and green); production main.go always passes the provider.
- `writeStartError` gains the caller (signature: `writeStartError(w, err, user store.User)`) for role-based 503 filtering; filtering uses ONE `ListChannels(true)` enabled-ID set per 503, not per-session lookups.

## A5. Task 5 — Preview navigation wiring
`App.tsx` is in scope: `Admin` gains `onPreview(target: WatchTarget)` prop (WatchTarget = the existing `watching` shape: channelId, guideNumber, name); Shell passes `(t) => setWatching(t)`; Player Back returns to Guide (accepted simplification — document in the commit body). Channels.tsx Preview builds the target from the row.

## A6. Task 6 — quality bottom sheet is a real component
Under 640px the player quality control is a custom sheet (button opens overlay + option list, focus-trapped, Escape/scrim closes, aria-modal), replacing the native `<select>` at that breakpoint (desktop keeps the select). This is component work, not CSS — "logic untouched" applies to the rest of Task 6 only. Card-collapse applies to Channels + Users tables (Sessions/Tuners are already card grids — verify at review, no table work there).

## A7. Task 5 — guide scope confirmation
Guide.tsx already renders clickable full-width empty cells and splits loading/error/empty; the work is exactly: copy updates ("No guide data — press to watch"), zero-enabled-channels copy split by role (admins get the Admin → Channels link), and an extracted pure empty-state selector with tests.
