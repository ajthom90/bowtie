# Bowtie — Phase 2 Design: Native iOS/iPadOS/tvOS + Android Apps

**Date:** 2026-08-04
**Status:** Approved (pending implementation)
**Depends on:** Phase 1 server v0.1.0 — API contract frozen at `docs/api/openapi.yaml`

## What Phase 2 builds

Two fully native viewer apps consuming the v0.1.0 API: Swift/SwiftUI for
iOS/iPadOS/tvOS and Kotlin/Jetpack Compose for Android. Viewer scope only —
login, channels, guide, playback with quality selection. Admin remains on the
web UI.

## Decisions of record

| Topic | Decision |
|---|---|
| Build order | Both apps in parallel worktree tracks (`track/ios`, `track/android`) |
| Scope | Viewer only: Connect, Login, Guide, Player, Settings. No admin screens |
| Architecture | Mirrored thin-native (Approach A): API client → secure session store → observable view state → native UI |
| Apple | One Xcode project; iOS/iPadOS + tvOS targets; shared local Swift package **BowtieKit**; Swift 5.10+; SwiftUI; zero third-party deps |
| Android | Gradle modules `:core` (minSdk 25, Fire-TV-ready for Phase 3) + `:app` (minSdk 26); Compose; Media3 ExoPlayer; OkHttp only |
| OS floors | iOS/iPadOS/tvOS 17+; Android app 26+ (`:core` 25+) |
| API clients | Hand-written thin clients over the viewer-only endpoint allowlist (9 operations); server's OpenAPI-coverage test guards drift |
| Playback | AVPlayer/`AVPlayerViewController` (AirPlay + PiP free) / Media3 `PlayerView` (+ Activity PiP) |
| Distribution | Sideload: Xcode install for Apple devices; signed APK on GitHub releases. Release keystore lives in **GitHub Actions secrets** (never in the repo — a committed keystore would let anyone forge same-signature "update" APKs); local dev builds use the debug key |
| Design language | Broadcast tokens (charcoal #101418, amber #F0A428, condensed numerals, mono readouts) adapted idiomatically per platform — no web pixel-cloning |

## Shared behavior (both platforms)

### Connect flow
First launch → Connect screen: server URL field (placeholder shows both forms:
`https://tv.example.com` and `http://192.168.1.50:8400`), Validate = `GET
/healthz` (2s timeout) with clear failure copy. One saved server. Changing the
server signs out. URL normalization: add `http://` if missing scheme; strip
trailing slash.

**Cleartext LAN HTTP is a first-class path and MUST be enabled explicitly**
(the OS default blocks it on both platforms):
- Apple: `NSAppTransportSecurity` with `NSAllowsLocalNetworking = true` in both
  targets' Info.plist (allows LAN IPs/.local; public hostnames still require
  HTTPS — correct, since remote access is BYO-TLS-proxy).
- Android: `android:networkSecurityConfig` permitting cleartext to private
  address ranges only (RFC1918 + .local), not globally.

### Auth/session
- Login → `POST /api/v1/auth/login`; access token in memory, refresh token +
  server URL in Keychain (Apple) / EncryptedSharedPreferences-style DataStore
  (Android).
- Every API call attaches Bearer; on 401 the client rotates via
  `POST /api/v1/auth/refresh` once and retries; rotation failure → sign-out to
  Login (Connect state preserved).
- **Refresh is single-flight.** The server rotates refresh tokens (the old one
  dies on use), so concurrent 401s MUST coalesce onto one refresh call —
  waiters retry with the new access token; the new refresh token is persisted
  BEFORE any retry fires. A second refresh attempt with a consumed token must
  not be treated as a sign-out-worthy failure if a coalesced refresh succeeded.
- App start with stored refresh token → rotate immediately → Guide (skip Login).
- Sign out calls `POST /api/v1/auth/logout` best-effort and clears storage.

### Playback lifecycle
- Start: `POST /api/v1/sessions {channelId, caps}` → play `playlistUrl`
  (relative → resolve against server URL). Show `session` metadata (codec,
  profile, backend) in a stats overlay (debug-style, hidden by default).
- The player's playlist polling is the server-side heartbeat; no extra timer.
- Stop/leave: `DELETE /api/v1/sessions/{viewerId}` best-effort when the user
  actually stops watching — back out of the player, switches channel, signs
  out, or the process is terminated (termination hooks are best-effort; the
  server reaps by heartbeat timeout regardless). **Backgrounding alone is NOT
  teardown**: PiP and continued playback keep the session alive (the player's
  playlist polling is the heartbeat). Android specifically must not DELETE in
  `onUserLeaveHint`/`onStop` when entering PiP. If playback is paused and the
  app stays backgrounded, no client timer is needed — the server's 30s
  heartbeat reaper handles it.
- Quality change AND channel zap are the same **session-replace state
  machine**: cancel any in-flight create, DELETE the old session, POST the new
  one. Rapid zapping debounces (~400ms) so channel surfing can't strand
  viewers or exhaust tuners. Quality picker shows Auto (= omit profile) plus
  only the rungs allowed by the signed-in user's `maxQuality` cap (from the
  login/refresh `user` object); capped rungs are hidden, and the stats overlay
  shows the negotiated result.
- App-process death loses the in-memory access token and viewerId: relaunch
  always creates a NEW session (never resumes an old playlistUrl).
- Error mapping (from the actual API shapes): **503** `TunersBusyError` →
  "All tuners are in use" + the `sessions[]` list rendered as "who's
  watching" + retry; **422** (negotiation) → "This device can't play this
  channel at that quality" + fall back to Auto; **404** (unknown/disabled
  channel) → refresh channel list and inform; **mid-play playlist/segment
  403** (expired/killed viewer token) → silently create a fresh session once,
  then surface an error; network loss → player retry with backoff, then error
  state with retry.

### Capability reporting
- Apple: `videoCodecs: [h264, hevc]`, `audioCodecs: [aac, ac3, eac3]`,
  `maxHeight: 1080` (tvOS: 2160 when display supports it — future-proofing,
  server caps at source resolution anyway).
- Android: `h264` always; `hevc` when `MediaCodecList` has a hardware HEVC
  decoder; `aac` always; `ac3`/`eac3` only when `AudioManager` passthrough or
  decoder support confirms. `maxHeight` from display metrics (cap 1080 in v1).

### Guide data
`GET /api/v1/guide` with explicit `start`/`stop` windows (default now+4h).
**There is no server-side pagination** — "paging" is purely the client
requesting a different window (max span 24h per the API). `GET
/api/v1/channels` feeds the zap list. Program cells show title + time range;
now-playing carries the amber on-air accent. Guide refresh on foreground +
every 5 min while visible.

### HLS auth specifics (both platforms — easy to get wrong)
`playlistUrl` is a server-relative path carrying a signed `?token=` query.
Resolve it against the stored server base URL WITHOUT dropping the query.
Segment requests reuse the same query token — the player must NOT inject an
`Authorization` header into media requests (and must not need to; token auth
is in the URL by design for exactly this reason).

## Apple app (`ios/`)

- `ios/Bowtie.xcodeproj` with targets **Bowtie** (iOS/iPadOS universal) and
  **Bowtie TV** (tvOS); shared local package **BowtieKit** at `ios/BowtieKit/`.
- BowtieKit (platform-independent, XCTest without simulator): `Models` (Codable
  mirrors of the VIEWER API surface only), `BowtieClient` (URLSession
  async/await, single-flight refresh, typed `BowtieError`), `SessionStore`
  (Keychain), `Caps`, `GuideLayout` (now/next derivation; grid layout math
  ported from web `guideModel` when the grid milestone lands).
- **Viewer API surface only** (identical allowlist on Android): login, refresh,
  logout, me, me/password, channels, guide, sessions create/delete. Admin
  endpoints are deliberately NOT modeled in the apps.
- App layer (SwiftUI): `ConnectView`, `LoginView`, `GuideView`, `PlayerView`
  (`AVPlayerViewController` wrapper; stats overlay; quality menu),
  `SettingsView` (server info, account, **change password** via
  `POST /api/v1/me/password`, sign out).
- **Guide is staged:** v1 ships the channel list with now/next program info on
  ALL form factors (iPhone/iPad/tvOS — on tvOS a focus-driven channel rail).
  The full time-grid EPG is a separate later milestone (it's the largest UI
  risk; list-first gets the family watching sooner).
- tvOS: same view models; views tuned for focus engine (channel rail is the
  home screen; play on select; swipe down = transport/quality).
- PiP enabled on iOS/iPadOS (`AVPictureInPictureController` via
  AVPlayerViewController default); AirPlay via native route picker in player UI.
- Audio session: `.playback` category so TV audio behaves (silent switch,
  background transition → session delete after 60s grace via background task).

## Android app (`android/`)

- Modules: `:core` (Kotlin, no Android UI deps beyond annotations; OkHttp +
  kotlinx-serialization; suspend API; `TokenStore` on DataStore+Tink or
  EncryptedSharedPreferences equivalent; `Caps`; `GuideLayout` ported w/ tests)
  and `:app` (Compose, Material3 themed with Bowtie tokens, Media3 ExoPlayer).
- Screens mirror Apple: Connect, Login, Guide (channel list with now/next on
  all form factors in v1; grid is a later milestone), Player (Media3
  `PlayerView` in Compose interop; stats overlay; quality sheet), Settings
  (incl. change password).
- PiP via Activity `enterPictureInPictureMode`; entering PiP must NOT tear
  down the session (see lifecycle rules).
- Release signing: keystore generated once and stored in **GitHub Actions
  secrets** (`ANDROID_KEYSTORE_B64`, passwords as secrets); the release
  workflow decodes it to sign the APK. Local dev uses the debug key. The
  keystore is NEVER committed — a repo-published keystore would let anyone
  build forged same-signature "updates" for sideloaded family devices.
- APK build attached to GitHub releases by the release workflow.

## Design tokens per platform

Both apps define the token set (bg #101418, surface #1A2027, raised #232B34,
line #2E3843, text #F2EFE8, dim #9BA5AE, amber #F0A428, signal #5DBB63, alert
#E4574B). Type: Apple uses SF + SF Compressed/Condensed for channel numbers,
SF Mono for readouts; Android uses Roboto + Roboto Condensed + Roboto Mono.
Channel numbers stay the signature: oversized condensed numerals leading every
channel row on every platform.

## Testing

- **BowtieKit / :core carry the coverage:** client behavior against an
  embedded mock HTTP server (auth refresh-once, 401→sign-out, session CRUD,
  error mapping incl. 503 shape), caps with injected codec/display info,
  GuideLayout golden cases (same fixtures as web's guideModel tests).
- UI layers: thin; smoke via one launch test per target (`XCUITest` /
  `androidTest` kept minimal and local-only where emulators are needed).
- CI: `ios` job on macOS runner — BowtieKit tests + `xcodebuild build` for
  both targets + `xcodebuild test` on iPhone simulator; `android` job on
  ubuntu — `:core` and `:app` unit tests + `assembleRelease`.
- Real-hardware validation (actual antenna playback on phone/TV) is a
  user step at each milestone end, like Phase 1.

## Out of scope for Phase 2

Admin screens, DVR, Chromecast, downloads/offline, multi-server, widgets,
watchOS/CarPlay, App Store / Play Store submission (accounts, review,
signing secrets), ATSC 3.0.

## Process note

Spec and both implementation plans get a headless Grok review round before
user sign-off; incorporated suggestions are noted in the doc's history section.

## Review history

- 2026-08-04: Initial draft (Claude).
- 2026-08-04: Grok review round — 12 findings, 11 incorporated: ATS/cleartext
  config hard-required (blocker); background-teardown rules rewritten to
  coexist with PiP/heartbeats (blocker); guide "paging" corrected to
  windowing; single-flight refresh required; maxQuality-aware picker; keystore
  moved from repo to CI secrets (modifies a previously-approved decision —
  flagged to user); change-password added to Settings; time-grid staged behind
  list-first v1; API surface narrowed to viewer allowlist; full error-shape
  mapping (503 sessions[]/422/404/mid-play 403); zap = debounced
  session-replace; HLS query-token auth subsection. Not incorporated: none
  rejected outright — finding 8's grid was staged rather than cut.
