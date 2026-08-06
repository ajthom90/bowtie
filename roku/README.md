# Bowtie Roku channel

BrighterScript SceneGraph channel for the Bowtie viewer:

**Connect → Login → Channel rail → Live play**

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

## Player controls

| Key | Action |
|-----|--------|
| OK / Play | Play / pause |
| Back | Stop session (DELETE) and return to rail |
| Up / Down | Zap previous / next channel (400 ms debounce, session-replace) |
| `*` / Options | Quality dialog (profiles filtered by `user.maxQuality`) |
| Info / Display | Toggle debug overlay |

### Session lifecycle (A3)

On zap or quality change: bump generation → **DELETE** current viewer → debounce
400 ms → **POST** create. A create response for a **stale** generation triggers
orphan DELETE before discard. Back/stop: DELETE then leave. Mid-play Video
errors: empty auth allowlist until on-device capture; otherwise bounded retry
(1 s / 2 s / 4 s ×3) **without** new sessions.

### Debug overlay

Amber strip at the bottom (on by default for sideload validation) shows:

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
│   ├── lib/                # AuthState, BowtieClient, Caps, GuideLogic, Registry
│   └── tests/              # on-device fixtures
└── components/
    ├── AppScene            # phase routing (connect/login/checking/home/settings/player)
    ├── ConnectScene / LoginScene
    ├── HomeScene           # MarkupList rail + guide join
    ├── PlayerScene         # Video + session-replace
    ├── SettingsScene
    ├── SelfTestScene
    └── tasks/ApiTask       # sole HTTP + token holder
```
