# Running Bowtie on TrueNAS SCALE

Tested against TrueNAS SCALE 24.10+ (Electric Eel and later — the versions with
the native Docker backend). Everything lives in ONE compose file: no `.env`
files, no external volumes to pre-create beyond a dataset.

## 1. Create a dataset for Bowtie's state

Storage → your pool → Add Dataset → name it e.g. `apps/bowtie`
(→ `/mnt/<pool>/apps/bowtie`). Bowtie's container runs as root and creates
what it needs; no special ACLs required.

## 2. Install the app from YAML

Apps → Discover Apps → ⋮ (top-right) → **Install via YAML** → name it
`bowtie`, paste the compose below, adjust the two `CHANGE-ME` values:

```yaml
services:
  bowtie:
    image: ghcr.io/ajthom90/bowtie:0.4.0
    container_name: bowtie
    restart: unless-stopped
    ports:
      - "8400:8400"
    environment:
      BOWTIE_DATA_DIR: /data
      # Bridge networking cannot receive HDHomeRun's UDP discovery broadcasts,
      # so list the tuner IP(s) here (comma-separated) — CHANGE-ME:
      # First-boot seed for the device table (Admin → Tuners can also add IPs).
      BOWTIE_DEVICES: "192.168.1.50"
      # Product settings (EPG sources, encoder, HEVC) live in Admin → Settings.
      # BOWTIE_ENCODER / yaml EPG+transcode keys are first-boot seeds only —
      # after the DB has those keys, UI changes win and survive restarts.
    volumes:
      # Persistent state (SQLite, config, secrets) — CHANGE-ME to your dataset:
      - /mnt/tank/apps/bowtie:/data
    tmpfs:
      # High-churn HLS segments stay in RAM: no SSD wear, auto-clean on restart.
      # ~4 MB/segment at the top profile × 30-segment window ≈ 128 MB/session;
      # 2g comfortably covers several concurrent sessions.
      - /data/segments:size=2g
    devices:
      # Intel Quick Sync / VAAPI hardware transcoding.
      - /dev/dri:/dev/dri
```

Click Save — TrueNAS pulls the image and starts the container.

## 3. Get the first-run admin password

It is printed ONCE to the container log:

Apps → bowtie → Workloads → the container's ⋮ → Logs — look for
`first run: created admin user "admin" with password "…"`.
(Shell alternative: `sudo docker logs bowtie 2>&1 | grep password`.)

## 4. First-run setup

1. Open `http://<truenas-ip>:8400`, sign in as `admin`, change the password
   (Profile / password API).
2. Admin → Tuners: your HDHomeRun should already be listed (from
   `BOWTIE_DEVICES`); if not, add it by IP. Sync lineups.
3. Admin → Channels: enable the channels your family should see. You can watch
   immediately — guide data is optional. Admins can ▶ Preview a disabled
   channel before enabling it.
4. Admin → Settings: configure XMLTV and/or Schedules Direct (with lineup
   picker), encoder, and HEVC. Changes apply without restart. Admin → EPG
   shows status + Refresh only.
5. Admin → Transcode (status page): confirm the probe lists `qsv` and/or
   `vaapi` with an FFmpeg version — that's hardware transcoding live. If only
   `software` appears, see the GPU note below. Prefer **Admin → Settings →
   Transcode** to pick encoder / Allow HEVC.
6. Guide → pick a channel → watch. The player's stats overlay (and Admin →
   Sessions) shows which encoder backend the session negotiated.

## GPU notes (Intel iGPU)

- `ls /dev/dri` in the TrueNAS shell should show `card0` + `renderD128`. If it
  does not: System → Advanced Settings → check that the iGPU is not isolated
  for VM passthrough (Isolated GPU Devices must NOT include it).
- Inside the container, `qsv` requires the iHD driver (bundled in the image on
  amd64); `vaapi` is the fallback and also hardware-accelerated on Intel.

## Networking notes

- Bridge mode (the YAML above) is the TrueNAS-friendly default: only port 8400
  is published, and tuners are configured by IP. UDP auto-discovery does not
  work across the bridge — that is expected and fine.
- If you prefer discovery, replace `ports:` with `network_mode: host` — but
  bridge + explicit IP is the recommended, least-surprising setup.
- Remote access for the family stays BYO reverse proxy / tunnel
  (see `docs/deploy/remote-access.md`) pointed at `<truenas-ip>:8400`.

## Upgrading

Edit the app's YAML, bump the image tag (e.g. `:0.4.0`), save. State lives in
your dataset; segments are disposable.

**v0.4.0 note:** On first boot after upgrade, EPG/transcode keys are
presence-seeded from env/yaml into the DB (defaults: encoder `auto`,
refreshHours `12`, allowHevc `false`). After that, **Admin → Settings** is the
control plane — changing `BOWTIE_ENCODER` or yaml EPG/transcode keys will not
override values already stored.
