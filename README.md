# Bowtie

[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)
[![CI](https://github.com/ajthom90/bowtie/actions/workflows/ci.yml/badge.svg)](https://github.com/ajthom90/bowtie/actions/workflows/ci.yml)

**Open-source HDHomeRun live TV streaming with hardware transcoding** — share your antenna with your family.

Bowtie is a single Go binary (with an embedded React web viewer) that:

- Discovers Silicondust HDHomeRun tuners on your LAN
- Transcodes over-the-air channels to HLS with hardware acceleration when available
- Serves a TV guide (XMLTV and/or Schedules Direct)
- Lets an admin manage users, devices, channels, and active sessions

**Project status:** v0.1.0 series — Phase 1 (server + web). Client apps (iOS/tvOS, Android, Roku, Fire TV) are planned for later phases.

---

## Quickstart

### Docker Compose (recommended)

```bash
# From a checkout, or drop deploy/docker-compose.yml into a folder and:
cd deploy
docker compose up -d
docker compose logs -f
```

On first start, Bowtie creates an `admin` user and **prints the password once** in the logs:

```text
created admin user "admin" with password "…" — change it after login
```

Then:

1. Open **http://localhost:8400** (or `http://<host>:8400` on your LAN).
2. Log in as `admin` and **change the password** (Profile / password API).
3. **Admin → Tuners** — add your HDHomeRun by IP if it is not discovered automatically.
4. **Admin → Channels** — enable the channels you want to stream.
5. Open the **guide**, pick a channel, and watch.

Persistent data lives in the `bowtie-data` Docker volume (`/data` in the container). HLS segments use a tmpfs mount so they do not wear the volume.

Image: `ghcr.io/ajthom90/bowtie:latest` (published by the release workflow).

To build locally:

```bash
docker build -f deploy/Dockerfile -t bowtie:dev .
docker run --rm -p 8400:8400 -v bowtie-data:/data bowtie:dev
```

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

## Hardware transcode support

Bowtie shells out to FFmpeg (never links it). Encoder selection defaults to `auto`.

| Backend        | Platform                         | Status            | Notes                                      |
|----------------|----------------------------------|-------------------|--------------------------------------------|
| **VideoToolbox** | macOS (Apple Silicon / Intel)  | Dev / first-class | Preferred on Mac for local development     |
| **QSV**        | Intel CPUs (Linux, `/dev/dri`)   | Production        | Image includes `intel-media-va-driver-non-free` (amd64) |
| **VAAPI**      | Intel / AMD GPUs (Linux)         | Production        | `mesa-va-drivers` in the runtime image     |
| **NVENC**      | NVIDIA GPUs                      | Community-tested  | Needs host NVIDIA runtime / drivers        |
| **software**   | Anywhere                         | Fallback          | `libx264` when no hardware encoder works   |

Override with `BOWTIE_ENCODER` or `encoder:` in `<dataDir>/config.yaml`
(`auto` \| `videotoolbox` \| `qsv` \| `vaapi` \| `nvenc` \| `software`).

The Docker Compose file passes `/dev/dri` into the container for QSV/VAAPI on Intel NUCs and similar hosts.

---

## EPG setup

Guide data is optional but recommended. Configure via `<dataDir>/config.yaml`
(inside Docker: `/data/config.yaml` on the volume).

### XMLTV

```yaml
xmltv:
  source: "https://example.com/guide.xml"   # or a local file path
  refreshHours: 12
```

### Schedules Direct

```yaml
schedulesDirect:
  username: "your-sd-username"
  password: "your-sd-password"
  lineupId: "USA-OTA-90210"                 # your SD lineup ID
```

After config is in place, restart Bowtie (or use **Admin → EPG → Refresh**). Map each enabled channel to an EPG channel ID under **Admin → Channels**.

---

## Remote access

Bowtie itself speaks plain HTTP on port **8400**. For HTTPS and off-LAN access, see:

**[docs/deploy/remote-access.md](docs/deploy/remote-access.md)**

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
| Env (override file) | `BOWTIE_LISTEN_ADDR`, `BOWTIE_FFMPEG_PATH`, `BOWTIE_ENCODER`, `BOWTIE_SEGMENT_DIR`, `BOWTIE_DEVICES` |
| File | `<dataDir>/config.yaml` |

Default listen address: `:8400`. Health check: `GET /healthz` → `ok`.

---

## Development

```bash
cd server && CGO_ENABLED=0 go test ./...   # no real HDHomeRun / FFmpeg required
cd web && npm ci && npm test && npm run build
```

API surface: [docs/api/openapi.yaml](docs/api/openapi.yaml).

---

## License

Licensed under the [Apache License, Version 2.0](LICENSE).
