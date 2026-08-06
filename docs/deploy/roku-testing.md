# Roku (and Fire TV) validation

Adversarial on-device gate before family rollout. This is not a smoke pass —
every step must match the expected behavior. Capture unexpected Video error
codes/messages from the debug overlay; they feed the mid-play auth-error allowlist.

Release asset: `bowtie-roku-<version>.zip` from the GitHub Release (or a local
`make roku-package` / `roku/out/bowtie-roku.zip`).

---

## Roku — Developer Mode + sideload

1. Enable **Developer Mode** on the Roku remote: Home ×3, Up ×2, Right, Left, Right, Left, Right.
2. Note the device IP and set a developer password when prompted.
3. Sideload the zip:
   - Browser: open `http://<roku-ip>`, sign in with the dev password, upload `bowtie-roku-<version>.zip`.
   - Or curl:

```bash
curl -F "mysubmit=Install" -F "archive=@bowtie-roku-<version>.zip" \
  "http://rokudev:<password>@<roku-ip>/plugin_install"
```

4. Optional pure-logic launch (step 1 below):

```bash
curl "http://<roku-ip>:8060/launch/dev?selftest=1"
```

---

## Roku — 10-step adversarial gate

Each step lists **do**, **expect**, and **report if not**.

### 1. SelfTestScene

| | |
|---|---|
| **Do** | After sideload, launch with ExternalControl: `curl "http://<roku-ip>:8060/launch/dev?selftest=1"` |
| **Expect** | Screen shows `PASS n/n` (all AuthState, GuideLogic, BowtieClient fixtures green). |
| **Report** | Any failing case names shown on screen. |

### 2. Connect → login → rail

| | |
|---|---|
| **Do** | Launch the channel normally. Enter a LAN Bowtie URL (`http://<server-ip>:8400`), validate, sign in, land on the channel rail. |
| **Expect** | Connect accepts the URL; login succeeds; rail shows channel numbers/names with now-title + progress and next (dim) when guide data is present. |
| **Report** | Exact error copy if connect/login fails; empty rail when channels are enabled on the server. |

### 3. Play + HEVC negotiation

| | |
|---|---|
| **Do** | Select a channel. Confirm video plays. On the server **Admin → Sessions**, inspect the negotiated session. |
| **Expect** | Playback starts (buffering then live). On a 4K/HEVC-capable Roku, admin shows HEVC (or the device’s best supported codec) in the session row — not forced software-only. |
| **Report** | Codec/profile shown in admin; any “device can't play” copy on first play. |

### 4. Zap storm

| | |
|---|---|
| **Do** | While playing, press Up/Down rapidly ~10 times. Wait ~5s for settle. Check **Admin → Sessions**. |
| **Expect** | Final channel plays. Admin shows **one** live viewer for this device — no stranded sessions from intermediate zaps. |
| **Report** | Extra viewer rows or stuck sessions after the storm (A3 session-replace failure). |

### 5. Quality change

| | |
|---|---|
| **Do** | Press `*` / Options to open the quality dialog. Switch profile (e.g. Auto → medium → Auto). |
| **Expect** | Dialog lists only profiles allowed by `user.maxQuality`. Playback restarts on the new profile without error; chrome shows the selected quality. |
| **Report** | Profiles above maxQuality appearing; 422 loops; black screen after change. |

### 6. Auth race (access expiry)

| | |
|---|---|
| **Do** | Start playback, leave the channel idle past the **15-minute access-token expiry**, then zap Up/Down immediately. |
| **Expect** | Single-flight refresh inside ApiTask; zap succeeds; **no** forced sign-out to Login. |
| **Report** | Kick to login, “session expired”, or hung “Starting…”. |

### 7. Token kill (capture Video error codes)

| | |
|---|---|
| **Do** | While playing, from **Admin → Sessions** terminate this device’s session. Watch the **debug overlay** (bottom amber strip: `Video state=… errorCode=… errorMsg=…`). |
| **Expect** | Overlay shows non-empty `errorCode` and/or `errorMsg` (verbatim). App either recovers via bounded retry (no new session) or surfaces error UI — **do not** expect auth-allowlist recreate yet (allowlist is seeded empty until this capture). |
| **Report** | **Write down** `errorCode` and `errorMsg` exactly. These extend `authErrorPatterns()` for silent session re-create on future builds. Also note whether a retry recovered. |

### 8. Tuner-busy (503)

| | |
|---|---|
| **Do** | Occupy all HDHomeRun tuners (other devices or admin sessions). From this Roku, try to play a channel that needs a free tuner. |
| **Expect** | Full-screen copy: **“All tuners are in use — someone else is watching. Try again in a few minutes.”** plus a who’s-watching list from `sessions[]`, and a **Try again** button. |
| **Report** | Missing list, wrong copy, or hard crash instead of the busy UI. |

### 9. HTTPS end-to-end

| | |
|---|---|
| **Do** | Settings → Change server → reconnect using the **public https** Bowtie URL. Sign in, play a channel. |
| **Expect** | TLS works (ApiTask uses `ca-bundle.crt`). Full flow: connect → login → rail → play over https. |
| **Report** | Certificate / healthz failures; play works on LAN http but not https. |

### 10. Sign out + relaunch

| | |
|---|---|
| **Do** | Settings → Sign out. Force-close or relaunch the channel. |
| **Expect** | Lands on **Login** with the server URL remembered (not Connect). No active session left in admin for this device after leave/sign-out. |
| **Report** | Boots straight to Home without credentials; server forgotten; orphaned admin session. |

---

## Pass criteria

All ten steps match **Expect**. Step 7’s captured `errorCode`/`errorMsg` values should be filed back for an allowlist follow-up even if the rest of the gate passes.

---

## Fire TV

> Placeholder: if a Fire TV sideload section is added by the Fire TV track, **preserve it here**. Roku owns this document’s Roku gate; Fire TV owns only its section.

### Sideload (when TV APK is published)

1. Enable **Apps from Unknown Sources** / ADB debugging on the Fire TV stick.
2. Install the release APK from GitHub Releases (`bowtie-tv-<version>.apk` when available):

```bash
adb connect <fire-tv-ip>
adb install -r bowtie-tv-<version>.apk
```

3. Optional smoke: launch Bowtie, connect to the LAN server, sign in, play one channel, Back to stop (session should disappear from Admin → Sessions).

Fire-OS-specific quirks (codec, AC3 passthrough, focus) are best validated on a real stick; emulator coverage is Compose/Media3 only.
