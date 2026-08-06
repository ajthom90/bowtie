# Bowtie Roku channel

BrighterScript SceneGraph channel for the Bowtie viewer (connect → login → rail → play).

**Status:** scaffold (toolchain, packaging, hello scene). Full flow lands in later tasks.

## Requirements

- Node.js 22+
- A Roku device (or emulator) in [developer mode](https://developer.roku.com/docs/developer-program/getting-started/developer-setup.md) for sideload

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

The zip root contains `manifest`, `source/`, `components/`, and `images/` only — no `.bs`, `node_modules`, or config files.

## Sideload (dev package)

1. Enable developer mode on the Roku (home ×3, up ×2, right, left, right, left, right).
2. Note the device IP and set a password when prompted.
3. Open `http://<roku-ip>` in a browser, sign in with the dev password, upload `out/bowtie-roku.zip`.
4. Or use `curl`:

```bash
curl -F "mysubmit=Install" -F "archive=@out/bowtie-roku.zip" \
  "http://rokudev:<password>@<roku-ip>/plugin_install"
```

## Self-test (on-device pure-logic suite)

SelfTestScene runs AuthState, GuideLogic, and BowtieClient fixture suites
(mirroring iOS/Android test cases) and renders `PASS n/n` or failing case names.

After sideloading the package, launch with ExternalControl:

```bash
curl "http://<roku-ip>:8060/launch/dev?selftest=1"
```

(`supports_input_launch=1` is set in the channel manifest so query args reach `Main`.)

## Design tokens

| Role | Value |
|------|-------|
| Background | `#101418` |
| Focus / accent | `#F0A428` (amber) |
| Text | `#F2EFE8` |
| Dim text | `#9BA5AE` |
