# Device testing notes

> **Fire TV section only** on this track. The Roku track owns the Roku validation
> script and any other sections of this file.

## Fire TV sideload

Package: `app.bowtie.tv` · release asset: `bowtie-tv-<version>.apk`

### Prerequisites

- Fire TV stick / cube on **Fire OS 6+** (API 25+)
- Same LAN as your machine (for `adb`) or a way to fetch the APK on-device
- Bowtie server reachable from the TV (LAN `http://…` or public HTTPS)

### Option A — adb

1. **Settings → My Fire TV → Developer options**
   - Enable **ADB debugging**
   - Enable **Apps from Unknown Sources**
2. Note the IP under **Settings → My Fire TV → About → Network**
3. Connect and install:

```bash
adb connect <fire-tv-ip>:5555
adb install -r bowtie-tv-<version>.apk
# local debug build:
# adb install -r android/tv/build/outputs/apk/debug/tv-debug.apk
```

4. Open **Bowtie** from the home row (Leanback launcher).

### Option B — Downloader app

1. Install **Downloader** from the Amazon Appstore.
2. Enter a URL to `bowtie-tv-<version>.apk` (GitHub Release asset or a LAN HTTP path).
3. Download and install (Unknown Sources must be allowed).

### Smoke checklist (optional hardware gate)

1. Connect to server (LAN HTTP) → login → channel rail with guide data when EPG is configured.
2. Select a channel → playback starts; DPAD still works while video plays.
3. **Select** short = play/pause; **Select** long or **Menu** = quality drawer; stats toggle in drawer.
4. **Up/Down** zap several channels — no stranded sessions in the server admin panel afterward.
5. **Back** stops and returns to the rail.
6. Sign out; relaunch remembers the server and lands on login.

Fire OS quirks (HDMI AC-3, OEM launcher) are best validated on real hardware; the
Android TV emulator covers focus and Compose flows only.
