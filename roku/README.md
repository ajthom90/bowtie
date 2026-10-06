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
| `*` (Options) on a channel | Opens a dialog: **Favorite / Unfavorite**, **Record this program** (only when the guide has a program on now that isn't already scheduled), **Record series** (when the guide has a program on now), **Cancel**. |
| Up from the first channel | The filter chips, then the Recent row (when the server has watch history), then **Continue watching** (when there is something to continue), then the header (Recordings / Settings) |
| Left / Right on the filter chips | Move between **All · Sports · Movies · News · Kids · New** |
| OK on a filter chip | Show only that category's channels (the chosen chip is filled amber) |
| Down from the filter chips | The rail, or **Show all** when nothing matches |
| Up from **Show all** | The filter chips |
| OK on **Show all** | Back to **All** (focus on its chip) |
| Left / Right in the header | Move between **Recordings** and **Settings** |
| Down from the header / Continue watching / Recent | Back toward the chips and the rail (hidden rows are skipped) |
| Left / Right on Continue watching | Move along the row (up to 10 recordings) |
| OK on a Continue watching item | Resume the recording at its saved position (no Resume / Start over question); Back or the end returns here |
| `*` (Options) on a Continue watching item | **Remove from Continue watching** (sets the saved position back to 0), **Cancel** |

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

**Record series** records every new episode of the show on this channel
(`POST /api/v1/recording-rules` with the program's channel and start; the
server's defaults are this channel, new episodes only). The upcoming airings in
the next 14 days are scheduled at once and the dialog says **Scheduled N
episodes**; more are added as the guide refreshes.

**Continue watching** sits above Recent and lists recordings you started and
haven't finished, the same as the other apps: ready, not blocked by parental
controls, at least 1 minute watched and more than 2 minutes left; the most
recently watched first (`positionUpdatedAt`; recordings without one, from an
older server, follow newest-recording first); at most 10. Each card shows the
title, the episode, the time left ("37 min left", "1 hr 5 min left") and a
progress bar. The row comes from `GET /api/v1/recordings?state=recorded`, is
hidden when empty (or on a server without recordings), and reloads whenever
the channel rail does, including on the way back from playback. Logic:
`source/lib/ContinueWatching.bs` (also the row layout).

**Filter chips** (All · Sports · Movies · News · Kids · New) sit directly
above the rail. Under a category the rail keeps only the channels whose
program on now or next (in the guide the rail already loads) is in it; when
none are, "No sports on right now" (movies, news, kids' shows, new episodes)
and **Show all** take the rail's place. The rules are the other apps'
(`web/src/guide/guideFilterModel.ts`): the category string split on `,` `;`
`|` and matched case-insensitively on whole words, Movies only when a whole
piece is a movie word ("Movie review" is not), Schedules Direct program IDs
`MV`/`SP` + 8 digits as movies / sports events, mature ratings (TV-14, TV-MA,
R, NC-17, X) kept out of Kids, and New for new episodes. The chip is kept on
this device (registry section `bowtie`, key `guideFilter`) and survives
sign-out and change server. Logic: `source/lib/GuideFilter.bs`.

Up/down zapping in the player follows rail order, so it cycles favorites
first; it goes through every channel, whatever the filter.
The Recent row lists the last 8 channels watched for 30 s or more (any device,
same account) and is hidden when empty or on a server without favorites.

## Recordings controls

**Recordings** (in the header next to Settings) lists everyone's recordings in
three tabs: **Upcoming** (scheduled, waiting for a tuner, recording now),
**Recorded** (converting, ready) and **Missed** (failed, with the reason in
plain words, e.g. "No tuner was free"), plus **Shows**, the series being
recorded (`GET /api/v1/recording-rules`). Recordings a series scheduled
(`ruleId` > 0) say **Series**; an episode skipped by deleting it ahead of time
says **Skipped** (not tinted red like a real miss). A recording parental
controls block for this account (`locked`) says **Locked** and OK explains
instead of playing.

| Key | Action |
|-----|--------|
| Left / Right on the tabs | Switch tab |
| Down / Up | Between the tabs and the list |
| OK on a recorded item | Play it. Past the first 10 s and before the last 30 s, asks **Resume from m:ss** / **Start over**. |
| OK on any other item | Same as `*` |
| `*` (Options) on an item | **Stop recording** (while recording), **Keep / Don't keep** (protect from automatic deletion), **Cancel recording** (upcoming) or **Delete** (asks first: it removes the recording for everyone), **Close**. Only the person who scheduled it or an admin (`canManage`) gets these; others see who scheduled it. |
| `*` or OK on a show (Shows tab) | **Stop recording this show** (`DELETE /recording-rules/{id}`: cancels its upcoming recordings, recorded ones stay), **Close**. Only whoever set it up or an admin (`canManage`). |
| Back | Recordings → channel rail |

### Recording playback

Recordings play as HLS VOD in a `Video` node with Roku's standard trick-play
UI (OK pause/play, Left/Right and FF/RW to seek, the progress bar; there are no
BIF thumbnails). The position is saved (`PUT …/position`) every 15 s, when the
recording ends, and on Back, which returns to the list.

| Key | Action |
|-----|--------|
| OK / Play | Play / pause (the Video's trick-play UI) |
| Left / Right, FF / RW | Seek |
| OK on **Skip ad ▸ (OK)** | Jump to the end of the ad |
| Left / Right / FF / RW / Replay on **Skip ad** | Put Skip ad away for this ad; the next press seeks |
| Play on **Skip ad** | Play / pause |
| Down | **Sleep timer** menu |
| OK on **Still watching?** | Keep watching (same duration again) |
| Back | Save the position and return to the list (to the channel rail when started from Continue watching) |

**Skip ad.** A recording may carry `commercials: [{start, end}]` (seconds on
its timeline; missing or empty means none). While the position is inside one
(start inclusive, end exclusive) a **Skip ad ▸ (OK)** button appears bottom
right and takes focus, so OK reaches it rather than the Video. With
**Settings → Skip ads automatically** on, each ad is skipped once per playback
and **Skipped ad** shows briefly; seeking back into a skipped ad shows the
button instead of skipping again. Logic: `source/lib/Commercials.bs`.

**Sleep timer** (Down): Off, 15 / 30 / 45 / 60 / 90 minutes, 2 hours, with the
time left in the menu. A minute before it runs out, **Still watching? Sleeping
in 1:00 — OK to keep watching** takes focus; OK adds the same duration again.
When it runs out, playback stops as Back does. Reset when playback ends; never
saved. "Down: sleep timer" shows for a few seconds when playback starts.

## Player controls

| Key | Action |
|-----|--------|
| OK / Play | Play / pause |
| Back | Stop session (DELETE) and return to rail |
| Up / Down | Zap previous / next channel (400 ms debounce, session-replace) |
| Right | **Options** dialog: quality (profiles filtered by `user.maxQuality`) and **Sleep timer**. `*` is left to the system menu (audio tracks, closed captioning; Roku TVs add picture settings). |
| OK while **Still watching?** shows | Keep watching (instead of pausing) |
| Info / Display | Toggle debug overlay |

### Sleep timer

Right → **Sleep timer**: Off, 15 / 30 / 45 / 60 / 90 minutes, 2 hours, and
**End of this program** when the guide (from the channel rail) knows when the
program on now ends; the menu shows the time left. A minute before it runs
out, **Still watching? Sleeping in 1:00 — OK to keep watching** appears; OK adds
the same duration again (30 minutes for End of this program). When it runs
out the player leaves exactly as Back does: the viewer is DELETEd (tuner
freed) and the rail comes back. It survives zapping, resets when the player is
left, and is never saved. Logic: `source/lib/SleepTimer.bs` (clock injected,
tested under brs).

### Signal and busy tuners

The 15 s heartbeat asks for the antenna's reception (`?signal=1`). The top
bar shows the latest reading — **Signal quality 46% · strength 96% ·
error-free 0%** (quality first: strength can read high while the picture
breaks up) — and so does the debug overlay. When the server says the signal
is weak, **Weak signal (46%) — the picture may break up.** stays under the
top bar (read out by Audio Guide when it appears). An older server (empty
204) or no reading shows neither.

Channels the server marks `watchable: false` (every tuner they need is busy)
leave the rail, the Recent row and Up/Down zapping, with **All tuners are in
use — showing channels you can join.** right of the filter chips; with none
left, **All tuners are in use. Try again in a few minutes.** and **Try again**
take the rail's place. The rail re-checks every 30 s while shown. Logic:
`source/lib/Tuners.bs` (tested under brs).

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
Signal quality …% · strength …% · error-free …%   (when the server reports one)
```

Use step 7 of the validation gate to capture real `errorCode`/`errorMsg` after
admin token-kill — those values extend the mid-play auth recreate allowlist.

### Error surfaces

| Case | UI |
|------|-----|
| 503 tuners busy | Full copy + who’s-watching list + Try again |
| 422 negotiation | Reset quality to Auto, retry once; second → device-can’t-play |
| 404 | Channel not found; rail refreshes on return |
| 403 `code: parental` (session start, or a live viewer's heartbeat once the server stops it) | The server's message ("Blocked by parental controls (rated TV-MA)") + pick another channel; no retry loop |
| Mid-play failure | Bounded retry, then **The stream stopped. Try again.** + Try again |
| No answer (offline, timed out) | **Can't reach your Bowtie server. Check your connection and try again.** |
| Anything else | The server's own message, else plain words; `errorCode` / raw bodies go to the debug log only |

## Settings

| Key | Action |
|-----|--------|
| Up / Down | Move between **Back**, **Change server**, **Change password**, **Sign out**, **Skip ads automatically** |
| OK | Press the focused button (**Skip ads automatically** toggles On / Off) |
| Back | Return to the channel rail |

**Skip ads automatically** (default Off) is kept on this device (registry
section `bowtie`, key `autoSkipAds`) and survives sign-out and change server.

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
│   ├── lib/                # AuthState, BowtieClient, Caps, Commercials, ContinueWatching, DeviceAuth, Favorites, GuideFilter, GuideLogic, Recordings, Registry, SleepTimer
│   └── tests/              # on-device fixtures
└── components/
    ├── AppScene            # phase routing (connect/login/checking/home/settings/recordings/player)
    ├── ConnectScene
    ├── LoginScene          # Sign in with your phone (QR + code, polling) or password
    ├── HomeScene           # Continue watching (RowList of ContinueItem) + Recent + filter chips (FilterChip) + MarkupList rail + guide join, * dialogs
    ├── RecordingsScene     # Upcoming / Recorded / Missed / Shows + VOD Video (RecordingItem rows); resume mode for Continue watching
    ├── PlayerScene         # Video + session-replace
    ├── SettingsScene
    ├── SelfTestScene
    └── tasks/ApiTask       # sole HTTP + token holder
```
