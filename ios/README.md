# Bowtie — iOS / iPadOS / tvOS / macOS

Native SwiftUI **viewer** for the Bowtie media server (v0.1.0 API). Connect to a
LAN or remote server, log in, browse channels with now/next guide data, and
play HLS with quality selection. Admin stays on the web UI.

| App | Platforms | Deployment |
|-----|-----------|------------|
| **Bowtie** | iPhone & iPad | iOS / iPadOS **17+** |
| **BowtieTV** | Apple TV | tvOS **17+** |
| **BowtieMac** | Mac | macOS **14+** (Sonoma) |

Shared logic lives in the local Swift package **BowtieKit** (`ios/BowtieKit/`).
App UI is under `App/Shared`, `App/iOS`, `App/tvOS`, and `App/macOS`. Zero third-party
runtime dependencies.

## Prerequisites

- **Xcode 15+** (Swift 5.10+)
- [XcodeGen](https://github.com/yonaskolb/XcodeGen) — `brew install xcodegen`
- A running Bowtie server (see the repo root README) to connect to after install

## Generate & open

```bash
cd ios
xcodegen generate
open Bowtie.xcodeproj
```

From the repo root:

```bash
make ios-gen          # xcodegen generate
make ios-test         # generate + BowtieKit tests + both app builds
```

## Schemes

| Scheme     | Platform     | Description                          |
|------------|--------------|--------------------------------------|
| `Bowtie`   | iOS / iPadOS | Phone & tablet app (+ SharedTests)   |
| `BowtieTV` | tvOS         | Apple TV app                         |
| `BowtieMac`| macOS        | Mac app                              |

## Build & test (CLI)

```bash
cd ios
xcodegen generate

# Package unit tests (no simulator — BowtieKit includes macOS for tests)
swift test --package-path BowtieKit

# App builds (generic destinations; no device name hard-coding)
xcodebuild -project Bowtie.xcodeproj -scheme Bowtie \
  -destination 'generic/platform=iOS Simulator' build
xcodebuild -project Bowtie.xcodeproj -scheme BowtieTV \
  -destination 'generic/platform=tvOS Simulator' build
# Ad-hoc signed (no team needed); drop the overrides to sign with your team.
xcodebuild -project Bowtie.xcodeproj -scheme BowtieMac \
  -destination 'platform=macOS' \
  CODE_SIGN_STYLE=Manual CODE_SIGN_IDENTITY=- DEVELOPMENT_TEAM= build

# SharedTests (app-layer unit tests) — resolve a runtime iPhone simulator UDID
UDID=$(xcrun simctl list devices available -j | python3 -c "
import json, sys
data = json.load(sys.stdin)
for _runtime, devices in data.get('devices', {}).items():
    for device in devices:
        if device.get('isAvailable') and 'iPhone' in device.get('name', ''):
            print(device['udid'])
            sys.exit(0)
sys.exit('No available iPhone simulator found')
")
xcodebuild -project Bowtie.xcodeproj -scheme Bowtie \
  -destination "id=$UDID" \
  -only-testing:SharedTests \
  test
```

Never hard-code a simulator device name in scripts or CI — Xcode runner images
change default device sets. Prefer a UDID resolved from
`xcrun simctl list devices available -j` as above.

## Sideload to a device

v1 is **family / sideload only** — no App Store listing yet.

### iPhone / iPad

1. Open `Bowtie.xcodeproj` in Xcode (`make ios-gen` first if needed).
2. Select the **Bowtie** scheme and your physical device as the run destination.
3. Under the Bowtie target → **Signing & Capabilities**, choose your **Team**
   (a free personal Apple ID is enough for development signing).
4. Press **Run** (⌘R). Trust the developer certificate on the device if prompted
   (**Settings → General → VPN & Device Management**).

**Free provisioning caveat:** with a free Apple ID, the installed app expires
after **7 days** and must be reinstalled from Xcode. A paid Apple Developer
Program membership extends this and unlocks proper distribution later.

### Apple TV

1. Put the Apple TV and Mac on the same network; enable **Remotes and Devices →
   Remote App and Devices** (or pair wirelessly via Xcode’s Devices and
   Simulators window).
2. Select the **BowtieTV** scheme and the Apple TV as the run destination.
3. Set **Signing & Capabilities** Team as above, then **Run** (⌘R).

### Mac

Select the **BowtieMac** scheme and **My Mac**, set the Team under Signing &
Capabilities, and Run (⌘R). See [macOS app](#macos-app) below.

### TestFlight (later)

TestFlight / App Store distribution is **not** set up in this tree. When that
lands, it will use a paid team, archive/export, and App Store Connect — not
free personal-team sideload.

## First run — connect walkthrough

1. Launch the app. You land on **Connect**.
2. Enter your Bowtie server address. Both forms work:
   - Remote / TLS: `https://tv.example.com`
   - LAN cleartext: `http://192.168.1.50:8400` (or `192.168.1.50:8400` — the
     app adds `http://` when the scheme is missing)
3. Tap **Validate**. The app calls `GET /healthz` (2s timeout). On failure you
   see: *Couldn't reach a Bowtie server there. Check the address and try again.*
4. On success, sign in with a viewer (or admin) account from the server.
5. The channel list shows now/next guide data. Pick a channel to play.
6. **Settings** (gear): server info, change password, sign out. Changing the
   server URL signs you out.

Cleartext HTTP is allowed for **LAN IPs and `.local` hostnames only**
(`NSAllowsLocalNetworking`). Public hostnames still require HTTPS (use a
reverse proxy or tunnel — see `docs/deploy/remote-access.md`).

## macOS app

`BowtieMac` is a native SwiftUI + AppKit app (not Catalyst), macOS 14+. It
shares `App/Shared` and BowtieKit with iOS and tvOS: the same `AppModel`,
`ConnectView` / `LoginView` / `SettingsView`, `PlayerModel` (session replace,
15 s heartbeats, 422 / 404 / tuners-busy handling), `PlayerBridge` (stall
recovery, live edge, out-of-window clamp), `RecordingPlayerController` (VOD
resume and position saves), `RecordFlow`, and `MediaSelectionMemory`.

- **Window:** one main window (`Window`, not `WindowGroup`, so two windows
  can't hold two tuners). A `NavigationSplitView` sidebar lists Recordings,
  Recent, Favorites and Channels; right-click a channel to favorite it or
  record now / next. The detail pane shows live TV or recordings.
- **Player:** `AVPlayerView` with floating controls (live DVR scrubber, audio
  and captions menu, picture in picture, full-screen button) plus Bowtie's
  overlay (channel, Live pill, quality ceiling, stats) on hover. Leaving the
  live view stops the session unless PiP is up; then it stops when PiP ends.
- **Keys (Playback / View menus):** Space play/pause, L go to live,
  ⌘↑ / ⌘↓ channel up / down (sidebar order, wrapping), ⌘F full screen
  (hides the sidebar). They only act in the signed-in main window, so typing
  in Connect, Login or Settings is unaffected. ⌘, opens Settings.
- **Quit** stops the live session (and saves a recording's position) first,
  waiting at most 2 s for the server. Closing the window quits.
- **Sandbox:** `com.apple.security.app-sandbox` +
  `com.apple.security.network.client` (`App/macOS/BowtieMac.entitlements`);
  ATS allows local networking as on iOS.
- **Caps:** reports `maxHeight` 1080 like iOS.
- **Keychain:** the login is stored with the same `KeychainSessionStore`, which
  on macOS is the login (file-based) keychain. Ad-hoc / re-signed dev builds
  change the code signature, so macOS may ask to allow access to the saved
  item after a rebuild; team-signed builds don't.
- **Icon:** `App/macOS/Assets.xcassets/AppIcon.appiconset` is generated from
  the iOS `AppIcon-1024.png` with `sips`. When the logo changes, regenerate:

  ```bash
  cd ios/App/macOS/Assets.xcassets/AppIcon.appiconset
  for px in 16 32 64 128 256 512 1024; do
    sips -z $px $px ../../../iOS/Assets.xcassets/AppIcon.appiconset/AppIcon-1024.png --out icon_$px.png
  done
  ```

### Distributing the Mac app

Nothing is published yet. Pick one:

- **Mac App Store / TestFlight (universal purchase):** the target uses the iOS
  bundle id `app.bowtie`. In App Store Connect, open the existing Bowtie app →
  **Add Platform → macOS**. In Xcode Cloud, add a workflow (or an action on the
  existing one) that archives the **BowtieMac** scheme for macOS and
  distributes to TestFlight / App Store; `ci_scripts/ci_post_clone.sh` already
  generates the project. The Mac needs its own screenshots, and App Review
  will check the sandbox entitlements.
- **Direct download (Developer ID):** archive BowtieMac, export with
  **Developer ID** signing (hardened runtime is already on), then
  `xcrun notarytool submit Bowtie.zip --keychain-profile <profile> --wait`
  and `xcrun stapler staple Bowtie.app` before zipping / DMG-ing it.

## ATS / local networking

All three apps set `NSAllowsLocalNetworking = true` so cleartext HTTP works for
LAN IPs and `.local` hostnames. Public hostnames still require HTTPS.

## Layout

```
ios/
├── project.yml          # XcodeGen source of truth (.xcodeproj is gitignored)
├── BowtieKit/           # Shared Swift package (models, client, session, guide)
└── App/
    ├── Shared/          # Theme, view models, Connect/Login/Settings/…
    ├── SharedTests/     # App-layer XCTest bundle (hosted by Bowtie)
    ├── iOS/             # iOS entry + platform views
    ├── macOS/           # macOS entry, split view, AVPlayerView player
    └── tvOS/            # tvOS entry + platform views
```
