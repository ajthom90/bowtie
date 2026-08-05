# Bowtie Android

Native Kotlin/Compose viewer for the Bowtie v0.1.0 API.

## Modules

| Module | Type | minSdk | Role |
|--------|------|--------|------|
| `:core` | library | 25 | Client, models, token store, caps, guide logic (Fire-TV-ready) |
| `:app` | application | 26 | Compose UI + Media3 playback |

## Requirements

- JDK 17
- Android SDK (`ANDROID_HOME` or `local.properties` `sdk.dir`)
- Gradle wrapper (Gradle **8.13** — see `gradle/wrapper/gradle-wrapper.properties`)

## Build

```bash
./gradlew :core:test :app:testDebugUnitTest :app:assembleDebug
```

Debug APK: `app/build/outputs/apk/debug/app-debug.apk`

## Version pins

Pinned in `gradle/libs.versions.toml`. AGP ↔ Gradle ↔ Kotlin ↔ Compose set is
documented in comments there; do not bump one without checking the others.
