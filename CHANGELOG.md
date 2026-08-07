# Changelog

All notable changes to Bowtie are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project aims to follow [Semantic Versioning](https://semver.org/).

## [0.4.0] — 2026-08-07

### Added

- **EPG-less watching** — channels are watchable with zero guide configured.
  Empty guide state splits by role (admin vs viewer copy). Program-less cells
  say "No guide data — press to watch". Admins can **▶ Preview** disabled
  channels from Admin → Channels (viewers still get 404 for disabled).
- **Admin → Settings** control plane (DB-backed, restart-free):
  - XMLTV source + refresh interval
  - Schedules Direct username/password + lineup picker (`Load lineups`)
  - Encoder selection (from probe `available` + `auto`) and Allow HEVC
  - Per-section Save with "Saved." feedback; EPG tab keeps status + Refresh only
- **Mobile-friendly web** — 640px breakpoint: admin tables card-collapse,
  scrollable nav pills, touch targets ≥44px, guide/player polish, player
  quality bottom sheet on narrow viewports.
- Settings API: `GET`/`PUT /api/v1/admin/settings`, `GET /api/v1/admin/epg/lineups`.

### Changed

- EPG supervisor always runs and re-reads settings each cycle (enable/disable
  sources and change intervals without restart).
- Stream manager and `/admin/transcode` `selected` read encoder/HEVC from the
  same settings provider per session.
- iOS/tvOS empty-now copy: "Nothing on now" → "No guide data".
- Deploy docs (README, Compose comments, TrueNAS) point product settings at
  Admin → Settings.

### Breaking-ish (ops)

**`BOWTIE_ENCODER` and yaml EPG/transcode keys are first-boot seeds, not live
overrides.** On first start after upgrade, absent DB keys are presence-seeded
from env/`config.yaml` (defaults: encoder `auto`, refreshHours `12`,
allowHevc `false`). After a key exists in the DB — including an intentional
empty string (e.g. XMLTV disabled) — Admin → Settings is the sole source of
truth; changing env/yaml for those product keys no longer overrides stored
values. Infra keys still apply every start: listen address, data dir, segment
dir, FFmpeg path, `devices` / `BOWTIE_DEVICES`.

Existing deployments: first boot after upgrade seeds from current config —
zero behavior change until you edit Admin → Settings.
