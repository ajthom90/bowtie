# Bowtie — Phase 1 Design: Server + Web Viewer

**Date:** 2026-08-04
**Status:** Approved
**Phase:** 1 of 3 (Phase 2: iOS/iPadOS/tvOS + Android. Phase 3: Roku + Fire TV.)

## What Bowtie is

Bowtie is an open-source live-TV streaming server for HDHomeRun network tuners. It
transcodes over-the-air broadcasts (MPEG-2 video, AC-3 audio) into formats modern
clients play natively, serves them over HLS, provides a TV guide, and lets an
administrator create accounts for family members — including people outside the
home network — without the sharing restrictions of Plex "Home."

Named for the classic bowtie UHF antenna. Licensed **Apache-2.0** (permissive, with
an explicit patent grant that matters for video software). Hosted as a monorepo at
`github.com/ajthom90/bowtie`.

## Decisions of record

| Topic | Decision |
|---|---|
| Name | Bowtie |
| Build order | Phase 1: server + web viewer. Phase 2: iOS/tvOS, Android. Phase 3: Roku, Fire TV |
| Server language | Go (single static binary; FFmpeg does the media heavy lifting) |
| Streaming architecture | HLS with shared transcode sessions (Approach A) |
| Tuner hardware | HDHomeRun CONNECT/FLEX Duo/Quatro (ATSC 1.0: MPEG-2 + AC-3). ATSC 3.0 out of scope for v1 |
| Transcode dev target | Apple Silicon / VideoToolbox (developer machine) |
| Transcode prod target | Intel QSV (owner's server). NVENC + VAAPI implemented but community-tested |
| EPG | XMLTV (file or URL) **and** Schedules Direct JSON API, both at launch |
| Remote access | BYO reverse proxy / tunnel. Bowtie serves plain HTTP; docs cover Caddy, Cloudflare Tunnel, Tailscale |
| DVR | Out of scope for v1; architecture stays DVR-friendly (segment pipeline could persist) |
| Repo | Monorepo under `ajthom90/bowtie` |
| License | Apache-2.0 |
| Distribution | Multi-arch Docker (amd64 + arm64) via GitHub Actions to ghcr.io; bare binaries via GoReleaser |

## Architecture overview

A single Go binary embeds the compiled web UI (`go:embed`). State lives in a data
directory: a SQLite database (`modernc.org/sqlite`, pure Go — no CGO, clean
cross-compilation) for users, channels, EPG, and settings, plus a segment
ring-buffer directory for live HLS output (tmpfs recommended; path configurable).

FFmpeg runs as an **external supervised process**, never linked, keeping Apache-2.0
licensing clean alongside FFmpeg's LGPL/GPL.

Components and responsibilities:

- **Tuner Manager** — HDHomeRun discovery, status polling, tuner pool.
- **Channel Manager** — lineup ingestion, channel enable/disable, EPG mapping.
- **EPG Service** — XMLTV + Schedules Direct ingestion, normalization, refresh.
- **Stream Manager** — sessions, codec negotiation, FFmpeg supervision, HLS output.
- **Auth** — users, roles, password hashing, tokens.
- **HTTP API** — versioned REST (`/api/v1`) + HLS delivery + embedded web UI.

### Monorepo layout

```
bowtie/
  server/     Go server
  web/        React + TypeScript web viewer & admin (embedded at build time)
  ios/        Phase 2 — Swift/SwiftUI, one codebase for iOS/iPadOS/tvOS
  android/    Phase 2 — Kotlin/Jetpack Compose + Media3
  roku/       Phase 3 — BrightScript/SceneGraph
  firetv/     Phase 3 — Fire TV (Kotlin; shares a core module with android/)
  docs/       specs, plans, API contract (OpenAPI)
  deploy/     Dockerfile, docker-compose examples, reverse-proxy configs
  .github/    Actions workflows
```

## Tuner & channel management

- **Discovery:** HDHomeRun UDP broadcast discovery (port 65001) plus manual IP
  entry. Multiple devices supported; their tuners aggregate into one pool.
- **Lineup:** read `lineup.json` per device. Admin enables/disables channels —
  only enabled channels are visible to viewers — and maps each to an EPG channel
  (auto-matched by call sign/guide number, overridable).
- **Status:** poll device status endpoints. Admin dashboard shows each tuner:
  idle/active, tuned channel, signal strength/quality (SS/SNQ/SEQ), and the
  owning Bowtie session.
- **Ingest:** HTTP MPEG-TS from device port 5004 using `auto` tuner selection
  (`http://<device>:5004/auto/v<guideNumber>`). If all tuners are busy the client
  receives a clear error listing current viewers; an admin can end a session to
  free a tuner.

## Transcoding pipeline

### Codec negotiation

Clients report capabilities when starting a stream: video codecs (`h264`, `hevc`,
`av1`), audio codecs (`aac`, `ac3`, `eac3`), max resolution, quality preference.
Server picks the cheapest working path:

- **Video:** MPEG-2 always transcodes (modern clients can't play it). H.264 is the
  universal default; HEVC when the client supports it and config allows. v1 ships
  H.264 + HEVC encoders; AV1 later.
- **Audio:** AC-3 passthrough when supported (most TV devices); otherwise AAC with
  standard 5.1→stereo downmix.
- **Deinterlacing:** always deinterlace to progressive (1080i/480i is common OTA),
  using the hardware deinterlacer when available (e.g. `vpp_qsv`, `yadif` on
  software fallback).

### Hardware encoder selection

At startup, probe FFmpeg with tiny test encodes to build a ranked working-backend
list: VideoToolbox (macOS) → QSV → NVENC → VAAPI → software x264 fallback.
AMD GPUs are supported through the VAAPI backend (the standard AMD encode path
on Linux, where Bowtie is deployed via Docker). Config can force a backend.
Probe results are visible in the admin dashboard.

### Quality ladder & session sharing

Named profiles in config; defaults: Original (1080p ≈ 8 Mbps), High (720p ≈ 4),
Medium (720p ≈ 2.5), Low (480p ≈ 1.5). Viewers choose quality; admins may cap
quality per user. A transcode session is keyed by **(channel, video codec,
quality, audio codec)** — matching requests join the existing FFmpeg process and
tuner rather than claiming new ones. One rendition per session in v1; adaptive
multi-rendition ladders deferred (GPU cost).

## HLS delivery & session lifecycle

- One supervised FFmpeg process per active session emitting **4-second MPEG-TS
  segments** (best cross-device compatibility; fMP4 later if needed) into a
  sliding window of ~30 segments — which doubles as a ~2-minute pause/rewind
  buffer.
- Stream URLs carry a short-lived signed token as a **query parameter** (TV
  players often can't attach headers to segment fetches).
- Lifecycle: `POST /api/v1/sessions` with capabilities → playlist URL. Playlist
  fetches serve as viewer heartbeat. Viewer leaves via explicit `DELETE` or
  heartbeat timeout; when a session's last viewer leaves, FFmpeg stops after a
  ~60s grace period and the tuner frees. FFmpeg crashes trigger supervised
  restart with backoff (viewers see a brief stall, not an error).
- Admin dashboard lists live sessions (viewer, channel, quality, encoder) and can
  terminate them.
- DVR-ready by construction: recording later = persisting segments instead of
  expiring them.

## EPG

- **Sources:** XMLTV file/URL with configurable refresh; Schedules Direct JSON
  API (credentials + lineup selection in admin UI). Both normalize into the same
  SQLite tables.
- **Mapping:** guide channels ↔ tuner channels auto-matched by call sign/guide
  number; admin can override.
- **Retention:** keep ~14 days forward (as the source provides), prune stale
  programs. Failed fetches retry with backoff; persistent failure shows a
  "guide data stale" admin warning.
- **API:** `GET /api/v1/guide?start=&end=` returns grid-ready data for enabled
  channels only.

## Users, auth, and API contract

- **Roles:** admin, viewer. Admins create accounts (no self-signup) and may set
  per-user max quality. Users change their own passwords.
- **Security:** Argon2id password hashing. Short-lived JWT access tokens;
  revocable long-lived refresh tokens stored hashed (long-lived deliberately —
  TV-remote password entry is hostile).
- **Contract:** the whole API lives under `/api/v1` and is documented as an
  OpenAPI 3 spec in `docs/`. That spec is the contract Phase 2/3 clients build
  against; native clients require nothing the web viewer doesn't already use.

## Web viewer

React + TypeScript + Vite; hls.js playback (native HLS on Safari). Compiled and
embedded into the Go binary. Areas:

- **Login**
- **Guide** — grid with now-playing, click to watch
- **Player** — channel switching, quality picker, stats-for-nerds overlay
  (codec, encoder, bitrate)
- **Admin** — tuners, channels, EPG config, users, live sessions

## Packaging & CI

- **Docker:** multi-arch (amd64 + arm64) via buildx in GitHub Actions, published
  to ghcr.io. amd64 bundles FFmpeg with QSV + Intel media driver; QSV requires
  `/dev/dri` passthrough — a documented docker-compose example shows it. arm64
  ships software-encoding FFmpeg (Apple Silicon dev runs native, not in Docker).
- **Native dev:** `make dev` on macOS uses Homebrew FFmpeg (VideoToolbox works
  out of the box).
- **CI:** golangci-lint, Go tests, web build, Docker build on every PR. Tagged
  releases: multi-arch images + GoReleaser binaries.

## Error handling

| Failure | Behavior |
|---|---|
| HDHomeRun unreachable | Admin alert; stream attempts fail with a clear message |
| All tuners busy | Client error lists active viewers; admin can end a session |
| FFmpeg crash | Supervised restart with backoff; brief stall, not an error |
| EPG source down | Retry with backoff; stale-guide banner in admin |
| Weak signal | SS/SNQ surfaced in tuner dashboard |

## Testing strategy

- **Fake HDHomeRun:** a test HTTP server emulating discovery, lineup, status, and
  streaming (serves a canned MPEG-2 TS fixture). Enables tuner logic, session
  sharing, and end-to-end pipeline tests in CI with no hardware.
- **Unit tests:** codec negotiation, session keying, EPG parsing (golden XMLTV
  files), auth.
- **Local-tagged tests:** real-FFmpeg pipeline tests run on dev machines (probe,
  transcode fixture, verify output codecs), skipped in CI.
- **Web:** component tests for guide/player state; Playwright smoke later.

## Out of scope for v1

DVR/recording (architecture-friendly, not built), ATSC 3.0 (HEVC/AC-4 input),
AV1 encoding, adaptive multi-rendition ABR, built-in TLS/ACME, per-user channel
lists, self-signup.
