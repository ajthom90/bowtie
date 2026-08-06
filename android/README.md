# Bowtie Android

Native Kotlin/Compose viewer for the Bowtie v0.1.0 API. Phone/tablet (**Android
8.0+** / API 26) and **Fire TV** (**API 25+**, Fire OS 6+) share `:core`
(ViewModels, client, Media3 [PlayerEngine](core/src/main/kotlin/app/bowtie/core/player/PlayerEngine.kt)).

## Modules

| Module | Type | minSdk | Role |
|--------|------|--------|------|
| `:core` | library | 25 | Client, models, token store, caps, guide logic, PlayerEngine |
| `:app` | application | 26 | Phone Compose UI + Media3 PlayerView |
| `:tv` | application | 25 | Fire TV (Compose for TV) + Media3 PlayerView |

## Requirements

- **JDK 17** (`java -version`)
- **Android SDK** — either:
  - `local.properties` with `sdk.dir=/path/to/Android/sdk` (created by Android Studio), or
  - `ANDROID_HOME` / `ANDROID_SDK_ROOT` pointing at the SDK
- Gradle wrapper is checked in (Gradle **8.13** — see `gradle/wrapper/gradle-wrapper.properties`)

No Android Studio required for CLI build/test; Studio is optional for UI work.

## Build & test

From the repo root:

```bash
make android-test   # :core:test + :app + :tv unit tests
make android-apk    # :app:assembleDebug + :tv:assembleDebug
```

From `android/`:

```bash
./gradlew :core:test :app:testDebugUnitTest :tv:testDebugUnitTest
./gradlew :app:assembleDebug :tv:assembleDebug
```

Debug APKs:

- Phone: `app/build/outputs/apk/debug/app-debug.apk`
- Fire TV: `tv/build/outputs/apk/debug/tv-debug.apk`

Release APKs (local, uses debug key unless `BOWTIE_KEYSTORE_*` env vars are set):

```bash
./gradlew :app:assembleRelease :tv:assembleRelease
# → app/build/outputs/apk/release/app-release.apk
# → tv/build/outputs/apk/release/tv-release.apk
```

## Install from GitHub Releases (sideload)

Each version tag (`v*`) runs the release workflow, which attaches **signed**
release APKs to the GitHub Release:

- Phone: `bowtie-<version>.apk`
- Fire TV: `bowtie-tv-<version>.apk`

1. On the phone: **Settings → Apps → Special app access → Install unknown apps**
   (wording varies by OEM) and allow your browser or Files app to install APKs.
2. Open the [Releases](https://github.com/ajthom90/bowtie/releases) page on the
   device (or transfer the APK via USB/AirDrop-equivalent).
3. Download the `.apk` asset for the tag you want and open it to install.
4. Later tags install **over** previous installs: the APK is signed with the
   project's release keystore (stored only in GitHub Actions secrets), so Android
   treats upgrades as the same app. Do not re-sign with a different key if you
   want seamless updates.

Debug builds from `assembleDebug` use the debug keystore and **cannot** update
over a release-signed install (and vice versa) without uninstalling first.

## Fire TV

Package id: `app.bowtie.tv` (separate from the phone app). minSdk **25** (Fire OS 6+).

### Build

```bash
cd android
./gradlew :tv:assembleDebug          # debug
./gradlew :tv:assembleRelease        # release (debug key if no BOWTIE_KEYSTORE_*)
```

### adb sideload

1. On the Fire TV stick: **Settings → My Fire TV → Developer options** — enable
   **ADB debugging** and **Apps from Unknown Sources**.
2. Note the device IP (**Settings → My Fire TV → About → Network**).
3. From a machine with `adb`:

```bash
adb connect <fire-tv-ip>:5555
adb install -r tv/build/outputs/apk/debug/tv-debug.apk
# or a release asset:
# adb install -r bowtie-tv-0.1.0.apk
```

4. Launch from the Fire TV home row (Leanback launcher) — look for **Bowtie**.

### Downloader app (no computer)

1. Install **Downloader** from the Amazon Appstore on the Fire TV.
2. Open a release URL that serves `bowtie-tv-<version>.apk` (GitHub Releases
   asset link, or a LAN HTTP path you control).
3. Download → install when prompted (Unknown Sources must be on).

### Remote key map (player)

| Key | Action |
|-----|--------|
| **Select / DPAD Center** short-press | Play / pause |
| **Select / DPAD Center** long-press (≥700 ms) | Open transport / quality drawer |
| **Menu** | Open transport / quality drawer |
| **DPAD Up / Down** | Channel zap (session-replace, debounced) |
| **Back** | Close drawer if open; otherwise stop playback and return to the rail |

Stats toggle lives inside the quality drawer. Media3 `PlayerView` never owns DPAD
focus — Compose handles keys so zap and drawer keep working while video plays.

## First run — connect to your server

1. Launch **Bowtie**. You land on **Connect to your server**.
2. Enter the server address, for example:
   - LAN: `http://192.168.1.50:8400` or `192.168.1.50:8400` (scheme defaults to `http`)
   - Remote (HTTPS via your reverse proxy): `https://tv.example.com`
3. Tap **Validate**. The app probes `{server}/healthz` and only continues on HTTP 200.
4. Sign in with a Bowtie username and password (admin is created on first server start).
5. Pick a channel from the list (now/next guide data when EPG is configured), then watch.
6. **Settings** covers change password, change server, and sign out.

Cleartext HTTP is allowed for LAN use (`network_security_config` permits
cleartext globally — Android cannot scope cleartext to private IP ranges).
Public hostnames should use HTTPS.

## Play Store path (later)

v1 distribution is **family sideload only** — no Play Store listing yet.

When you are ready for Play:

1. Generate a **new** upload keystore (or enroll **Play App Signing** and use
   Google's app-signing key). Do **not** publish the GitHub-secrets keystore
   used for sideload APKs; a leaked release key would allow forged same-signature
   updates for every family device already installed.
2. Bump `applicationId` only if you intentionally want a separate Play package;
   same `applicationId` + new signing key means users must uninstall the
   sideload build first (Android will not update across signing keys).
3. Wire Play Console / CI upload separately from the current
   `gh release upload` APK path in `.github/workflows/release.yml`.

## Layout

```
android/
├── gradle/libs.versions.toml   # pinned AGP / Kotlin / Compose / Media3
├── core/                       # library: client, VMs, PlayerEngine, caps, guide
├── app/                        # phone application: Compose UI + PlayerView
└── tv/                         # Fire TV application: Compose for TV + PlayerView
```

## Version pins

Pinned in `gradle/libs.versions.toml`. AGP ↔ Gradle ↔ Kotlin ↔ Compose set is
documented in comments there; do not bump one without checking the others.
