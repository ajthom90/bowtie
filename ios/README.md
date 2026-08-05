# Bowtie — iOS / iPadOS / tvOS

Native SwiftUI **viewer** for the Bowtie media server (v0.1.0 API). Connect to a
LAN or remote server, log in, browse channels with now/next guide data, and
play HLS with quality selection. Admin stays on the web UI.

| App | Platforms | Deployment |
|-----|-----------|------------|
| **Bowtie** | iPhone & iPad | iOS / iPadOS **17+** |
| **BowtieTV** | Apple TV | tvOS **17+** |

Shared logic lives in the local Swift package **BowtieKit** (`ios/BowtieKit/`).
App UI is under `App/Shared`, `App/iOS`, and `App/tvOS`. Zero third-party
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

## ATS / local networking

Both targets set `NSAllowsLocalNetworking = true` so cleartext HTTP works for
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
    └── tvOS/            # tvOS entry + platform views
```
