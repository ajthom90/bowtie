# Bowtie Roku channel

BrighterScript SceneGraph channel for the Bowtie viewer:

**Connect → Login → Channel rail → Live play**, plus **Recordings** (DVR)

Single `ApiTask` owns all HTTP and tokens (auth actor). Pure logic
(`AuthState`, `GuideLogic`, `Caps`, request builders/parsers) is exercised
on-device by **SelfTestScene**.

## Requirements

- Node.js 22+
- A Roku device in [developer mode](https://developer.roku.com/docs/developer-program/getting-started/developer-setup.md) for sideload

## Build

```bash
cd roku
npm ci
npx bsc                          # transpile .bs → out/staging
npx bslint --severity error
npm run package                  # out/bowtie-roku.zip (staging contents at zip root)
```

Or from the repo root:

```bash
make roku-package
```

The zip root contains `manifest`, `source/`, `components/`, and `images/` only —
no `.bs`, `node_modules`, or config files.

GitHub Releases attach `bowtie-roku-<version>.zip` (release workflow job `roku`,
`needs: [goreleaser]`).

## Sideload

1. Enable developer mode (Home ×3, Up ×2, Right, Left, Right, Left, Right).
2. Note the device IP and set a password when prompted.
3. Open `http://<roku-ip>` in a browser, sign in, upload `out/bowtie-roku.zip`.
4. Or use `curl`:

```bash
curl -F "mysubmit=Install" -F "archive=@out/bowtie-roku.zip" \
  "http://rokudev:<password>@<roku-ip>/plugin_install"
```

Full adversarial checklist: [`docs/deploy/roku-testing.md`](../docs/deploy/roku-testing.md).

## Self-test (on-device pure-logic suite)

SelfTestScene runs AuthState, GuideLogic, and BowtieClient fixture suites
(mirroring iOS/Android) and renders `PASS n/n` or failing case names.

```bash
curl "http://<roku-ip>:8060/launch/dev?selftest=1"
```

(`supports_input_launch=1` is set in the channel manifest.)

## Sign in

Login opens on **Sign in with your phone** (quick sign-in): the Roku asks
for a code (`POST /api/v1/auth/device`, named after the Roku's friendly
name), shows the server's QR PNG (`qrUrl`, 512 px, shown 1:1) and "Or go to
`<server>/link` and enter **BCDF-2345**". A phone or browser that is signed
in approves it. Meanwhile ApiTask polls `POST /api/v1/auth/device/token` every
`interval` s (one poll at a time): 428 keeps waiting, 200 signs in exactly as a
password login does (tokens held by ApiTask, refresh token persisted), and 410
(or the code's `expiresIn` passing) shows **Code expired** with **Get a new
code** focused. Network trouble keeps polling.

| Button | Action |
|--------|--------|
| Get a new code | Drop this code and ask for another |
| Use a password instead | Username / password keyboards (the old flow); **Sign in with your phone** returns |
| Change server | Back to Connect |

A server without quick sign-in (404) drops straight to the password form with
a note.

## Channel rail controls

| Key | Action |
|-----|--------|
| `*` (Options) on a channel | Opens a dialog: **Favorite / Unfavorite**, **Record this program** (only when the guide has a program on now that isn't already scheduled), **Cancel**. |
| Up from the first channel | Recent row (when the server has watch history), then the header (Recordings / Settings) |
| Left / Right in the header | Move between **Recordings** and **Settings** |
| Down from the header / Recent | Back toward the rail |

**Favorite** stars the channel: starred channels move to the top
(guide-number order) with a ★; the change is sent to the server and undone if
it fails. Favorite is left out on a server without favorites.

**Record this program** schedules the current program by its guide start
(`POST /api/v1/recordings`; capture starts 1 min early and ends 3 min late).
A warning such as "uses all tuners" is shown with the confirmation. When more
channels than tuners would be recording at once, the server answers 409 and the
dialog lists the recordings already holding the tuners with **Record anyway**
(scheduled at a lower priority; earlier recordings keep their tuners) or
**Cancel**.

Up/down zapping in the player follows rail order, so it cycles favorites first.
The Recent row lists the last 8 channels watched for 30 s or more (any device,
same account) and is hidden when empty or on a server without favorites.

## Recordings controls

**Recordings** (in the header next to Settings) lists everyone's recordings in
three tabs: **Upcoming** (scheduled, waiting for a tuner, recording now),
**Recorded** (converting, ready) and **Missed** (failed, with the reason in
plain words, e.g. "No tuner was free").

| Key | Action |
|-----|--------|
| Left / Right on the tabs | Switch tab |
| Down / Up | Between the tabs and the list |
| OK on a recorded item | Play it. Past the first 10 s and before the last 30 s, asks **Resume from m:ss** / **Start over**. |
| OK on any other item | Same as `*` |
| `*` (Options) on an item | **Stop recording** (while recording), **Keep / Don't keep** (protect from automatic deletion), **Cancel recording** (upcoming) or **Delete** (asks first: it removes the recording for everyone), **Close**. Only the person who scheduled it or an admin (`canManage`) gets these; others see who scheduled it. |
| Back | Recordings → channel rail |

### Recording playback

Recordings play as HLS VOD in a `Video` node with Roku's standard trick-play
UI (OK pause/play, Left/Right and FF/RW to seek, the progress bar; there are no
BIF thumbnails). The position is saved (`PUT …/position`) every 15 s, when the
recording ends, and on Back, which returns to the list.

## Player controls

| Key | Action |
|-----|--------|
| OK / Play | Play / pause |
| Back | Stop session (DELETE) and return to rail |
| Up / Down | Zap previous / next channel (400 ms debounce, session-replace) |
| Right (or `*` / Options on streaming sticks) | Quality dialog (profiles filtered by `user.maxQuality`). Roku TVs open their own picture menu on `*` during playback. |
| Info / Display | Toggle debug overlay |

### Session lifecycle (A3)

On zap or quality change: bump generation → **DELETE** current viewer → debounce
400 ms → **POST** create. A create response for a **stale** generation triggers
orphan DELETE before discard. Back/stop: DELETE then leave. Mid-play Video
errors: empty auth allowlist until on-device capture; otherwise bounded retry
(1 s / 2 s / 4 s ×3) **without** new sessions.

### Debug overlay

Amber strip at the bottom (off by default; set `showDebug` to `true` in `components/PlayerScene.xml` for sideload validation) shows:

```text
Video state=… errorCode=… errorMsg=…
viewerId=… gen=… phase=…
```

Use step 7 of the validation gate to capture real `errorCode`/`errorMsg` after
admin token-kill — those values extend the mid-play auth recreate allowlist.

### Error surfaces

| Case | UI |
|------|-----|
| 503 tuners busy | Full copy + who’s-watching list + Try again |
| 422 negotiation | Reset quality to Auto, retry once; second → device-can’t-play |
| 404 | Channel not found; rail refreshes on return |
| Mid-play failure | Bounded retry, then error + Try again |

## Design tokens

| Role | Value |
|------|-------|
| Background | `#101418` |
| Focus / accent | `#F0A428` (amber) |
| Text | `#F2EFE8` |
| Dim text | `#9BA5AE` |

## Layout

```text
roku/
├── manifest
├── bsconfig.json
├── package.json
├── images/                 # icons, splash, amber focus 9-patch
├── source/
│   ├── main.bs             # entry; selftest=1 → SelfTestScene
│   ├── lib/                # AuthState, BowtieClient, Caps, DeviceAuth, Favorites, GuideLogic, Recordings, Registry
│   └── tests/              # on-device fixtures
└── components/
    ├── AppScene            # phase routing (connect/login/checking/home/settings/recordings/player)
    ├── ConnectScene
    ├── LoginScene          # Sign in with your phone (QR + code, polling) or password
    ├── HomeScene           # MarkupList rail + guide join, * dialog (favorite / record)
    ├── RecordingsScene     # Upcoming / Recorded / Missed + VOD Video (RecordingItem rows)
    ├── PlayerScene         # Video + session-replace
    ├── SettingsScene
    ├── SelfTestScene
    └── tasks/ApiTask       # sole HTTP + token holder
```
