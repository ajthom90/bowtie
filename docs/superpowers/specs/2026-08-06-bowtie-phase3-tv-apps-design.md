# Bowtie — Phase 3 Design: Fire TV + Roku Apps

**Date:** 2026-08-06
**Status:** Approved (pending implementation)
**Depends on:** v0.2.0 — API frozen at `docs/api/openapi.yaml`; Android `:core` (minSdk 25); design tokens; viewer behavior contracts from the Phase 2 spec (`2026-08-04-bowtie-phase2-native-apps-design.md` — shared-behavior sections apply verbatim unless overridden here).

## Decisions of record

| Topic | Decision |
|---|---|
| Build order | Parallel worktrees: `track/firetv`, `track/roku` |
| Fire TV | New `:tv` Gradle module, **minSdk 25** (Fire OS 6+), Compose for TV (`androidx.tv:tv-material`), Media3; reuses `:core` wholesale |
| ViewModel refactor | VM CLASSES + their tests move `:app` → `:core` (lifecycle-viewmodel added there). ViewModel FACTORIES and DI wiring stay in each application module (`:app`'s in Nav.kt as-is; `:tv` gets its own). CI test expectations update in the same commit. |
| Roku | BrighterScript (typed, transpiled) SceneGraph channel in `roku/`; CI = `bsc` compile + bslint; NO runtime tests possible (no device/emulator) |
| Roku validation | User-executed on-device script (`docs/deploy/roku-testing.md`) on their 4K (HEVC-capable) Roku; gate before family rollout |
| Distribution | Fire TV: `:tv` release APK (same keystore) on GitHub releases (`bowtie-tv-<version>.apk`), adb/Downloader sideload. Roku: sideloadable channel zip (`bowtie-roku-<version>.zip`) on releases; Roku Channel Store out of scope |
| PiP | Not applicable on TV platforms — omitted |
| Fonts | Fire TV: same Roboto set. Roku: system font (no font packaging); identity carried by palette + oversized channel numbers |

## Fire TV (`android/tv/`)

- Module `:tv` (com.android.application, applicationId `app.bowtie.tv`, minSdk 25, desugaring like siblings). Manifest: `LEANBACK_LAUNCHER` category, `android:banner` (320x180 token-styled), `uses-feature android.hardware.touchscreen required=false`, `android.software.leanback required=true`, INTERNET, same networkSecurityConfig as `:app`.
- Screens (Compose for TV, tv-material components, focus-first):
  - **Connect/Login**: shared copy/behavior from Phase 2 spec; fields sized for 10-foot; TV keyboard friendly.
  - **Channel rail**: `TvLazyColumn` rows — condensed channel number (~40sp), name, now-title + progress, next dim. Select → play. Long-press/menu → quality.
  - **Player**: Media3 `PlayerView` interop; DPAD: select = play/pause, back = stop+return, down = transport/quality drawer (quality per `maxQuality` filtering; stats readout Roboto Mono). Same session-replace/zap semantics via the shared `PlayerViewModel` (up/down = channel zap with debounce).
  - **Focus ownership is an explicit requirement**: `AndroidView(PlayerView)` must NOT own DPAD focus (`isFocusable=false`, `descendantFocusability=FOCUS_BLOCK_DESCENDANTS`); the Compose layer owns key events via a focused container, and focus restores to it after playback starts. Acceptance: zap and drawer keys work WHILE video plays.
  - **Settings**: server info, account, change password, sign out.
- Caps: HEVC detection unchanged. **AC3/EAC3 detection must be extended for TV**: the current `hasAc3Passthrough` path only works on API 31+, but Fire OS 6-11 are API 25-30 and typically expose AC-3 as HDMI passthrough, not a MediaCodec decoder. Add a pre-S probe (`AudioTrack`/`AudioFormat.ENCODING_AC3`+`ENCODING_E_AC3` support check) in `Caps.detect` inputs so Fire TV negotiates audio passthrough instead of forcing AAC transcode. Covered by unit tests with injected probe results.
- Release workflow: the EXISTING android job (already `needs: [goreleaser]`, already using the fixed `BOWTIE_KEYSTORE_*`/`ANDROID_KEY_ALIAS` names) additionally builds `:tv:assembleRelease` and uploads `bowtie-tv-<version>.apk` — one job, both APKs, no new upload job to race the release.
- Testing: relocated ViewModel tests keep running in `:core`; `:tv` gets thin unit tests for any TV-only logic (e.g. DPAD key mapping); CI adds `:tv:testDebugUnitTest :tv:assembleDebug` to the android job. Visual validation on a local Android TV emulator (AVD with TV system image) by the orchestrator with screenshots; Fire-OS-specific behavior validated by the user on a real stick if available.

## Roku (`roku/`)

- BrighterScript project: `bsconfig.json` (strict mode), `manifest` (mm_icon/splash assets in token colors, `ui_resolutions=fhd`), structure:

```
roku/
├── bsconfig.json  package.json (bsc + bslint devDeps)  README.md
├── manifest
├── images/            # icons/splash generated in palette colors
├── source/
│   ├── main.bs                  # app entry, scene boot
│   └── lib/
│       ├── BowtieClient.bs      # request building/parsing (pure); transport lives in ApiTask
│       ├── AuthState.bs         # PURE token/refresh state machine (no I/O) — see Auth actor below
│       ├── Caps.bs              # h264 always; hevc via roDeviceInfo.CanDecodeVideo({Codec:"hevc"}).Result;
│       │                        # maxHeight via GetVideoMode() (UHD-capable → 2160 potential; ladder caps at 1080 anyway)
│       ├── GuideLogic.bs        # nowNext + allowedProfiles (ported semantics)
│       └── Registry.bs          # serverUrl + refreshToken persistence (roRegistrySection "bowtie")
└── components/
    ├── AppScene.(xml|bs)        # routing by app phase (connect/login/home)
    ├── ConnectScene / LoginScene# keyboard dialogs, /healthz validate, error copy per Phase 2 spec
    ├── HomeScene                # channel rail: RowList/MarkupList — number/name/now/next; select→Player; Settings entry
    ├── PlayerScene              # Video node, streamFormat hls, absolute playlistUrl WITH token query;
    │                            # OK=play/pause, back=stop+return, up/down=zap (debounced session-replace)
    │                            # quality menu (maxQuality-filtered); buffering states surfaced.
    │                            # Mid-play recovery: ONLY auth-shaped failures (errorCode/errorStr
    │                            # matching an allowlist established during on-device probing — the
    │                            # validation script includes a token-kill step to CAPTURE the codes)
    │                            # get one silent session re-create; all other errors → bounded retry
    │                            # WITHOUT new sessions, then error UI. Never "any error → recreate".
    ├── SettingsScene            # server, account, change password, sign out
    ├── tasks/ApiTask            # THE auth actor: see below
    └── SelfTestScene            # hidden debug scene (launch arg) running pure-logic tests on-device
```

### Roku Auth actor (the design, not an aspiration)

ONE long-lived `ApiTask` instance owns ALL API traffic and the token pair.
Scenes never do HTTP; they enqueue requests onto an ApiTask queue field and
observe response fields. Because a Task node processes its queue sequentially,
refresh handling is structurally serialized: on a 401 the task (1) runs ONE
refresh, (2) persists the new refresh token to the registry BEFORE retrying,
(3) retries the failed request and continues the queue with the new access
token. There is no cross-Task coalescing problem because there are no other
API Tasks — the queue IS the mutex. A refresh failure drains the queue with
auth-failure responses and signals sign-out; a request that was queued behind
a successful refresh must succeed with the new token (never sign out because
"its" 401 came before the refresh). The token/refresh decision logic lives in
`AuthState.bs` as PURE functions (state in, actions out) so `SelfTestScene`
can execute its transition table on-device.

### HTTPS (first-class, per Phase 2)

Every `roUrlTransfer` in ApiTask sets `SetCertificatesFile("common:/certs/ca-bundle.crt")`
+ `InitClientCertificates()`; https server URLs are the normal remote path and
appear in the validation script. HTTP LAN URLs need nothing special on Roku.

- Behavior contracts (Phase 2 shared sections apply): auth flow incl. rotate-on-boot, single-flight refresh (serialize refresh inside one ApiTask queue — Roku Tasks are single-threaded per node, so the design forces all auth traffic through ONE ApiTask instance, which serializes naturally), session lifecycle (playlist polling = heartbeat; DELETE on real stop/zap/sign-out; process death → always new session), error mapping (503 who's-watching + retry, 422 → Auto retry once, 404 → refresh channels, network → bounded retry), caps reporting, quality picker filtering.
- Design: palette tokens as constants; channel numbers large (system font bold); focus follows Roku default ring styled amber where the platform allows (`focusBitmapUri` 9-patch in amber).
- CI: `roku` job (ubuntu, node 22): `npm ci` (lockfile committed) → `npx bsc` compiling `.bs` → **staging dir** (`out/staging` with transpiled `.brs`, `manifest`, `components/`, `images/` — never `.bs` sources, `node_modules`, or config) → zip with staging CONTENTS at the zip ROOT (`cd out/staging && zip -r ../bowtie-roku.zip .`) → artifact. bslint runs too. Release workflow: the roku packaging runs inside a job that **`needs: [goreleaser]`** before `gh release upload bowtie-roku-<version>.zip` (same race rule as the APK job — or fold the upload into that job).
- **No runtime tests in CI** — compensating controls: strict compile, lint, orchestrator line review, logic mirrored from twice-proven implementations, pure-function structure for the risky logic, AND the on-device **SelfTestScene**: launched via a dev flag, it executes the pure-logic suites (AuthState transition table, GuideLogic nowNext/allowedProfiles fixtures — same fixtures as the other platforms) and renders pass/fail counts. The user's validation run therefore executes real automated tests on real hardware.

## User validation script (`docs/deploy/roku-testing.md`)

Step-by-step: enable Developer Mode (remote konami code), note device IP + dev password, sideload the release zip via `http://<roku-ip>` installer, then a checklist that is a GATE, not a smoke pass:
1. SelfTestScene run (all pure-logic tests pass on-device)
2. Connect (LAN http) → login → rail with guide data
3. Play; verify HEVC negotiation via the server admin sessions panel
4. Zap storm: 10 rapid up/downs — no stranded sessions in admin panel afterward
5. Quality change; stats sanity
6. Auth race: idle past 15-min access expiry, then zap immediately — no sign-out
7. Token kill: admin terminates the session mid-play — CAPTURE the Video error
   code/message shown in the debug overlay (feeds the auth-error allowlist), app
   recovers with one recreate
8. Tuner-busy copy (occupy all tuners first)
9. HTTPS: reconnect using the public https URL end-to-end
10. Sign out; relaunch resumes to login (server remembered)
Each step lists expected behavior and what to report. Same doc gets a short Fire TV section (adb sideload steps).

## Out of scope

Roku Channel Store / Amazon Appstore submission, DVR, grid EPG on TV (list/rail only, consistent with Phase 2 staging), Roku custom fonts, ATSC 3.0.

## Review history

- 2026-08-06: Initial draft (Claude).
- 2026-08-06: Grok review — 10 findings, all incorporated: Roku Auth rewritten as an
  explicit single-ApiTask actor with pure AuthState machine (blocker); zip packaging
  pipeline specified staging-dir→root-contents with goreleaser-gated upload (blocker);
  Caps API signatures corrected (CanDecodeVideo arg, GetVideoMode) + Android AC3
  passthrough probe for API 25-30; Roku TLS cert setup required; single android job
  builds both APKs; ViewModel move narrowed to classes+tests (factories stay in app
  modules); PlayerView focus-ownership requirement; PlayerScene recovery tightened to
  an auth-error allowlist (captured on-device); validation script expanded to a
  10-step adversarial gate; added SelfTestScene so on-device validation executes the
  pure-logic test suites.
