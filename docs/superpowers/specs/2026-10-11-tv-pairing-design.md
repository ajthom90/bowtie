# TV pairing (set up a TV from your phone) — Design

Date: 2026-10-11. Requested: "a nice and simple TV pairing system … users
could scan a QR code, type in their server URL on their phone … which would
then authenticate the TV. All without having to type the server URL on the
TV using the remote." The user ruled out anything that points at a fixed host
(Bowtie is self-hosted by many people) and picked the no-cloud "local
hand-off" approach for all TV apps (2026-10-05); approved to build
2026-10-11. Apple TV, Android TV/Fire TV and Roku now; Xbox when that app
moves again.

## What exists

- Every TV app starts on a Connect screen that asks for the server address
  (typed with the remote), checks `GET <server>/healthz`, saves it, and moves
  to Login.
- Login already offers phone sign-in: the TV calls `POST /api/v1/auth/device`,
  shows the server-rendered QR for `<server>/link?code=…`, and polls
  `/api/v1/auth/device/token` until the phone approves. Tokens are stored by
  the client on success.
- The only painful step left is typing the server address on the TV.

## Goal

On the Connect screen the TV also shows a QR code. Scanning it opens a small
page **served by the TV itself** where the phone types the server address.
The TV connects to that server and sends the phone straight on to the
server's `/link?code=…` page, so one scan and one sign-in on the phone sets
up the TV. No server changes, no cloud, no fixed host.

## The flow

1. TV on Connect: starts a pairing listener on a random free TCP port on its
   LAN IPv4 address and shows a QR for
   `http://<tv-ip>:<port>/p?t=<token>`, with the same address as text
   underneath ("Or open … on your phone"). The typed-address field stays.
2. Phone opens the page: a form with one field, "Your Bowtie server address"
   (placeholder `http://192.168.1.50:8400`), and a Continue button.
3. Phone posts the address. The TV runs its normal connect (normalize →
   `/healthz` → save → Login). The phone gets a "Connecting to your TV…" page
   that re-checks `/p/status?t=<token>` every second.
4. Login starts phone sign-in as it does today. When it has a code, the
   status check answers `303` to `<entered server>/link?code=<code>` — built
   from the address the phone typed (which the phone can reach), not from the
   server's `verifyUrl`.
5. The phone signs in (or is already signed in) and approves; the TV's
   existing polling signs it in. The listener stops.

If the server can't be reached, the status page shows "Couldn't reach a
Bowtie server at <address>." with the form again; the TV stays on Connect.

## Pairing listener (same shape on every platform)

**Pure core (unit-tested):** `PairingSession` holds the token and a state:
`waitingForAddress → connecting(address) → failed(message) | ready(linkURL)`.
`handle(request) → response (+ optional "submit address" action)`:

| Request | Response |
|---|---|
| `GET /p?t=<ok>` | 200 form page |
| `POST /p` (form `t`, `server`) with ok token | action submit(server); 303 → `/p/status?t=…` |
| `GET /p/status?t=<ok>` | connecting → 200 waiting page (`<meta refresh=1>`); failed → 200 form with the error; ready → 303 `linkURL` |
| `/p` or `/p/status` with a missing or wrong `t` | 403 "This pairing code has expired. Scan the code on your TV again." |
| anything else (e.g. `/favicon.ico`) | 404 |

- Token: 24 lowercase hex chars (96 bits) from the platform's secure random
  (Roku: `roDeviceInfo.GetRandomUUID()`). Compared in constant time where the
  platform allows. After 20 requests to `/p` or `/p/status` carrying a
  present-but-wrong `t` the session closes (missing tokens, favicon and other
  404s never count) (the TV
  shows a fresh code on next visit to Connect).
- Requests are parsed minimally: request line, headers up to a blank line,
  `Content-Length` body. Limits: 8 KB per request, 5 s read timeout, up to
  4 connections served at once (more are closed). Responses always send
  `Connection: close`.
- Pages are tiny static HTML (inline CSS, no JS beyond meta refresh), all
  user-supplied text HTML-escaped. `server` must be http/https after the
  platform's existing `normalize`; anything else is a form error.
- Lifetime: starts when Connect is shown; stops when the TV reaches the
  signed-in state, after 15 minutes, or when the user leaves pairing by
  typing an address themselves (the typed connect wins). It keeps running
  through Login so the status redirect can be served.
- No LAN IPv4 (e.g. Ethernet-less/odd networks) or the bind fails: no QR, the
  typed field works as today.

## Platforms

- **Apple TV:** listener with Network.framework `NWListener` in BowtieKit
  (`PairingListener` + pure `PairingSession`, tested with `swift test`); LAN
  IP via `getifaddrs` (first up, non-loopback IPv4 on `en*`); QR drawn with
  CoreImage `CIQRCodeGenerator`. `AppModel` owns the coordinator; it calls the
  existing `connect(rawURL:)` and gets the code through `DeviceSignInModel`'s
  `onWaiting`. Add `NSLocalNetworkUsageDescription` to the tvOS plist. Only
  the tvOS app starts it (iOS/macOS unchanged).
- **Android TV / Fire TV:** pure `PairingSession` in `:core` (JUnit), a
  `ServerSocket` listener on `Dispatchers.IO` in `:core` with a socket test,
  LAN IP via `NetworkInterface` (site-local `Inet4Address`). The QR is drawn
  in `:tv` only, with `com.google.zxing:core` (new `:tv` dependency,
  Apache-2.0, pure Java) as an `ImageBitmap`; the phone APK doesn't grow. Wired in `AppViewModel`/`ConnectScreen`; the sign-in code
  comes from `DeviceSignInViewModel`'s `Waiting` state. Phone app unchanged.
- **Roku:** a new `PairTask` node runs the `roStreamSocket` listener in its
  own thread (the `ApiTask` loop must not block). It exposes `submitted`
  (address) and takes `linkUrl`/`error`/`stop` from the scene. Pure pieces in
  `source/lib`: `PairHttp.bs` (parse/respond, tested under brs) and a
  BrightScript QR encoder `Qr.bs` (byte mode, ECC M, versions 1–6, no
  version-info block needed; byte capacity at M is 14/26/42/62/84/106, and the
  pairing URL is at most 57 bytes, so v4 in practice; the mask is
  chosen by the standard penalty rules) drawn as one `Rectangle` per run
  of dark modules. Tests decode the encoder's matrix with `jsqr` (dev
  dependency) to prove it scans. LAN IP from `roDeviceInfo.GetIPAddrs()`.
  `ConnectScene` stops auto-opening the keyboard: it shows the QR with
  "Type the address instead" as the focused button.

## Not in scope

- Server or web changes (the `/link` page already handles sign-in and
  approval).
- Finding the server automatically on the network (mDNS/SSDP).
- Phone apps doing the pairing natively (the phone uses its browser).
- HTTPS on the TV's page (LAN only; it carries no credentials — the phone
  signs in on the server's own page).

## Testing

- Unit: `PairingSession` request/response table on all three platforms;
  token rejection and lockout (favicon/404s don't count); HTML escaping; request parsing limits; Roku QR
  encoder decodes back (jsqr) to the input for several URL lengths up to the
  longest, `http://255.255.255.255:65535/p?t=` + 24 hex.
- Socket: real listener on localhost in `swift test` and JVM tests (not
  available on Roku's brs; covered on device).
- Device: bedroom Roku (IP confirmed with the user first) end to end with a
  phone on the same Wi-Fi. Apple TV and Android TV builds are checked by
  build + unit tests; the simulator can reach the listener from the Mac's
  browser for a manual run.
