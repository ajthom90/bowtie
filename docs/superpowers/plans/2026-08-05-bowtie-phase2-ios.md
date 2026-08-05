# Bowtie Phase 2 — iOS/iPadOS/tvOS App Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax. **This session: implementation delegated to Grok via /grok-build on branch `track/ios`; Claude reviews each task.**

**Goal:** Native SwiftUI viewer app for iOS/iPadOS + tvOS consuming the Bowtie v0.1.0 API: connect, login, channel list with now/next, HLS playback with quality selection, settings.

**Architecture:** Mirrored thin-native per spec `docs/superpowers/specs/2026-08-04-bowtie-phase2-native-apps-design.md`. Local Swift package **BowtieKit** (models, client, session store, caps, guide logic) + two thin SwiftUI app targets. XcodeGen generates the project from committed `project.yml` (agent-friendly, reviewable diffs; `.xcodeproj` is gitignored).

**Tech Stack:** Swift 5.10+, SwiftUI, AVKit/AVFoundation, XCTest, XcodeGen, zero third-party runtime dependencies.

## Global Constraints

- iOS/iPadOS/tvOS **17.0** deployment floor. Swift 5.10+. Zero third-party runtime deps.
- Viewer API allowlist ONLY: login, refresh, logout, me, me/password, channels, guide, sessions create/delete.
- Refresh is **single-flight**; new refresh token persisted before any retry fires.
- Teardown rules: DELETE session on real leave only; PiP/background-with-playback keeps the session; zap/quality = cancel-in-flight → DELETE old → POST new, ~400ms debounce.
- ATS: `NSAllowsLocalNetworking = true` in both targets (cleartext for LAN/.local only).
- HLS: playlistUrl resolved against server base URL preserving the `?token=` query; never attach Authorization to media requests.
- Quality picker: Auto + rungs ≤ user `maxQuality` (empty cap = all).
- Design tokens: bg #101418, surface #1A2027, raised #232B34, line #2E3843, text #F2EFE8, dim #9BA5AE, amber #F0A428, signal #5DBB63, alert #E4574B. SF + SF Condensed (channel numbers) + SF Mono (readouts).
- Copy in sentence case, plain verbs; error copy verbatim from spec where given.
- Commits: conventional (`feat:`, `test:`, `chore:`) + `Co-Authored-By: Grok Build <noreply@x.ai>`.
- Verification for every task unless stated: `cd ios && xcodegen generate && swift test --package-path BowtieKit` (BowtieKit is multiplatform incl. macOS so its tests run fast without a simulator; there is NO xcodebuild scheme for the package — never reference one) plus the task's own build/test commands.
- CI destinations: builds use `generic/platform=iOS Simulator` / `generic/platform=tvOS Simulator`; simulator TESTS resolve a device UDID at runtime (`xcrun simctl list devices available -j` → first available iPhone) — never hard-code a device name in CI.
- Worktree rules: branch `track/ios`, touch ONLY `ios/**`, `.github/workflows/ci.yml` (ios job, Task 1 only), `Makefile` (ios targets, Task 1 only). No `docs/**` edits; evidence goes in commit message bodies.

## File Structure (target)

```
ios/
├── project.yml                     # XcodeGen: targets Bowtie (iOS), BowtieTV (tvOS)
├── .gitignore                      # Bowtie.xcodeproj, DerivedData, xcuserdata
├── README.md                       # build + sideload instructions
├── BowtieKit/
│   ├── Package.swift               # platforms: iOS 17, tvOS 17, macOS 14 (tests)
│   ├── Sources/BowtieKit/
│   │   ├── Models.swift            # User, Channel, GuideChannel, GuideProgram, SessionCreate…
│   │   ├── BowtieClient.swift      # actor; viewer allowlist; single-flight refresh
│   │   ├── BowtieError.swift
│   │   ├── ServerURL.swift         # normalization + healthz validation
│   │   ├── SessionStore.swift      # protocol + KeychainSessionStore + InMemorySessionStore
│   │   ├── Caps.swift
│   │   └── GuideLogic.swift        # now/next derivation
│   └── Tests/BowtieKitTests/
│       ├── BowtieClientTests.swift # URLProtocol-stubbed
│       ├── ServerURLTests.swift
│       ├── CapsTests.swift
│       └── GuideLogicTests.swift
└── App/
    ├── Shared/                     # compiled into BOTH app targets
    │   ├── Theme.swift             # Color/Font tokens
    │   ├── AppModel.swift          # auth state machine, DI container
    │   ├── PlayerModel.swift       # session-replace state machine (shared, unit-tested via BowtieKit? see Task 5)
    │   ├── ConnectView.swift  LoginView.swift  SettingsView.swift
    │   └── StatsOverlay.swift
    ├── iOS/
    │   ├── BowtieApp.swift  ChannelListView.swift  PlayerView.swift
    │   └── Info.plist              # ATS local networking, PiP background mode
    └── tvOS/
        ├── BowtieTVApp.swift  ChannelRailView.swift  TVPlayerView.swift
        └── Info.plist
```

---

### Task 1: Project scaffold — XcodeGen, BowtieKit package, ATS, CI

**Files:**
- Create: `ios/project.yml`, `ios/.gitignore`, `ios/README.md`, `ios/BowtieKit/Package.swift`, `ios/BowtieKit/Sources/BowtieKit/Models.swift` (empty enum namespace to compile), `ios/BowtieKit/Tests/BowtieKitTests/SmokeTests.swift`, `ios/App/Shared/Theme.swift`, `ios/App/iOS/BowtieApp.swift` (Hello screen), `ios/App/iOS/Info.plist`, `ios/App/tvOS/BowtieTVApp.swift`, `ios/App/tvOS/Info.plist`
- Modify: `.github/workflows/ci.yml` (add `ios` job), `Makefile` (`ios-gen`, `ios-test` targets)

**Interfaces:**
- Produces: buildable Bowtie (iOS) + BowtieTV (tvOS) targets depending on local package BowtieKit; `Theme` enum with the 9 token colors as `Color` statics + `Font` helpers `channelNumber(_ size:)` (SF Condensed weight-700 via `.width(.condensed)`), `mono(_ size:)`.

- [x] **Step 1:** `brew list xcodegen || brew install xcodegen`. Write `project.yml`: two targets, deploymentTarget 17.0, `packages: BowtieKit: {path: BowtieKit}`; both Info.plists set `NSAppTransportSecurity → NSAllowsLocalNetworking: true`; iOS target adds `UIBackgroundModes: [audio]` (PiP/AirPlay continuity).
- [x] **Step 2:** BowtieKit `Package.swift` platforms `[.iOS(.v17), .tvOS(.v17), .macOS(.v14)]`; SmokeTests asserts `true` (replaced next task).
- [x] **Step 3:** `xcodegen generate` then `xcodebuild -project Bowtie.xcodeproj -scheme Bowtie -destination 'generic/platform=iOS Simulator' build` and same for `BowtieTV` with `generic/platform=tvOS Simulator` — both succeed.
- [x] **Step 4:** CI `ios` job (runs-on macos-15): install xcodegen, `xcodegen generate`, swift test for BowtieKit (`swift test --package-path ios/BowtieKit`), build both targets against generic simulator destinations.
- [x] **Step 5:** Commit `feat: ios scaffold with xcodegen, bowtiekit package, ats config`

### Task 2: BowtieKit — Models, ServerURL, errors

**Files:**
- Create/Modify: `Models.swift`, `ServerURL.swift`, `BowtieError.swift`
- Test: `ServerURLTests.swift`, `ModelsTests.swift`

**Interfaces (Produces — exact):**

```swift
public struct User: Codable, Equatable, Sendable { public let id: Int64; public let username: String; public let role: String; public let maxQuality: String }
public struct TokenPair: Codable, Sendable { public let accessToken: String; public let refreshToken: String; public let user: User }
public struct Channel: Codable, Equatable, Identifiable, Sendable { public let id: Int64; public let guideNumber: String; public let name: String; public let logoUrl: String }
public struct GuideProgram: Codable, Equatable, Sendable { public let start: Date; public let stop: Date; public let title: String; public let subtitle: String; public let description: String; public let category: String }
public struct GuideChannel: Codable, Equatable, Sendable { public let channelId: Int64; public let guideNumber: String; public let name: String; public let logoUrl: String; public let programs: [GuideProgram] }
public struct ClientCaps: Codable, Sendable { public var videoCodecs: [String]; public var audioCodecs: [String]; public var maxHeight: Int; public var profile: String }
public struct SessionInfoMeta: Codable, Equatable, Sendable { public let videoCodec: String; public let profile: String; public let backend: String; public let channelName: String }
public struct CreatedSession: Codable, Sendable { public let viewerId: String; public let playlistUrl: String; public let session: SessionInfoMeta? }
public struct ActiveSessionSummary: Codable, Sendable { public let channelName: String; public let viewers: [ViewerSummary]; public struct ViewerSummary: Codable, Sendable { public let username: String } }

public enum BowtieError: Error, Equatable {
  case unauthorized                      // post-refresh 401 → sign out
  case tunersBusy([ActiveSessionSummary])
  case negotiationFailed(String)         // 422 message
  case notFound
  case server(status: Int, message: String)
  case network(String)
  case invalidServerURL
}

public enum ServerURL {
  public static func normalize(_ raw: String) -> URL?           // adds http:// if schemeless, strips trailing /, rejects empty/garbage
  public static func validate(_ url: URL, timeout: TimeInterval = 2) async -> Bool   // GET /healthz == 200
  public static func resolve(path: String, against base: URL) -> URL   // preserves query of path (HLS token!)
}
```

JSON decoding: RFC3339 dates via `.iso8601` strategy; all requests/responses camelCase (matches API — no key strategy conversion needed).

- [x] **Step 1: Failing tests** — `ServerURLTests`: normalize("192.168.1.50:8400") → `http://192.168.1.50:8400`; normalize("https://tv.example.com/") strips slash; normalize("") → nil; resolve(path:"/api/v1/stream/x/index.m3u8?token=abc", against:) keeps `token=abc`. `ModelsTests`: decode fixture JSON strings for TokenPair, GuideChannel (RFC3339 date asserts), CreatedSession with & without `session`.
- [x] **Step 2:** Run `swift test --package-path ios/BowtieKit` — FAIL. **Step 3:** Implement. **Step 4:** PASS. **Step 5:** Commit `feat: bowtiekit models, server url handling, error taxonomy`

### Task 3: BowtieKit — SessionStore + BowtieClient with single-flight refresh

**Files:**
- Create: `SessionStore.swift`, `BowtieClient.swift`
- Test: `BowtieClientTests.swift` (URLProtocol stub), `SessionStoreTests.swift` (InMemory)

**Interfaces (Produces — exact):**

```swift
public protocol SessionStore: Sendable {
  func loadServer() -> URL?
  func loadRefreshToken() -> String?
  func save(server: URL?, refreshToken: String?)   // nil clears
}
public final class KeychainSessionStore: SessionStore { public init(service: String = "app.bowtie") }
public final class InMemorySessionStore: SessionStore { public init() }

public actor BowtieClient {
  public init(server: URL, store: SessionStore, urlSession: URLSession = .shared)
  public private(set) var currentUser: User?
  // Viewer allowlist:
  public func login(username: String, password: String) async throws -> User
  public func bootstrapFromStoredToken() async throws -> User      // rotate stored refresh → tokens; throws .unauthorized if absent/dead
  public func logout() async                                        // best-effort POST, always clears store
  public func changePassword(current: String, new: String) async throws
  public func channels() async throws -> [Channel]
  public func guide(start: Date, stop: Date) async throws -> [GuideChannel]
  public func createSession(channelId: Int64, caps: ClientCaps) async throws -> CreatedSession
  public func deleteSession(viewerId: String) async
  public func me() async throws -> User
}
```

Behavior contract (write tests FIRST for each):
1. Bearer attached to every call; NEVER to URLs under `/api/v1/stream/`.
0. Request bodies match OpenAPI field names EXACTLY and tests ASSERT the recorded JSON: login `{username,password}`, refresh/logout `{refreshToken}`, password `{currentPassword,newPassword}`, session create `{channelId, caps:{videoCodecs,audioCodecs,maxHeight,profile}}`.
2. On 401: exactly ONE refresh regardless of concurrent callers (single-flight via in-actor `refreshTask: Task<Void,Error>?`); waiters await it; new refresh token saved to store BEFORE retries fire; retried request succeeds.
3. Refresh failure (401 on refresh) → all waiters get `.unauthorized`, store cleared of token (server kept).
4. 503 with `{"error":"all tuners in use","sessions":[...]}` → `.tunersBusy`; 422 → `.negotiationFailed(message)`; 404 → `.notFound`.
5. `deleteSession`/`logout` swallow errors (best-effort).

- [x] **Step 1: Failing tests** — URLProtocol stub (`StubProtocol` recording requests, scripted responses). Cases: `testBearerAttached`, `testSingleFlightRefresh` (3 concurrent calls hit 401; assert exactly 1 POST /auth/refresh recorded, all 3 retried successfully, store.refreshToken == new value before retries — script the stub to assert ordering), `testRefreshFailureSignsOut`, `testTunersBusyDecoded`, `testNegotiation422`, `testDeleteSwallowsErrors`.
- [x] **Step 2:** FAIL. **Step 3:** Implement. **Step 4:** PASS incl. `swift test --package-path ios/BowtieKit`. **Step 5:** Commit `feat: bowtie client with single-flight refresh and keychain session store`

### Task 4: BowtieKit — Caps + GuideLogic (now/next)

**Files:**
- Create: `Caps.swift`, `GuideLogic.swift`
- Test: `CapsTests.swift`, `GuideLogicTests.swift`

**Interfaces:**

```swift
public enum Caps {
  public static func make(maxHeight: Int) -> ClientCaps  // pure: h264+hevc, aac+ac3+eac3, profile "" — fully unit-testable, compiles on macOS
  #if canImport(UIKit)
  public static func current() -> ClientCaps      // thin platform wrapper: maxHeight from screen (tvOS 2160 when display>1080, else 1080)
  #endif
}
// The macOS test platform never touches current(); all logic lives in make() — mirror of Android's pure detect().
public enum GuideLogic {
  public struct NowNext: Equatable, Sendable { public let now: GuideProgram?; public let next: GuideProgram? }
  public static func nowNext(programs: [GuideProgram], at date: Date) -> NowNext   // now: start<=date<stop; next: earliest start>=now's stop (or >date if no now)
  public static func allowedProfiles(maxQuality: String) -> [String]              // "" → [original,high,medium,low]; else prefix of ladder from that rung down
}

// ALSO in this task — ChannelListModel (BowtieKit, so it's swift-test-able):
@Observable @MainActor public final class ChannelListModel {
  public struct Row: Equatable, Identifiable { public let channel: Channel; public let nowNext: GuideLogic.NowNext; public var id: Int64 { channel.id } }
  public enum LoadState: Equatable { case loading, loaded([Row]), failed(String), empty }
  public private(set) var state: LoadState
  public init(client: BowtieClient, now: @escaping () -> Date = Date.init)
  public func load() async            // channels() + guide(start: now, stop: now+4h) → join via nowNext
  public func refreshIfStale() async  // called on foreground AND by a 5-min timer while visible
}
// Tests: join logic (channel with/without guide data), failure → .failed, empty → .empty, refresh window math.
```

- [x] **Step 1: Failing tests** — nowNext: mid-program, exact boundary (stop is exclusive), gap (no current → next only), empty. allowedProfiles: "", "medium" → [medium,low], "original" → all, unknown → all (defensive).
- [x] **Step 2-4:** FAIL → implement → PASS. **Step 5:** Commit `feat: capability reporting and now-next guide logic`

### Task 5: App models — AppModel auth state machine + PlayerModel session-replace machine

**Files:**
- Create: `ios/App/Shared/AppModel.swift`, `ios/App/Shared/PlayerModel.swift`
- Test: in BowtieKit tests? NO — these live in the app layer. Create `ios/App/SharedTests/` target (unit test bundle, hosted by iOS app) with `AppModelTests.swift`, `PlayerModelTests.swift`. Add to project.yml.

**Interfaces:**

```swift
@Observable @MainActor public final class AppModel {
  public enum Phase: Equatable { case connect, login, ready, checking }
  public private(set) var phase: Phase
  public private(set) var user: User?
  public var client: BowtieClient?
  public init(store: SessionStore)          // phase: server? (token? .checking : .login) : .connect
  public func connect(rawURL: String) async -> Bool     // normalize+validate+save → .login
  public func start() async                             // .checking → bootstrapFromStoredToken → .ready | .login
  public func signIn(username: String, password: String) async throws
  public func signOut() async                           // → .login (server kept)
  public func changeServer()                            // clears everything → .connect
}

@Observable @MainActor public final class PlayerModel {
  public enum State: Equatable { case idle, starting, playing(CreatedSession), stalled, failed(String), tunersBusy([ActiveSessionSummary]) }
  public private(set) var state: State
  public private(set) var currentChannel: Channel?
  public var selectedProfile: String                    // "" = Auto
  public init(client: BowtieClient, caps: ClientCaps, debounce: Duration = .milliseconds(400), clock: any Clock<Duration> = ContinuousClock())
  // CONTRACT: every createSession call sends `effectiveCaps` = caps with profile = selectedProfile ("" = Auto).
  // On 422: reset selectedProfile to "" (Auto) and retry ONCE; second 422 → .failed with the device-can't-play copy.
  public func play(channel: Channel) async              // session-replace: cancel in-flight create, DELETE old, debounce, POST new
  public func setProfile(_ p: String) async             // same machine, keeps channel
  public func stop() async                              // DELETE, → idle. Called on real leave ONLY (view dismissal, sign-out, change-server, app termination) — never on background/PiP
  public func playbackAuthFailed() async                // mid-play 403 handler: one silent replace, then .failed
}
```

- [x] **Step 0: XcodeGen test wiring** — add to project.yml a `SharedTests` unit-test-bundle target hosted by the Bowtie app:
```yaml
targets:
  SharedTests:
    type: bundle.unit-test
    platform: iOS
    sources: [App/SharedTests]
    dependencies: [{target: Bowtie}]
    settings: {TEST_HOST: "$(BUILT_PRODUCTS_DIR)/Bowtie.app/Bowtie", BUNDLE_LOADER: "$(TEST_HOST)"}
schemes:
  Bowtie:
    build: {targets: {Bowtie: all}}
    test: {targets: [SharedTests]}
```
(Adjust to real XcodeGen syntax as needed; acceptance = `xcodebuild test -scheme Bowtie` runs SharedTests. `ENABLE_TESTABILITY` is on for Debug by default; tests `@testable import Bowtie`.)
- [x] **Step 0b: TestClock** — the plan bans third-party deps, and XCTest has no controllable clock. Add `App/SharedTests/ManualClock.swift` (~20 lines): a `Clock<Duration>` whose `sleep` suspends on a continuation queue and a `func advance(by:)` that resumes due sleepers. All debounce tests use it — never wall-clock sleeps.
- [x] **Step 1: Failing tests** — with InMemorySessionStore + stubbed client (URLProtocol): AppModel phase transitions (fresh → connect; stored server+token → checking → ready; bootstrap failure → login). PlayerModel with ManualClock: rapid `play` × 3 within debounce window then `advance(by: .milliseconds(400))` → exactly ONE createSession (assert via stub recording); zap after playing → deleteSession(old) then create(new); setProfile keeps channel and sends effectiveCaps with the new profile (assert request body); 422 once → auto-retry with profile "" (assert second body), 422 twice → .failed; stop → delete + idle; playbackAuthFailed once → replace, twice → failed.
- [x] **Step 2-4:** FAIL → implement → PASS (`xcodebuild test -scheme Bowtie -destination 'platform=iOS Simulator,name=iPhone 17' -only-testing:SharedTests`). **Step 5:** Commit `feat: app auth state machine and session-replace player model`

### Task 6: iOS UI — Connect, Login, ChannelList (now/next), Settings

**Files:**
- Create: `ConnectView.swift`, `LoginView.swift`, `ios/App/iOS/ChannelListView.swift`, `SettingsView.swift`, `ios/App/iOS/PlayerView.swift` (**STUB in this task**: black screen + channel name + Back button calling `playerModel.stop()` — real AVKit work is Task 7; the stub exists so this task compiles and navigation lands); finalize `Theme.swift`; wire navigation in `BowtieApp.swift`

Screens (all tokens from Theme; sentence-case copy):
- Connect: URL field, Validate button → spinner → error copy "Couldn't reach a Bowtie server there. Check the address and try again." Placeholder shows both URL forms.
- Login: username/password, error "Wrong username or password."
- ChannelList: rows = oversized condensed guide number (amber when playing), name, now-title + progress capsule, next-title dim; pull-to-refresh; auto-refresh 5min while visible; tap → Player. Empty state: "No channels yet. Ask your admin to enable some."
- Settings: server (address, change server), account (username, change password form → `client.changePassword`, sign out). Change-password success copy: "Password changed."

- [x] **Step 1:** Build all views with previews; navigation flows from AppModel.phase.
- [x] **Step 2:** `xcodebuild build` both targets zero warnings; SharedTests still green.
- [x] **Step 3 (acceptance):** app boots to the Connect screen in the iPhone simulator (`xcrun simctl launch`). Screenshot review of all screens is done by the orchestrator after this task lands.
- [x] **Step 4:** Commit `feat: ios connect, login, channel list, settings screens`

### Task 7: iOS Player — AVPlayerViewController wrapper + stats + quality + lifecycle

**Files:**
- Create: `ios/App/iOS/PlayerView.swift`, `ios/App/Shared/StatsOverlay.swift`
- Modify: `ChannelListView.swift` (navigation), `PlayerModel.swift` only if a hook is missing (note it)

Behavior:
- `PlayerView` hosts `AVPlayerViewController` (UIViewControllerRepresentable), `allowsPictureInPicturePlayback = true`, audio session `.playback` on appear.
- Builds `AVPlayer` from `ServerURL.resolve(path: session.playlistUrl, against: server)`. KVO/publisher on `status`/`error`: `.failed` with 403-ish → `playerModel.playbackAuthFailed()`.
- Overlay (auto-hide 3s): channel number (condensed amber) + now title; buttons: quality menu (Auto + `GuideLogic.allowedProfiles(user.maxQuality)`), stats toggle, AirPlay route picker (`AVRoutePickerView`), done.
- StatsOverlay (SF Mono, amber-on-dark): codec/profile/backend from `session.session` (— for nil), plus `player.currentItem?.accessLog()` last event: bitrate, dropped frames.
- Teardown: `.onDisappear` → `playerModel.stop()` ONLY when not transitioning to PiP (`AVPlayerViewController.isPictureInPictureActive` guard via delegate flag); app `willTerminate` notification → stop.
- Error UX acceptance (spec-mandated, testable by inspection + SharedTests where logic-level): `.tunersBusy` renders "All tuners are in use" + who's-watching list from `sessions[]` + Try again; 422 path auto-falls-back to Auto (PlayerModel contract) and shows the device-can't-play copy only on second failure; `.stalled` drives a spinner overlay + player retry with backoff (1s,2s,4s ×3) before `.failed`; 404 → post channels-stale signal so ChannelListModel reloads.

- [ ] **Step 1:** Implement; **Step 2:** builds + SharedTests green; **Step 3:** simulator boot: navigating to a channel with no server shows the failed-state copy (acceptance). **Step 4:** Commit `feat: ios player with pip-safe lifecycle, stats overlay, quality menu`

### Task 8: tvOS — channel rail home + TV player

**Files:**
- Create: `ios/App/tvOS/ChannelRailView.swift`, `ios/App/tvOS/TVPlayerView.swift`
- Modify: `BowtieTVApp.swift` (nav), `project.yml` if needed

- Rail: focus-driven vertical list of channels (number + name + now/next), Play on select; focused row scales slightly (default focus effect — don't fight the system).
- TVPlayer: `AVPlayerViewController` full-screen (native transport); custom info panel tab for quality (tvOS `customInfoViewControllers` hosting the quality picker) + stats.
- Connect/Login/Settings shared views must be focus-navigable (verify Button/TextField focus works; adjust with `.focusSection()` where needed).

- [ ] **Step 1:** Implement. **Step 2:** `xcodebuild build -scheme BowtieTV -destination 'generic/platform=tvOS Simulator'` clean; boot Apple TV sim to Connect screen. **Step 3:** Commit `feat: tvos channel rail and player`

### Task 9: README + sideload docs + CI polish

**Files:**
- Create/Modify: `ios/README.md` (Xcode sideload steps: open, set team to personal, select device, run; 7-day free-provisioning caveat; TestFlight note for later), `.github/workflows/ci.yml` (ensure ios job runs BowtieKit tests + both builds + SharedTests on iPhone sim)

- [ ] **Step 1:** Write docs; CI verified green on branch push. **Step 2:** Commit `docs: ios build and sideload guide`

## Verification summary (every task)
```bash
cd ios && xcodegen generate
swift test --package-path BowtieKit
xcodebuild -project Bowtie.xcodeproj -scheme Bowtie -destination 'platform=iOS Simulator,name=iPhone 17' test
xcodebuild -project Bowtie.xcodeproj -scheme BowtieTV -destination 'generic/platform=tvOS Simulator' build
```

## Post-plan notes
- Sequential: 1→2→3→4→5→(6,7)→8→9. 6 and 7 may interleave but land as separate commits.
- The full time-grid EPG is deliberately NOT in this plan (spec: staged to a later milestone).
- User validation gate at end: sideload to a real iPhone + Apple TV, watch a real channel.
