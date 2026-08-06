# Bowtie Phase 3 — Roku Channel Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [x]`) syntax. **This session: Grok implements on branch `track/roku`; Claude reviews each task. NO DEVICE is available — compile/lint are the only automated gates; the user validates on-device at the end.**

**Goal:** A BrighterScript SceneGraph channel implementing the Bowtie viewer (connect → login → rail → play) with an auth-actor ApiTask, pure-logic libraries testable on-device via SelfTestScene, and a sideloadable zip on GitHub releases.

**Architecture:** Per spec `docs/superpowers/specs/2026-08-06-bowtie-phase3-tv-apps-design.md` — especially the **Roku Auth actor** and **packaging pipeline** sections, which are BINDING. All HTTP flows through ONE ApiTask; risky logic lives in pure functions.

**Tech Stack:** BrighterScript (`brighterscript` pinned in package-lock), bslint, SceneGraph, node 22 for tooling only.

## Global Constraints

- Phase 2 shared behavior contracts bind (auth flow, session lifecycle, error copy, caps, quality filtering) as adapted by the Phase 3 spec.
- ALL HTTP in the single ApiTask; scenes communicate via its queue/response fields. `SetCertificatesFile("common:/certs/ca-bundle.crt")` + `InitClientCertificates()` on every roUrlTransfer.
- Pure logic (AuthState, GuideLogic, Caps decisions, request builders/parsers) has NO I/O and is exercised by SelfTestScene fixtures (mirroring iOS/Android test fixtures).
- PlayerScene recovery: auth-error allowlist only (constant `AUTH_ERROR_PATTERNS` — initially conservative: HTTP 403 surfaced codes; the on-device token-kill step captures real codes to extend it); other errors = bounded retry (1s/2s/4s ×3) WITHOUT new sessions.
- Packaging: `bsc` → `out/staging` (transpiled `.brs` + manifest + components + images ONLY) → zip staging CONTENTS at zip root. Never `.bs`, `node_modules`, configs in the zip.
- Design: palette constants (#101418 bg, #F0A428 amber focus/accents, #F2EFE8 text, #9BA5AE dim); system font; channel numbers large+bold; `focusBitmapUri` amber 9-patch for rail focus.
- Copy identical to other platforms (sentence case; exact error strings from Phase 2 spec).
- Worktree rules: branch `track/roku`; touch `roku/**`, ci.yml (roku job, Task 1), release.yml (roku upload, Task 5), `docs/deploy/roku-testing.md` (Task 5; if Fire TV section exists, preserve it), root README Apps line. Evidence in commit bodies.
- Verification every task: `cd roku && npm ci && npx bsc && npx bslint --severity error` then the staging/zip check once Task 1 lands: `test -f out/bowtie-roku.zip && unzip -l out/bowtie-roku.zip | grep -q "^.*manifest$"`.

---

### Task 1: Project scaffold, toolchain, CI, packaging pipeline

**Files:**
- Create: `roku/package.json` (+lockfile; brighterscript + @rokucommunity/bslint pinned), `roku/bsconfig.json` (strict, `stagingDir: "out/staging"`... use bsc's `stagingDir`/`retainStagingDir` options — verify exact option names against bsc docs and pin behavior with an npm script `npm run package` that produces `out/bowtie-roku.zip` with contents at root), `roku/manifest` (title=Bowtie, ui_resolutions=fhd, mm_icon_focus_hd + splash pointing at images/), `roku/images/*` (generated PNGs: solid #101418 with amber "Bowtie" — a python3/Pillow or plain-PNG script; commit the PNGs, not the script), `roku/source/main.bs` (boots AppScene), `roku/components/AppScene.(xml|bs)` (renders "Bowtie" on token background), `roku/README.md` (dev-mode + sideload basics; expanded in Task 5)
- Modify: `.github/workflows/ci.yml` (+ `roku` job: node 22, `npm ci`, `npx bsc`, `npx bslint --severity error`, `npm run package`, upload `out/bowtie-roku.zip` artifact), root `README.md` Apps line, `Makefile` (`roku-package` target)

- [x] **Step 1:** Scaffold; `npm run package` produces a zip whose root listing shows `manifest`, `source/`, `components/`, `images/` and NO `.bs`/`node_modules`/config files (assert with `unzip -l`).
- [x] **Step 2:** CI-equivalent commands green locally. **Step 3:** Commit `feat: roku channel scaffold with brighterscript toolchain and packaging`

### Task 2: Pure libraries + SelfTestScene

**Files:**
- Create: `roku/source/lib/{AuthState.bs,GuideLogic.bs,Caps.bs,Registry.bs,BowtieClient.bs}`, `roku/components/SelfTestScene.(xml|bs)`, `roku/source/tests/{AuthStateFixtures.bs,GuideLogicFixtures.bs,ClientFixtures.bs}`

**Interfaces (BrighterScript; namespaces):**

```brighterscript
namespace bowtie.auth
  ' Pure state machine: state AA {phase, accessToken, refreshToken, pendingRetry} + event → {state, actions[]}
  ' events: "boot", "loginOk", "response401", "refreshOk", "refreshFail", "signOut"
  ' actions: "doRefresh", "persistRefreshToken", "retryPending", "clearAndSignOut", "none"
  function reduce(state as object, event as object) as object
end namespace
namespace bowtie.guide
  function nowNext(programs as object, atIso as string) as object   ' {now, next} same boundary semantics as other platforms
  function allowedProfiles(maxQuality as string) as object
end namespace
namespace bowtie.caps
  function detect(canHevc as boolean, uhd as boolean) as object     ' pure; ClientCaps AA
  function current() as object                                      ' roDeviceInfo.CanDecodeVideo({Codec:"hevc"}).Result + GetVideoMode()
end namespace
namespace bowtie.client
  function buildRequest(kind as string, params as object, accessToken as string, baseUrl as string) as object ' {url, method, headers, body}
  function parseResponse(kind as string, httpCode as integer, body as string) as object ' {ok, data|error} incl. 503 sessions[], 422 message
end namespace
namespace bowtie.registry
  function loadServer() as dynamic
  function loadRefreshToken() as dynamic
  function save(server as dynamic, refreshToken as dynamic)
end namespace
```

SelfTestScene: runs fixture suites against `reduce`/`nowNext`/`allowedProfiles`/`buildRequest`/`parseResponse` (fixtures literal-translated from the iOS/Android test cases, incl. the full-wire 503 body and the single-flight sequence: 401 → doRefresh → persistRefreshToken BEFORE retryPending; queued-behind-refresh request never signs out), renders `PASS n/n` or failing case names. Launched when `main.bs` sees launch arg `selftest=1` (`ExternalControl` launch params — document `curl "http://<roku-ip>:8060/launch/dev?selftest=1"` in README).

- [x] **Step 1:** Write fixtures FIRST (they're the tests), then implement libs until a manual trace of each fixture passes; bsc strict + bslint green.
- [x] **Step 2:** Commit `feat: roku pure libraries with on-device self-test scene`

### Task 3: ApiTask auth actor + Connect/Login scenes

**Files:**
- Create: `roku/components/tasks/ApiTask.(xml|bs)`, `roku/components/{ConnectScene,LoginScene}.(xml|bs)`
- Modify: `AppScene` (phase routing: registry server? token? → connect/login/home)

ApiTask: interface fields `request` (AA queue append), `response` (AA), `authEvent` (signOut notifications). Internal loop: pop queue → bowtie.client.buildRequest → roUrlTransfer (certs per Global Constraints; 10s timeout) → on 401 run bowtie.auth.reduce-driven refresh (persist via bowtie.registry BEFORE retry) → respond. Scenes observe `response`.
Connect: keyboard dialog for URL, /healthz validate (2s), error copy exact. Login: username/password keyboard dialogs, error copy exact; on success ApiTask holds tokens, AppScene → HomeScene.

- [x] **Step 1:** Implement; bsc+bslint+package green. **Step 2:** Commit `feat: roku api task auth actor, connect and login scenes`

### Task 4: HomeScene rail + SettingsScene

**Files:**
- Create: `roku/components/{HomeScene,SettingsScene}.(xml|bs)`, amber focus 9-patch `roku/images/focus_amber.9.png`

HomeScene: MarkupList/RowList of channels — big bold number, name, now-title + progress, next dim (data: channels + guide via ApiTask; join with bowtie.guide.nowNext; refresh on show + 5-min timer). Select → PlayerScene (Task 5 target; this task navigates to a stub scene showing channel name + back). Settings entry row. SettingsScene: server info, change server (confirm → clear registry → ConnectScene), change password (ApiTask), sign out.

- [x] **Step 1:** Implement; verification green. **Step 2:** Commit `feat: roku channel rail and settings`

### Task 5: PlayerScene + release wiring + validation docs

**Files:**
- Create: `roku/components/PlayerScene.(xml|bs)`
- Modify: `.github/workflows/release.yml` (roku packaging + upload `bowtie-roku-<version>.zip` — inside a job that `needs: [goreleaser]`; folding into the android job is acceptable), `docs/deploy/roku-testing.md` (the 10-step adversarial gate from the spec, verbatim steps incl. SelfTestScene, zap storm, auth race, token-kill code capture, HTTPS; PRESERVE any existing Fire TV section), `roku/README.md` (full), root README release-assets list

PlayerScene: Video node (content: absolute playlistUrl WITH token; streamFormat hls), session create/delete via ApiTask (caps from bowtie.caps.current(), profile from quality selection filtered by maxQuality), OK play/pause, back stop+return, up/down zap with 400ms debounce (timer-based, cancel in-flight via request generation counters), quality dialog, debug overlay (dev flag) showing Video state/errorCode/errorStr for the token-kill capture step, recovery per Global Constraints (allowlist-only recreate; bounded retry otherwise).

- [x] **Step 1:** Implement; full verification + zip contents assert. **Step 2:** Commit `feat: roku player with session lifecycle; release zip and validation gate docs`

## Post-plan notes
- Sequential 1→2→3→4→5. Claude line-reviews EVERY task (no runtime net — review is the gate).
- After merge + release tag, the user runs docs/deploy/roku-testing.md on their 4K Roku; findings (esp. captured Video error codes) come back as a fix round.

---

# REVIEW AMENDMENTS (BINDING — these override the task text above where they conflict)

Incorporated from the Grok plan review, 2026-08-06. Every task prompt references this section.

## A1. ApiTask interface contract (replaces Task 3's field sketch)
- `request`: interface field of type assocarray with **`alwaysNotify="true"`**, payload `{id: string, kind: string, params: object}`. Scenes write ONE request AA per call (never mutate arrays in place — field reassignment is the only observer-safe write). ApiTask maintains an INTERNAL FIFO; the interface field is just the enqueue signal.
- `response`: assocarray `{id, ok as boolean, data|error}` — scenes match on their own `id` (Home guide refresh and Player session ops must not steal each other's responses).
- `authEvent`: signals forced sign-out.
- Task thread uses **port-form observeField** (message loop), never render-thread callbacks.
- ApiTask is the SOLE holder of the access token and the `user` object (incl. `maxQuality`) from login/refresh responses; it injects Bearer on every authed kind. Add `getUser`-style response or include `user` in login/refresh responses so scenes can read maxQuality.
- `buildRequest` kinds include `logout` (best-effort POST + clear registry) — sign-out flows through ApiTask.
- Per-request timeout override: `healthz` uses 2s (Phase 2 contract); default 10s otherwise.

## A2. Boot-rotate (replaces AppScene routing in Task 3; extends AuthState fixtures in Task 2)
Routing: registry has server? no → Connect. Server but no REFRESH token → Login. Refresh token present → **Checking phase: enqueue `doRefresh` — NEVER straight to Home**. Refresh ok (persist new refresh BEFORE anything else; hold access+user in ApiTask) → Home. Refresh fail → Login (server kept). AuthState fixtures MUST include the boot table: boot+refresh→doRefresh; refreshOk→persist+home; refreshFail→login-not-clear-server; response401-with-no-refresh-token→clearAndSignOut.

## A3. Session-replace algorithm (replaces Task 5's "generation counters" sentence)
On zap/quality change: (1) `gen++`; (2) enqueue DELETE for the CURRENT viewerId immediately; (3) debounce 400ms; (4) enqueue create; (5) when a create response arrives for a STALE gen, enqueue DELETE for that orphan viewerId before discarding. Back/stop: DELETE then leave. Sign-out while player alive: player teardown (DELETE) before ApiTask logout. "Ignoring a late response" is never sufficient — the server-side viewer must be deleted.

## A4. Playlist URL resolution
`parseResponse("createSession")` returns `playlistUrl` resolved ABSOLUTE against the server base (path+query preserved). Fixture asserts an absolute URL containing `?token=`. PlayerScene ContentNode: `url` = that absolute URL, `streamFormat = "hls"`, `Live = true`.

## A5. PlayerScene error matrix (extends Task 5)
- 503 → "All tuners are in use — someone else is watching. Try again in a few minutes." + who's-watching list from `sessions[]` + retry button (validation step 8 depends on this).
- 422 → reset quality to Auto and retry ONCE; second 422 → device-can't-play copy.
- 404 → refresh channel list signal + inform.
- Mid-play recovery allowlist: seeded **EMPTY** and keyed on **Video node `errorCode`/`errorMsg`** (NOT HTTP statuses — the Video pipeline never surfaces raw 403). Until the token-kill capture step populates it, mid-play failures take the bounded-retry path (1s/2s/4s ×3, NO new sessions) then error UI. The debug overlay must display errorCode+errorMsg verbatim for capture.

## A6. Packaging pinned (replaces Task 1's bsconfig sentence)
bsconfig: `stagingDir: "out/staging"`, `retainStagingDir: true`, `createPackage: false`, `files: ["manifest", "source/**/*", "components/**/*", "images/**/*"]` (source/tests INCLUDED deliberately — SelfTestScene needs fixtures on-device). `npm run package` = `bsc && cd out/staging && zip -r ../bowtie-roku.zip .`. CI asserts: `unzip -l` shows `manifest` at root and NO `.bs` files. Manifest keys by name: `mm_icon_focus_hd` (336x210), `mm_icon_focus_sd`, `splash_screen_fhd` (1920x1080), `splash_screen_hd` (1280x720). Release: **separate `roku` job** with `needs: [goreleaser]`, uploads `bowtie-roku-${GITHUB_REF_NAME#v}.zip`.

## A7. SceneGraph component picks (binding)
Keyboard entry: **StandardKeyboardDialog** (secure mode for password). Rail: **MarkupList** + custom item component (single vertical list; not RowList), `focusBitmapUri` on the MarkupList. Launch args: `sub Main(args as object)` checks `args.selftest = "1"` → SelfTestScene (document the curl line). AppScene phase changes must never orphan a live viewer (see A3).
