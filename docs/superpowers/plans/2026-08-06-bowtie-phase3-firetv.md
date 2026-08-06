# Bowtie Phase 3 — Fire TV App Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax. **This session: Grok implements on branch `track/firetv`; Claude reviews each task.**

**Goal:** A Compose-for-TV Fire TV app (`:tv`, minSdk 25) reusing `:core` and the relocated ViewModels, with correct AC-3 passthrough detection and a release APK on GitHub releases.

**Architecture:** Per spec `docs/superpowers/specs/2026-08-06-bowtie-phase3-tv-apps-design.md`. VM classes+tests move `:app`→`:core` (factories stay per app module); new `:tv` module is pure Compose-for-TV UI over the shared ViewModels; Media3 player with explicit focus-ownership rules.

**Tech Stack:** Kotlin (pinned catalog), `androidx.tv:tv-material`, Media3, lifecycle-viewmodel in `:core`, existing test stack.

## Global Constraints

- All Phase 2 Android Global Constraints still bind (pinned catalog, shared Json, viewer allowlist, single-flight refresh, teardown rules, no auth on media requests, tokens/copy).
- `:tv` minSdk **25**, applicationId `app.bowtie.tv`, desugaring, same networkSecurityConfig as `:app`.
- Manifest: `LEANBACK_LAUNCHER`, `android:banner` (320x180), `uses-feature android.hardware.touchscreen required=false`, `android.software.leanback required=true`, INTERNET.
- **Focus ownership**: `AndroidView(PlayerView)` never owns DPAD focus; Compose container owns key events; focus restores after playback starts. Acceptance: zap + drawer keys work while video plays.
- PiP omitted on TV.
- Worktree rules: branch `track/firetv`; touch `android/**`, ci.yml android job, release.yml android job, `Makefile` android targets, `docs/deploy/roku-testing.md` Fire TV section (Task 4 only). No other docs edits; evidence in commit bodies.
- Verification every task: `cd android && ./gradlew -q :core:test :app:testDebugUnitTest :tv:testDebugUnitTest :app:assembleDebug :tv:assembleDebug` (drop `:tv:` targets before Task 2 creates the module).

---

### Task 1: Relocate ViewModels to `:core` (classes + tests only)

**Files:**
- Move: `android/app/src/main/kotlin/app/bowtie/{AppViewModel,PlayerViewModel,ChannelListViewModel}.kt` → `android/core/src/main/kotlin/app/bowtie/core/vm/` (package `app.bowtie.core.vm`)
- Move: their three test files → `android/core/src/test/kotlin/app/bowtie/core/vm/`
- Modify: `android/core/build.gradle.kts` (+ `androidx.lifecycle:lifecycle-viewmodel-ktx`, coroutines-test/Turbine to testImplementation), `android/app/**` imports, `Nav.kt` (factories STAY here, only imports change), `.github/workflows/ci.yml` if it names test tasks explicitly.

**Interfaces:**
- Produces: same public VM APIs, now under `app.bowtie.core.vm`. NOTHING else changes — no behavior edits permitted in this task.

- [ ] **Step 1:** Move files, update package/imports mechanically.
- [ ] **Step 2:** Full suite green: `./gradlew -q :core:test :app:testDebugUnitTest :app:assembleDebug` — same test counts as before the move (54+27 minus the 27 that moved into :core's count; assert totals in commit body).
- [ ] **Step 3:** Commit `refactor: relocate viewmodels to core for tv reuse`

### Task 2: `:tv` module scaffold + TV audio-caps probe

**Files:**
- Create: `android/tv/build.gradle.kts`, `android/tv/src/main/AndroidManifest.xml`, banner drawable (solid #101418 320x180 with "Bowtie" — generate PNG via a small script, keep the script out of the repo), `android/tv/src/main/kotlin/app/bowtie/tv/{TvApp.kt,MainActivity.kt,TvTheme.kt}` (hello screen on tokens)
- Modify: `android/settings.gradle.kts`, versions catalog (+ `androidx.tv:tv-material` pinned), ci.yml android job (add `:tv:testDebugUnitTest :tv:assembleDebug`), Makefile (`android-test`/`android-apk` cover :tv)
- Modify: `android/core/src/main/kotlin/app/bowtie/core/Caps.kt` + test

**Interfaces:**
- Produces: compiling `:tv` app; extended Caps:

```kotlin
object Caps {
  // detect() unchanged signature PLUS:
  fun detect(hardwareDecoders: List<String>, ac3Passthrough: Boolean, displayHeight: Int): ClientCaps  // existing
  fun audioPassthroughProbe(context: Context): Boolean
  // pre-S path: AudioTrack.isDirectPlaybackSupported(AudioFormat ENCODING_AC3/ENCODING_E_AC3, ...) on API 29+;
  // API 25-28: AudioManager.getDevices HDMI + ENCODING_AC3 in device encodings; API 31+: existing profile path.
  // current(context) uses audioPassthroughProbe for the ac3Passthrough input.
}
```

- [ ] **Step 1: Failing tests** — Caps tests with injected probe result: probe=true → caps include ac3+eac3; probe=false → aac only (existing behavior). (The probe function itself is a thin platform wrapper — document per-API-level strategy in KDoc; unit tests cover detect() wiring only.)
- [ ] **Step 2:** Implement probe + wire into `current`. Scaffold `:tv` (manifest per Global Constraints; hello screen).
- [ ] **Step 3:** Full verification incl. `:tv:assembleDebug`. **Step 4:** Commit `feat: tv module scaffold; ac3 passthrough detection for api 25-30`

### Task 3: `:tv` screens — Connect, Login, Rail, Settings

**Files:**
- Create: `android/tv/src/main/kotlin/app/bowtie/tv/ui/{ConnectScreen,LoginScreen,ChannelRailScreen,SettingsScreen}.kt`, `TvNav.kt` (+ its own VM factories mirroring Nav.kt's), thin `android/tv/src/test/kotlin/**` for any TV-only pure logic

**Interfaces:**
- Consumes: `app.bowtie.core.vm.*` ViewModels exactly as relocated; `GuideLogic`; tokens from `TvTheme`.

Details: rail rows — condensed number ~40sp bold, name, now-title + progress bar (amber), next dim; focused row scales (tv-material default); select → Player route (stub composable this task: black + name + back → stop()); Settings/Connect/Login mirror phone copy, TV-keyboard-friendly single-column layouts, focus-visible everywhere.

- [ ] **Step 1:** Implement with the same ChannelListViewModel wiring as phone (ON_START + 5-min refresh + channelsStale).
- [ ] **Step 2:** Verification suite green; boot on an Android TV emulator if present, else `:tv:assembleDebug` acceptance (orchestrator does emulator screenshots at review).
- [ ] **Step 3:** Commit `feat: fire tv connect, login, channel rail, settings`

### Task 4: `:tv` player + release APK + docs

**Files:**
- Create: `android/tv/src/main/kotlin/app/bowtie/tv/ui/{TvPlayerScreen,TvStatsOverlay}.kt`
- Modify: `TvNav.kt`, `.github/workflows/release.yml` (android job also builds `:tv:assembleRelease`, uploads `bowtie-tv-<version>.apk` — SAME job, keystore env unchanged), `android/README.md` (+ Fire TV section), `docs/deploy/roku-testing.md` (create if absent with Fire TV sideload section ONLY — Roku track owns the rest; if it exists, append the Fire TV section)

Player details (mirror phone PlayerScreen but TV-idiomatic): Media3 PlayerView via AndroidView with `isFocusable=false` + `FOCUS_BLOCK_DESCENDANTS`; a focused Compose Box owns onKeyEvent — key map (definitive): DPAD_CENTER short-press = play/pause; DPAD_CENTER long-press (≥700ms) OR KEYCODE_MENU = open transport/quality drawer; DPAD_UP/DPAD_DOWN = zap (debounced via PlayerViewModel); BACK = stop + pop. Stats toggle lives inside the drawer. Key map documented in README; auth-error/stall handling identical to phone via shared VM.

- [ ] **Step 1:** Implement; unit-test any key-mapping helper (pure function KeyEvent→PlayerAction with tests).
- [ ] **Step 2:** Full verification; `:tv:assembleRelease` (debug-fallback) builds; release.yml YAML validates.
- [ ] **Step 3:** Commit `feat: fire tv player with focus-safe dpad controls; tv apk on releases`

## Post-plan notes
- Sequential 1→2→3→4. Orchestrator does Android-TV-emulator visual review after Tasks 3 and 4.
- User validation on real Fire TV hardware is optional (Fire OS quirks) — documented in the testing doc.

---

# REVIEW AMENDMENTS (BINDING — these override the task text above where they conflict)

Incorporated from the Grok plan review, 2026-08-06. Every task prompt references this section.

## A1. Task 4 player input (replaces the key-handling text)
- Use **`onPreviewKeyEvent`** (capture phase) on the focused container, never bubble-phase only.
- Long-press is a **stateful handler**, not a pure single-event map: track DPAD_CENTER KeyDown timestamp (or `nativeKeyEvent.isLongPress`/repeatCount); KeyUp <700ms = play/pause, ≥700ms (or MENU KeyDown) = drawer. Unit-test the state machine (down/up sequences with injected clock), not a KeyEvent→Action lookup.
- Focus mechanism is explicit: container has `focusable()` + a `FocusRequester`; request focus on entry AND re-request when the player transitions to `STATE_READY` or after a media-source change (Media3 surface attach steals focus otherwise).
- **Recorded decision (spec override):** up/down = zap; drawer = CENTER-long or MENU. The spec's "down = drawer" line is superseded. Rail long-press quality (spec) is DEFERRED out of Phase 3 scope — quality lives in the player drawer only.

## A2. Task 2 Caps (replaces the probe ladder)
- API 31+: keep the existing `getDevices` **encodings** check (do not call it a "profile path").
- API 29–30: `AudioTrack.isDirectPlaybackSupported` with a full `AudioFormat` (ENCODING_AC3 / ENCODING_E_AC3, 48000 Hz, CHANNEL_OUT_5POINT1) + `AudioAttributes` USAGE_MEDIA.
- API 25–28: HDMI device `encodings` contains AC3 — **best-effort**, KDoc documents the false-negative risk on Fire OS 6 stacks.
- Add the seam `current(context, probe: (Context) -> Boolean = ::audioPassthroughProbe)`; tests assert the wiring through the seam (honest framing: platform probe itself is untested; `detect` tests are regression).

## A3. Task 4 Media3 parity (new requirement)
Extract the phone `PlayerScreen`'s Media3 wiring into **`core/src/main/kotlin/app/bowtie/core/player/PlayerEngine.kt`** (`:core` gains media3-exoplayer + media3-hls — playback engine is not UI; PlayerView stays per-app): unauthenticated DefaultHttpDataSource + HLS factory, playlist resolve, 403→authError callback, behind-live-window recovery, network backoff. Phone `PlayerScreen` refactors onto it in the SAME task; `:tv` consumes it. Parity checklist in the commit body: 403, live-window, backoff, no Bearer on media.

## A4. Task 1 precision
- Imports change across `MainActivity`, `Nav.kt`, and all five screen files — not just Nav.
- Catalog: ADD `lifecycle-viewmodel-ktx` alias (reuse lifecycle 2.9.2). Do NOT re-add coroutines-test/Turbine to `:core` (already present).
- Commit body: total @Test count conserved (81) with new locations; `:app:testDebugUnitTest` may legitimately drop to ~9 (ChannelListViewModelTest moves too).

## A5. Task 2 scaffold precision
`:tv` build.gradle.kts MIRRORS `:app`: same signingConfigs-from-env block, same networkSecurityConfig reference, same versionName/versionCode source, Compose BOM + activity-compose + lifecycle-compose + Media3 + desugaring; PLUS `androidx.tv:tv-material` pinned in the catalog (current stable line, ~1.1.x — verify compatibility with Compose BOM 2026.06.00 before pinning).

## A6. Release workflow
Same android job: both `assembleRelease` targets; upload both files (`android/app/build/outputs/apk/release/app-release.apk` → `bowtie-<ver>.apk`, `android/tv/build/outputs/apk/release/tv-release.apk` → `bowtie-tv-<ver>.apk`).
