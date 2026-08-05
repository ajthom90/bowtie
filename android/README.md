# Bowtie Android

Native Kotlin/Compose viewer for the Bowtie v0.1.0 API. Targets **Android 8.0+**
(API 26 / Oreo and newer). Phone and tablet only in Phase 2; Fire TV reuses
`:core` in a later phase.

## Modules

| Module | Type | minSdk | Role |
|--------|------|--------|------|
| `:core` | library | 25 | Client, models, token store, caps, guide logic (Fire-TV-ready) |
| `:app` | application | 26 | Compose UI + Media3 playback |

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
make android-test   # :core:test + :app:testDebugUnitTest
make android-apk    # :app:assembleDebug
```

From `android/`:

```bash
./gradlew :core:test :app:testDebugUnitTest
./gradlew :app:assembleDebug
```

Debug APK: `app/build/outputs/apk/debug/app-debug.apk`

Release APK (local, uses debug key unless `BOWTIE_KEYSTORE_*` env vars are set):

```bash
./gradlew :app:assembleRelease
# → app/build/outputs/apk/release/app-release.apk
```

## Install from GitHub Releases (sideload)

Each version tag (`v*`) runs the release workflow, which attaches a **signed**
release APK to the GitHub Release (e.g. `bowtie-0.1.0.apk`).

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
├── core/                       # library: client, models, store, caps, guide
└── app/                        # application: Compose UI + Media3
    └── src/main/kotlin/app/bowtie/
        ├── ui/                 # Connect, Login, ChannelList, Player, Settings
        └── …ViewModel.kt
```

## Version pins

Pinned in `gradle/libs.versions.toml`. AGP ↔ Gradle ↔ Kotlin ↔ Compose set is
documented in comments there; do not bump one without checking the others.
