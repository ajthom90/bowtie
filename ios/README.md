# Bowtie — iOS / iPadOS / tvOS

Native SwiftUI viewer for the Bowtie media server. Targets **iOS/iPadOS 17+** and **tvOS 17+**.

## Requirements

- Xcode 15+ (Swift 5.10+)
- [XcodeGen](https://github.com/yonaskolb/XcodeGen) (`brew install xcodegen`)

## Generate & open

```bash
cd ios
xcodegen generate
open Bowtie.xcodeproj
```

Or from the repo root:

```bash
make ios-gen
```

## Schemes

| Scheme    | Platform     | Description        |
|-----------|--------------|--------------------|
| `Bowtie`  | iOS / iPadOS | Phone & tablet app |
| `BowtieTV`| tvOS         | Apple TV app       |

Shared logic lives in the local Swift package **BowtieKit** (`ios/BowtieKit/`). App UI is under `App/Shared`, `App/iOS`, and `App/tvOS`.

## Build (CLI)

```bash
cd ios
xcodegen generate

# Package unit tests (no simulator needed — BowtieKit includes macOS)
swift test --package-path BowtieKit

# App builds
xcodebuild -project Bowtie.xcodeproj -scheme Bowtie \
  -destination 'generic/platform=iOS Simulator' build
xcodebuild -project Bowtie.xcodeproj -scheme BowtieTV \
  -destination 'generic/platform=tvOS Simulator' build
```

From the repo root: `make ios-test`.

## Sideload to a device

1. Open `Bowtie.xcodeproj` in Xcode.
2. Select your Team under **Signing & Capabilities** for the target.
3. Plug in an iPhone/iPad or select an Apple TV on the network.
4. Choose the device as the run destination and press **Run** (⌘R).

No App Store distribution in v1 — family/sideload only. For a free Apple ID, the app expires after 7 days and must be reinstalled.

## ATS / local networking

Both targets set `NSAllowsLocalNetworking = true` so cleartext HTTP works for LAN IPs and `.local` hostnames (e.g. `http://192.168.1.50:8400`). Public hostnames still require HTTPS.

## Layout

```
ios/
├── project.yml          # XcodeGen source of truth
├── BowtieKit/           # Shared Swift package (models, client, session)
└── App/
    ├── Shared/          # Theme, view models, shared screens
    ├── iOS/             # iOS entry + platform views
    └── tvOS/            # tvOS entry + platform views
```
