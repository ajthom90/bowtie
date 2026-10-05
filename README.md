<img src="docs/brand/bowtie-icon.svg" width="112" alt="Bowtie logo: a classic UHF bowtie TV antenna" align="right">

# Bowtie

[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)
[![CI](https://github.com/ajthom90/bowtie/actions/workflows/ci.yml/badge.svg)](https://github.com/ajthom90/bowtie/actions/workflows/ci.yml)

**Open-source HDHomeRun live TV streaming with hardware transcoding** — share your antenna with your family.

Bowtie is a single Go binary (with an embedded React web app) plus native apps
for iPhone/iPad, Apple TV, Mac, Android, Android TV/Fire TV and Roku. It:

- Finds your HDHomeRun tuners and streams over-the-air channels to every
  screen as HLS, with hardware transcoding (Quick Sync, NVENC, VAAPI,
  VideoToolbox) when available, captions, every broadcast audio track and 5.1
- Shows a TV guide — free from your HDHomeRun, or XMLTV / Schedules Direct —
  with search, favorites, recents and **All · Sports · Movies · News · Kids ·
  New** filters
- Pauses and rewinds live TV, and plays up to four channels at once in the
  web app's **Multiview**
- **Records** shows and whole series (new episodes only, keep the latest N),
  finds and **skips commercials** with Comskip, and picks up where you left
  off with **Continue watching**
- Signs TVs in by **scanning a QR code** with your phone; supports **parental
  controls**, per-account stream limits, a **sleep timer**, SharePlay on Apple
  devices, and a personal **M3U/XMLTV feed** for Kodi, TiviMate and friends
- Gives the admin users, tuners, channels, sessions, a storage gauge,
  recording quality, **notifications** (ntfy, Discord, webhook) and
  **backup**

**Project status:** see [CHANGELOG.md](CHANGELOG.md) for the latest release
and [docs/roadmap.md](docs/roadmap.md) for what's next.

---

## Install

### Docker image (recommended)

Images are multi-arch (`linux/amd64`, `linux/arm64`) and published to GHCR on every version tag:

```bash
docker pull ghcr.io/ajthom90/bowtie:latest
# or pin a release:
docker pull ghcr.io/ajthom90/bowtie:0.1.0
```

Compose (recommended for a permanent install):

```bash
# From a checkout, or drop deploy/docker-compose.yml into a folder and:
cd deploy
docker compose up -d
docker compose logs -f
```

The Compose file uses `ghcr.io/ajthom90/bowtie:latest`, mounts `/dev/dri` for hardware encode, and keeps data in the `bowtie-data` volume.

To build the image yourself:

```bash
docker build -f deploy/Dockerfile -t bowtie:dev .
docker run --rm -p 8400:8400 -v bowtie-data:/data bowtie:dev
```

### Release binaries

Each GitHub Release attaches pre-built `bowtie` binaries (web UI embedded, `CGO_ENABLED=0`) plus client sideload packages when present:

| Asset | Notes |
|-------|--------|
| `bowtie_*_darwin_arm64.tar.gz` | macOS Apple Silicon server binary |
| `bowtie_*_linux_amd64.tar.gz` | Linux x86_64 server binary |
| `bowtie_*_linux_arm64.tar.gz` | Linux arm64 server binary |
| `bowtie-<version>.apk`, `bowtie-android.apk` | Android phone/tablet APK ([install guide](docs/install/android.md)) |
| `bowtie-tv-<version>.apk`, `bowtie-tv.apk` | Fire TV / Android TV APK |
| `bowtie-roku-<version>.zip` | Roku channel sideload zip |

```bash
# Example: latest Linux amd64
curl -sL "https://github.com/ajthom90/bowtie/releases/latest/download/bowtie_$(curl -sL https://api.github.com/repos/ajthom90/bowtie/releases/latest | grep -oP '"tag_name": "v\K[^"]+')_linux_amd64.tar.gz" \
  | tar -xz
./bowtie --data-dir ./data
```

Or download from the [Releases](https://github.com/ajthom90/bowtie/releases) page. You still need **FFmpeg** on `PATH` (or set `BOWTIE_FFMPEG_PATH`).

### From source

Requirements: Go 1.22+, Node 22+, FFmpeg on `PATH`.

```bash
make build          # builds web UI into the embed path, then the Go binary
./dist/bowtie --data-dir ./data
```

Dev loop (two terminals):

```bash
make dev-server     # Go API on :8400
make dev            # Vite on :5173, proxies /api → :8400
```

---

## Quickstart

On first start, Bowtie creates an `admin` user and **prints the password once** in the logs:

```text
created admin user "admin" with password "…" — change it after login
```

Then:

1. Open **http://localhost:8400** (or `http://<host>:8400` on your LAN).
2. Log in as `admin` and **change the password** (Profile / password API).
3. **Admin → Tuners** — add your HDHomeRun by IP if it is not discovered automatically.
4. **Admin → Channels** — enable the channels you want to stream (watch works
   without guide data; admins can Preview disabled channels).
5. **Admin → Settings** — optional XMLTV / Schedules Direct, encoder, and HEVC
   (restart-free; see [Configuration](#configuration)).
6. Open the **guide**, pick a channel, and watch.

Persistent data lives under `--data-dir` / `BOWTIE_DATA_DIR` (Docker: the `bowtie-data` volume at `/data`). HLS segments use a tmpfs mount in Compose so they do not wear the volume.

---

## Hardware transcode support

Bowtie shells out to FFmpeg (never links it). Encoder selection defaults to `auto`.

| Backend        | Platform                         | Status            | Notes                                      |
|----------------|----------------------------------|-------------------|--------------------------------------------|
| **VideoToolbox** | macOS (Apple Silicon / Intel)  | Dev / first-class | Preferred on Mac for local development     |
| **QSV**        | Intel CPUs (Linux, `/dev/dri`)   | Production        | Image includes `intel-media-va-driver-non-free` (amd64) |
| **VAAPI**      | Intel / AMD GPUs (Linux)         | Production        | `mesa-va-drivers` in the runtime image     |
| **NVENC**      | NVIDIA GPUs                      | Community-tested  | Needs host NVIDIA runtime / drivers        |
| **software**   | Anywhere                         | Fallback          | `libx264` when no hardware encoder works   |

**Preferred:** set encoder and Allow HEVC in **Admin → Settings → Transcode**
(restart-free). `BOWTIE_ENCODER` / `encoder:` in `<dataDir>/config.yaml` are
**first-boot seeds only** once the setting exists in the DB (`auto` \|
`videotoolbox` \| `qsv` \| `vaapi` \| `nvenc` \| `software`).

The Docker Compose file passes `/dev/dri` into the container for QSV/VAAPI on Intel NUCs and similar hosts.

---

## EPG setup

Guide data is **optional** — channels are fully watchable with no EPG configured.

### Free guide from your HDHomeRun (default)

Out of the box Bowtie fetches the free guide SiliconDust offers every
HDHomeRun owner (about 2-3 days ahead; 14 days with an HDHomeRun DVR
subscription). No account or setup is needed: Bowtie reads each tuner's
`DeviceAuth` from its `discover.json`, downloads the guide from
`api.hdhomerun.com` every 20-28 hours at a random time, and maps each
unmapped channel to it by guide number (for example `9.1`). Channels you
mapped yourself are never changed. Turn it off with
`PUT /api/v1/admin/settings` `{"hdhomerun": {"enabled": false}}`
(setting `epg.hdhomerun`); its health is under `hdhomerun` in
`GET /api/v1/admin/epg/status`. It works alongside XMLTV and Schedules Direct.

SiliconDust limits how often each tuner may download the guide: once a
tuner has used its allowance, downloads get HTTP 403 for a while. After a
403 Bowtie waits the same 20-28 hours before trying again (it remembers this
across restarts, and Admin → EPG → Refresh follows the same schedule).
**Only one Bowtie server per tuner should download it**: on test or
secondary servers that share the tuner, set `BOWTIE_HDHOMERUN_GUIDE=off`.
The free guide carries titles, episodes, series IDs (series recording works)
and new/repeat flags, but **no age ratings**; to limit accounts by rating
(parental controls), add Schedules Direct. Channel restrictions work either way.

### XMLTV and Schedules Direct

**Preferred:** configure XMLTV and/or Schedules Direct in **Admin → Settings**
(lineup picker, clear credentials by emptying username). Changes apply without
restart; **Admin → EPG** is status + Refresh only. Map each enabled channel to
an EPG channel ID under **Admin → Channels**.

Env / `config.yaml` EPG keys (below) are **first-boot seeds only** — after the
DB has those keys, the UI is the control plane (see [Configuration](#configuration)).

### XMLTV (seed / optional file)

```yaml
xmltv:
  source: "https://example.com/guide.xml"   # or a local file path
  refreshHours: 12
```

### Schedules Direct (seed / optional file)

```yaml
schedulesDirect:
  username: "your-sd-username"
  password: "your-sd-password"
  lineupId: "USA-OTA-90210"                 # your SD lineup ID
```

---

## Remote access

Bowtie itself speaks plain HTTP on port **8400**. For HTTPS and off-LAN access, see:

**[docs/deploy/remote-access.md](docs/deploy/remote-access.md)** · TrueNAS: **[docs/deploy/truenas.md](docs/deploy/truenas.md)**

Copy-paste examples for:

- **Caddy** reverse proxy (automatic Let's Encrypt)
- **Cloudflare Tunnel** (no open inbound ports)
- **Tailscale** Serve / Funnel (private mesh or public HTTPS)

HDHomeRun **UDP discovery** does not cross Docker bridge networks. Use
`network_mode: host` in Compose (commented in `deploy/docker-compose.yml`) or
add devices by IP.

---

## Configuration

| Source | Keys |
|--------|------|
| Flag / env | `--data-dir` / `BOWTIE_DATA_DIR` (default `./data`, Docker `/data`) |
| Env (infra every start) | `BOWTIE_LISTEN_ADDR`, `BOWTIE_FFMPEG_PATH`, `BOWTIE_SEGMENT_DIR`, `BOWTIE_DEVICES`, `BOWTIE_MULTITRACK` (`off` disables captions, extra audio and 5.1), `BOWTIE_HDHOMERUN_GUIDE` (`off` stops this server downloading the free HDHomeRun guide — use it on test servers sharing a tuner), `BOWTIE_RECORDINGS_DIR` (DVR, default `<data>/recordings`), `BOWTIE_DVR_MIN_FREE_GB` (default 20), `BOWTIE_COMSKIP_PATH` / `BOWTIE_COMSKIP_INI` ([commercial detection](#commercial-detection)) |
| Env / yaml (first-boot seeds) | `BOWTIE_ENCODER`; yaml `xmltv.*`, `schedulesDirect.*`, `encoder` / allow HEVC |
| Control plane (runtime) | **Admin → Settings** — XMLTV, Schedules Direct, encoder, HEVC, buffer, adaptive quality (DB-backed) |
| Control plane (DVR) | **Admin → Recordings** — padding, and recording quality: 720p (default, ~1.7 GB/hour) or up to 1080p (1080i kept at full resolution, deinterlaced; ~3.4 GB/hour), applied to recordings converted afterwards |
| File | `<dataDir>/config.yaml` |

**Seeds vs overrides:** product keys (EPG sources, encoder, HEVC) are
presence-seeded into the SQLite `settings` table on first boot (or first
upgrade that introduces a key). After a key exists — including empty string —
Admin → Settings wins; env/yaml for those keys is **not** a live override.
Infra keys (listen, data dir, segments, FFmpeg path, device IP list) still
apply every process start.

Default listen address: `:8400`. Health check: `GET /healthz` → `ok`.

## Commercial detection

When [Comskip](https://github.com/erikkaashoek/Comskip) is available, Bowtie
finds the commercial breaks in each finished recording, and every app's
recording player shows **Skip ad** while you're in one (web and Mac: or press
**S**). Turn on **Skip ads automatically** (web player header; app Settings →
Playback; Roku Settings) to skip each break once. Detection runs in the background after a recording is
ready, one at a time at low CPU priority, and never holds up recording or
conversion. Recordings made before Comskip was available are scanned too,
newest first. The breaks are in the recording API as `commercials`.

It's optional: without Comskip nothing changes. The Docker image includes it.
For other installs, put `comskip` on the `PATH` or point to it:

| Env | |
|-----|---|
| `BOWTIE_COMSKIP_PATH` | Comskip binary (default `comskip` on the `PATH`; not found = detection off; `off` turns it off, e.g. in Docker) |
| `BOWTIE_COMSKIP_INI` | Your own `comskip.ini` (default: Bowtie writes its settings to `<data>/comskip.ini` on first use; edit that file to tune detection) |

Detection is heuristic (black frames, the station logo, aspect ratio
changes), so it can miss a break or mark part of the show. After editing
`comskip.ini`, an admin can press **Find ads again** in the web recording
player (or `POST /api/v1/recordings/{id}/commercials/detect`). If Comskip can't
run at all (missing library, an ini without `output_edl=1`), detection stops
until the next restart and nothing is marked; a recording it fails on is
tried again after a restart.

## Notifications

Bowtie can tell you when something needs attention, on your phone or in a
chat, without any app-store push setup. In **Admin → Settings →
Notifications**, paste a URL, pick the events, **Save**, and **Send test**:

- **ntfy** (free phone app): `https://ntfy.sh/your-topic` (pick a hard-to-guess
  topic), or your own ntfy server — any host with `ntfy` in its name. For a
  protected topic use `https://user:pass@ntfy.example.com/topic`.
- **Discord**: a channel webhook URL (`https://discord.com/api/webhooks/…`).
- **Anything else** gets a JSON POST:
  `{"event": "recordingFailed", "title": "…", "message": "…", "recordingId": 42, "time": "2026-10-04T18:30:00Z"}`
  (Home Assistant, n8n, your own script, …).

Events: a recording failed (with the reason — no tuner, no signal, disk
full…; skipped episodes don't count), disk space is low (checked hourly after
old recordings are cleaned up), guide data hasn't updated for a day, and
(off by default) a recording is ready to watch. Each event is sent at most
once every 6 hours (a failed recording once), with one retry 30 s later if
the server didn't answer. The URL can contain a secret, so Bowtie only ever
logs its host name.

## Backup and restore

**Admin → Settings → Download backup** (or `GET /api/v1/admin/backup` with an
admin token) saves a snapshot of the database: accounts, channels, guide
mappings, series rules, the recording list and settings. It is taken safely
while Bowtie runs. Recorded video is not included — back up
`<data>/recordings` (or `BOWTIE_RECORDINGS_DIR`) separately if you want it.
The file holds password hashes, the Schedules Direct password and the
notification URL; keep it private. Token-signing keys and sign-in sessions are left out, so a restored
server makes new keys and everyone signs in again.

The snapshot is written next to `bowtie.db` while the download is prepared
(other requests wait a moment on a large guide).

To restore, stop Bowtie, replace `<data>/bowtie.db` with the backup file
(delete any `bowtie.db-journal`, `bowtie.db-wal` or `bowtie.db-shm` next to
it), and start Bowtie.

---

## API

All JSON API routes live under `/api/v1`. The OpenAPI 3.0 document is the **client contract** for the Phase 2/3 native apps (iOS/tvOS, Android, Roku, Fire TV) and any third-party client:

**[docs/api/openapi.yaml](docs/api/openapi.yaml)**

A server test (`TestOpenAPICoversRoutes`) asserts that every registered `/api/v1/...` route appears in the spec and that the spec has no orphaned path+method pairs.

---

## Development

```bash
cd server && CGO_ENABLED=0 go test ./...   # no real HDHomeRun / FFmpeg required
cd web && npm ci && npm test && npm run build
```

---

## Apps

- **iOS / iPadOS / tvOS** — native SwiftUI viewer: see [`ios/README.md`](ios/README.md) (build, test, sideload).
- **macOS** — native Mac app (macOS 14+) built from the same Xcode project (`BowtieMac` scheme): a sidebar of channels (Recent, Favorites) and recordings, an `AVPlayerView` player with picture in picture and full screen, and keyboard shortcuts (Space, L for live, ⌘↑/⌘↓, ⌘F). Not yet published to the Mac App Store or notarized; see [`ios/README.md`](ios/README.md#macos-app).
- **Android** — native Kotlin/Compose viewer: see [`android/README.md`](android/README.md) (build). To install, open `https://<your-server>/android` (phone) or `/tv` (Fire TV, via the Downloader app) — see [docs/install/android.md](docs/install/android.md).
- **Roku** — BrighterScript SceneGraph channel: see [`roku/README.md`](roku/README.md) (`make roku-package` → sideloadable zip). On-device gate: [`docs/deploy/roku-testing.md`](docs/deploy/roku-testing.md).
- **Windows** — native WinUI 3 app (x64 and ARM64) in progress on the `feat/windows` branch; not released yet.
- **Xbox** — no native app; install Kodi from the Microsoft Store and add your M3U/XMLTV feed (web app → Account → “Use Bowtie in other apps”).
- **PlayStation** — no way to install third-party apps; not supported.
- **Any other device** — the web app works in any modern browser, including smart-TV browsers. On a phone, tablet or computer you can install it (**Add to Home Screen** / **Install app**) to get its own icon and window; browsers offer that over HTTPS (see [Remote access](#remote-access)) or on `localhost`.

---

## License

Licensed under the [Apache License, Version 2.0](LICENSE).
