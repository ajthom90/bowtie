# Bowtie Phase 2 — Android App Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax. **This session: implementation delegated to Grok via /grok-build on branch `track/android`; Claude reviews each task.**

**Goal:** Native Kotlin/Compose viewer app for Android phones/tablets consuming the Bowtie v0.1.0 API: connect, login, channel list with now/next, HLS playback via Media3 with quality selection, settings.

**Architecture:** Mirrored thin-native per spec `docs/superpowers/specs/2026-08-04-bowtie-phase2-native-apps-design.md`. Gradle modules `:core` (client/models/store/caps/guide logic — minSdk 25, Fire-TV-ready) and `:app` (Compose UI + Media3, minSdk 26).

**Tech Stack:** Kotlin 2.x, Coroutines, OkHttp, kotlinx-serialization, Jetpack Compose + Material3, Media3 (ExoPlayer), DataStore + androidx.security-crypto, JUnit + MockWebServer + Robolectric + Turbine, AGP 8.x.

## Global Constraints

- `:app` minSdk **26**, `:core` minSdk **25** (no UI deps). compileSdk/targetSdk latest stable. JDK 17.
- Deps limited to: OkHttp, kotlinx-serialization, coroutines, AndroidX (core/lifecycle/activity/datastore/security-crypto), Compose BOM + Material3, Media3, coreLibraryDesugaring (both modules — java.time on API 25), test libs (JUnit4, MockWebServer, Robolectric, Turbine, coroutines-test). Nothing else without a plan note.
- Versions PINNED in `libs.versions.toml` at implementation time to a mutually-compatible current-stable set (AGP ↔ Gradle ↔ Kotlin ↔ Compose compiler per the official compatibility maps); Gradle wrapper version recorded explicitly. Incoherent version drift is a task failure.
- JSON: single shared `Json { ignoreUnknownKeys = true; encodeDefaults = false }` — the server sends fields our viewer models don't carry (e.g. full SessionInfo in 503 bodies); strict decoding is a bug.
- Manifest MUST declare `android.permission.INTERNET`.
- Media3 NEVER uses the authenticated OkHttp client — the player uses the default data source (stream auth is the URL query token); sharing the Bearer-intercepted Call.Factory with HlsMediaSource is forbidden.
- Viewer API allowlist ONLY: login, refresh, logout, me, me/password, channels, guide, sessions create/delete.
- Refresh is **single-flight** (Mutex-coalesced); new refresh token persisted before retries.
- Teardown rules: DELETE session on real leave only; entering PiP must NOT delete (`onUserLeaveHint`/`onStop` guards); zap/quality = cancel-in-flight → DELETE old → POST new, ~400ms debounce.
- Cleartext: `network_security_config.xml` with `base-config cleartextTrafficPermitted="true"` (Android cannot scope cleartext to private IP ranges — documented LAN-first trade-off); manifest wires it.
- HLS: playlistUrl resolved against server base preserving `?token=`; NO Authorization header on media requests (Media3 default data source, no auth interceptor).
- Quality picker: Auto + rungs ≤ user `maxQuality`.
- Design tokens: bg #101418, surface #1A2027, raised #232B34, line #2E3843, text #F2EFE8, dim #9BA5AE, amber #F0A428, signal #5DBB63, alert #E4574B. Roboto + Roboto Condensed (channel numbers) + Roboto Mono (readouts).
- Release signing from env/secrets ONLY (`BOWTIE_KEYSTORE_*`); signingConfig applied only when env present; debug key otherwise. Keystore never in repo (already in GH secrets: ANDROID_KEYSTORE_B64 etc.).
- Commits: conventional + `Co-Authored-By: Grok Build <noreply@x.ai>`.
- Worktree rules: branch `track/android`, touch ONLY `android/**`, `.github/workflows/ci.yml` (android job, Task 1), `.github/workflows/release.yml` (Task 7 only), `Makefile` (android targets, Task 1). No `docs/**` edits; evidence in commit bodies.

## File Structure (target)

```
android/
├── settings.gradle.kts  build.gradle.kts  gradle.properties  gradle/libs.versions.toml
├── gradlew  gradlew.bat  gradle/wrapper/…
├── README.md
├── core/
│   ├── build.gradle.kts            # com.android.library, minSdk 25
│   └── src/main/kotlin/app/bowtie/core/
│   │     Models.kt  BowtieClient.kt  BowtieError.kt  ServerUrl.kt
│   │     TokenStore.kt  Caps.kt  GuideLogic.kt
│   └── src/test/kotlin/app/bowtie/core/   # JVM: client (MockWebServer), ServerUrl, GuideLogic
│   └── src/test/… robolectric for TokenStore & Caps
└── app/
    ├── build.gradle.kts            # com.android.application, minSdk 26, signingConfig-from-env
    └── src/main/
        ├── AndroidManifest.xml     # networkSecurityConfig, PiP activity attrs
        ├── res/xml/network_security_config.xml
        └── kotlin/app/bowtie/
            ├── BowtieApp.kt  MainActivity.kt  Nav.kt  Theme.kt
            ├── AppViewModel.kt  PlayerViewModel.kt
            └── ui/  ConnectScreen.kt LoginScreen.kt ChannelListScreen.kt PlayerScreen.kt SettingsScreen.kt StatsOverlay.kt
    └── src/test/kotlin/…           # AppViewModel, PlayerViewModel (coroutines-test + Turbine)
```

---

### Task 1: Gradle scaffold — modules, versions catalog, network security, CI

**Files:**
- Create: everything under `android/` skeleton above minus real logic (Models.kt empty object; MainActivity shows "Bowtie" on token background), `gradle/libs.versions.toml`, wrapper via `gradle wrapper`
- Modify: `.github/workflows/ci.yml` (android job: ubuntu, JDK 17, `./gradlew :core:test :app:testDebugUnitTest :app:assembleDebug`), `Makefile` (`android-test`, `android-apk`)

**Interfaces:**
- Produces: compiling `:core` (library) + `:app` (application) with Compose enabled; `Theme.kt` exposing `BowtieColors` (9 tokens as `Color`), `BowtieType` (channelNumber/mono text styles); `network_security_config.xml`:

```xml
<!-- res/xml/network_security_config.xml -->
<network-security-config>
  <base-config cleartextTrafficPermitted="true"/>
</network-security-config>
```
  Android's network security config cannot scope cleartext to private IP ranges,
  so cleartext is permitted globally — the documented LAN-first trade-off
  (mirrors iOS `NSAllowsLocalNetworking`; remote access remains HTTPS via BYO
  proxy; revisit if Play Store review demands tightening).

- [x] **Step 1:** Scaffold; `./gradlew :app:assembleDebug` succeeds; MainActivity renders name on #101418.
- [x] **Step 2:** CI android job green locally via same commands.
- [x] **Step 3:** Commit `feat: android scaffold with core/app modules, compose, cleartext config`

### Task 2: :core — Models, ServerUrl, BowtieError

**Files:** `Models.kt`, `ServerUrl.kt`, `BowtieError.kt` + JVM tests

**Interfaces (kotlinx-serialization `@Serializable`; Instant via ISO-8601 custom serializer):**

```kotlin
@Serializable data class User(val id: Long, val username: String, val role: String, val maxQuality: String)
@Serializable data class TokenPair(val accessToken: String, val refreshToken: String, val user: User)
@Serializable data class Channel(val id: Long, val guideNumber: String, val name: String, val logoUrl: String)
@Serializable data class GuideProgram(val start: Instant, val stop: Instant, val title: String, val subtitle: String, val description: String, val category: String)
@Serializable data class GuideChannel(val channelId: Long, val guideNumber: String, val name: String, val logoUrl: String, val programs: List<GuideProgram>)
@Serializable data class ClientCaps(val videoCodecs: List<String>, val audioCodecs: List<String>, val maxHeight: Int, val profile: String)
@Serializable data class SessionInfoMeta(val videoCodec: String, val profile: String, val backend: String, val channelName: String)
@Serializable data class CreatedSession(val viewerId: String, val playlistUrl: String, val session: SessionInfoMeta? = null)

sealed class BowtieError : Exception() {
  object Unauthorized : BowtieError()
  data class TunersBusy(val sessions: List<ActiveSessionSummary>) : BowtieError()
  data class NegotiationFailed(val message: String) : BowtieError()
  object NotFound : BowtieError()
  data class Server(val status: Int, val message: String) : BowtieError()
  data class Network(val cause2: Throwable) : BowtieError()
}
@Serializable data class ActiveSessionSummary(val channelName: String, val viewers: List<ViewerSummary> = emptyList()) { @Serializable data class ViewerSummary(val username: String) }
// NOTE: the wire 503 body carries full SessionInfo objects (id, channelId, key, videoCodec, profile,
// backend, startedAt, viewers[{id,username,lastSeen}]). We model only what the UI needs;
// ignoreUnknownKeys=true (Global Constraints) makes that safe. 503 test fixtures MUST be the FULL
// wire shape copied from openapi.yaml/server, not the trimmed model.

object ServerUrl {
  fun normalize(raw: String): HttpUrl?                       // scheme default http, strip trailing /
  suspend fun validate(url: HttpUrl, client: OkHttpClient, timeoutMs: Long = 2000): Boolean
  fun resolve(path: String, base: HttpUrl): HttpUrl          // preserves query (HLS token)
}
```

- [x] **Step 1: Failing tests** mirroring iOS Task 2 (normalize/resolve-preserves-query/decode fixtures incl. optional `session`).
- [x] **Step 2-4:** FAIL → implement → PASS (`./gradlew :core:test`). **Step 5:** Commit `feat: core models, server url, error taxonomy`

### Task 3: :core — TokenStore + BowtieClient with single-flight refresh

**Files:** `TokenStore.kt`, `BowtieClient.kt` + tests (client: MockWebServer JVM; TokenStore: Robolectric)

**Interfaces:**

```kotlin
interface TokenStore {                                  // impl: EncryptedSharedPreferences (androidx.security-crypto)
  fun loadServer(): String?; fun loadRefreshToken(): String?
  fun save(server: String?, refreshToken: String?)      // null clears
}
class EncryptedTokenStore(context: Context) : TokenStore
class InMemoryTokenStore : TokenStore
// TESTING: TokenStore contract tests run against InMemoryTokenStore on the JVM.
// EncryptedTokenStore is NOT Robolectric-tested (AndroidKeyStore is absent there — known footgun);
// it gets a minimal androidTest smoke (local-only, not CI) and stays a thin wrapper.

class BowtieClient(val server: HttpUrl, private val store: TokenStore, okHttp: OkHttpClient = OkHttpClient()) {
  val currentUser: StateFlow<User?>
  suspend fun login(username: String, password: String): User
  suspend fun bootstrapFromStoredToken(): User          // throws Unauthorized when absent/dead
  suspend fun logout()                                  // best-effort + clear token
  suspend fun changePassword(current: String, new: String)
  suspend fun channels(): List<Channel>
  suspend fun guide(start: Instant, stop: Instant): List<GuideChannel>
  suspend fun createSession(channelId: Long, caps: ClientCaps): CreatedSession
  suspend fun deleteSession(viewerId: String)           // swallow errors
  suspend fun me(): User
}
```

Behavior contract == iOS Task 3 exactly (single-flight via `Mutex` + shared in-flight `Deferred`; persist new refresh before retry; error mapping incl. 503 body parse). Test list mirrors iOS: `bearerAttached`, `singleFlightRefresh` (launch 3 concurrent calls against MockWebServer scripted 401,401,401,refresh-200,retry-200×3 → assert exactly one `/auth/refresh` request), `refreshFailureSignsOut`, `tunersBusyDecoded`, `negotiation422`, `deleteSwallows`, `noAuthHeaderOnStreamPaths`.

- [x] **Steps 1-5** as pattern. Commit `feat: bowtie client with single-flight refresh and encrypted token store`

### Task 4: :core — Caps + GuideLogic

**Files:** `Caps.kt`, `GuideLogic.kt` + tests (Caps via Robolectric with shadow MediaCodecList? Robolectric's codec shadows are weak — inject: `Caps.detect(codecs: List<String>, passthroughAc3: Boolean, displayHeight: Int)` pure function + thin `Caps.current(context)` untested wrapper).

```kotlin
object Caps {
  fun detect(hardwareDecoders: List<String>, ac3Passthrough: Boolean, displayHeight: Int): ClientCaps
  fun current(context: Context): ClientCaps      // gathers real inputs, calls detect
}
object GuideLogic {
  data class NowNext(val now: GuideProgram?, val next: GuideProgram?)
  fun nowNext(programs: List<GuideProgram>, at: Instant): NowNext
  fun allowedProfiles(maxQuality: String): List<String>
}
```

- [x] Tests mirror iOS Task 4 exactly (same fixtures/cases). Commit `feat: capability detection and now-next guide logic`

### Task 5: :app — AppViewModel + PlayerViewModel (session-replace machine)

**Files:** `AppViewModel.kt`, `PlayerViewModel.kt` + JVM tests (coroutines-test `TestScope`/virtual time + Turbine on StateFlows)

**Interfaces:**

```kotlin
class AppViewModel(private val store: TokenStore, clientFactory: (HttpUrl) -> BowtieClient,
                   private val scope: CoroutineScope? = null /* null → viewModelScope; injected in tests */) : ViewModel() {
  sealed class Phase { data object Connect : Phase(); data object Login : Phase(); data object Checking : Phase(); data class Ready(val user: User) : Phase() }
  val phase: StateFlow<Phase>
  val client: BowtieClient?
  suspend fun connect(raw: String): Boolean
  fun start(); suspend fun signIn(u: String, p: String); suspend fun signOut(); fun changeServer()
}
class PlayerViewModel(private val client: BowtieClient, private val caps: ClientCaps,
                      private val debounceMs: Long = 400,
                      private val scope: CoroutineScope? = null /* injected in tests */) : ViewModel() {
  sealed class State { data object Idle : State(); data object Starting : State(); data class Playing(val s: CreatedSession) : State(); data object Stalled : State()
                       data class Failed(val message: String) : State(); data class TunersBusy(val sessions: List<ActiveSessionSummary>) : State() }
  val state: StateFlow<State>; val currentChannel: StateFlow<Channel?>
  var selectedProfile: String
  fun play(channel: Channel); fun setProfile(p: String); fun stop(); fun onPlaybackAuthError()
  // identical semantics to iOS PlayerModel incl. debounce, cancel-in-flight, one silent auth-replace
}
```

- [x] Test list mirrors iOS Task 5 (debounced triple-zap → 1 create; replace deletes old; stop; auth-error once/twice) PLUS: 404 on create → Failed with channel-list-refresh signal (expose `channelsStale: StateFlow<Boolean>` or equivalent event). Test harness requirements (binding): `Dispatchers.setMain(StandardTestDispatcher())` in @Before, tests in `runTest`, ViewModels take the injected test scope, debounce advanced via `advanceTimeBy` — the debounce path must be deterministic, never sleep-based. Commit `feat: auth state machine and session-replace player viewmodel`

### Task 6: :app UI — Theme, Connect, Login, ChannelList, Settings

**Files:** `Theme.kt` (finalize), `Nav.kt`, `ConnectScreen.kt`, `LoginScreen.kt`, `ChannelListScreen.kt`, `SettingsScreen.kt`

Same screen specs and copy as iOS Task 6, Material3-idiomatic (dark colorScheme from tokens; Roboto Condensed channel numbers ~28sp/700; progress capsule with amber track for now-playing). Guide/channel refresh: on every foreground (ON_START) AND every 5min while STARTED (repeatOnLifecycle); `channelsStale` from PlayerViewModel also triggers a refresh.

- [x] Build + existing tests green; app boots to Connect on emulator or `:app:assembleDebug` acceptance. Commit `feat: android connect, login, channel list, settings screens`

### Task 7: :app — Player (Media3) + PiP + release signing + APK on releases

**Files:** `PlayerScreen.kt`, `StatsOverlay.kt`, `MainActivity.kt` (PiP attrs/hooks), `app/build.gradle.kts` (signingConfig from env), `.github/workflows/release.yml` (android job)

Player behavior:
- ExoPlayer with `HlsMediaSource` from `ServerUrl.resolve(...)`; `PlayerView` via AndroidView interop; keep-screen-on.
- Error handling: `onPlayerError` HTTP 403 → `viewModel.onPlaybackAuthError()`; behind-live/stall → `Stalled` + auto `seekToDefaultPosition` retry; network-type errors → bounded retry with backoff (1s,2s,4s ×3) then Failed with retry button.
- Overlay: channel number + now title (auto-hide 3s), quality sheet (Auto + allowedProfiles(maxQuality)), stats toggle (Roboto Mono: codec/profile/backend + `player.videoFormat?.bitrate`, dropped frames from analytics listener).
- PiP: `enterPictureInPictureMode` on user-leave during playback (manifest `supportsPictureInPicture`, `configChanges`); entering PiP does NOT stop(); closing PiP window → stop(). Back from player → stop().
- Signing: `signingConfigs.release` populated only when `System.getenv("BOWTIE_KEYSTORE_FILE") != null`; release.yml `android` job **`needs: [goreleaser]`** (GoReleaser creates the GitHub Release — uploading before it exists fails), decodes `ANDROID_KEYSTORE_B64` secret to a temp file, exports env (`BOWTIE_KEYSTORE_FILE/_PASSWORD/_ALIAS/_KEY_PASSWORD` from the four repo secrets), runs `:app:assembleRelease`, renames the output to `bowtie-${GITHUB_REF_NAME#v}.apk`, attaches via `gh release upload "$GITHUB_REF_NAME"`.

- [ ] Tests: unit tests stay green; `assembleRelease` unsigned locally (no env) uses debug fallback — assert build succeeds both ways. Commit `feat: android media3 player with pip, stats, quality; signed apk on releases`

### Task 8: README + docs

**Files:** `android/README.md` (build, install-from-Releases sideload steps incl. "install unknown apps" note, switch-to-Play-Store path)

- [ ] Commit `docs: android build and sideload guide`

## Verification summary (every task)
```bash
cd android && ./gradlew :core:test :app:testDebugUnitTest :app:assembleDebug
```

## Post-plan notes
- Sequential: 1→2→3→4→5→(6,7)→8.
- Time-grid EPG deliberately excluded (spec staging).
- User validation gate: install release APK on a real phone, watch a real channel; Fire TV comes in Phase 3 on `:core`.
