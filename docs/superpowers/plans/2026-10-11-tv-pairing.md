# TV Pairing Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A TV on its Connect screen shows a QR code for a tiny page it serves itself; the phone types the server address there, the TV connects, and the phone is sent on to `<server>/link?code=…` to approve the TV.

**Architecture:** Each TV app gets the same three layers: a pure `PairingSession` (token, state, request → response table), a socket listener (NWListener / ServerSocket / roStreamSocket) that feeds raw HTTP requests to it, and wiring into the existing Connect → Login → phone-sign-in flow. The QR is drawn on the TV (CoreImage on tvOS, zxing on Android TV, a hand-written BrightScript encoder on Roku). No server or web changes.

**Tech Stack:** Swift/SwiftUI + Network.framework (BowtieKit, `swift test`); Kotlin/Compose + OkHttp + zxing core (`./gradlew`); BrighterScript/SceneGraph (`npm test` under brs, `jsqr` for decode tests).

**Spec:** `docs/superpowers/specs/2026-10-11-tv-pairing-design.md`

## Global Constraints

- No server, web or phone-app changes. No fixed hosts anywhere: the phone is only ever sent to the address it typed.
- Routes (exact): `GET /p?t=<token>`, `POST /p` (form `t`, `server`), `GET /p/status?t=<token>`. Everything else → 404.
- Token: 24 lowercase hex chars (96 bits) from a secure random source (Roku: `roDeviceInfo.GetRandomUUID()` with dashes removed, first 24 chars).
- Lockout: after **20** requests to `/p` or `/p/status` carrying a present-but-wrong `t`, the session closes (every later request → 403). Missing `t`, favicon and other 404s never count.
- Request limits: 8192 bytes total per request, 5 s read timeout, one connection at a time, every response has `Connection: close` and `Cache-Control: no-store`.
- Listener lifetime: starts when Connect is shown; stops when the TV is signed in, after 15 minutes, or when the user connects by typing on the TV.
- Link redirect target: `<normalized address the phone typed>/link?code=<userCode without dashes>`.
- Copy (verbatim, all platforms):
  - Page title / heading: `Set up Bowtie on your TV`
  - Field label: `Your Bowtie server address`; placeholder `http://192.168.1.50:8400`; button `Continue`
  - Hint: `Use the address you open Bowtie with in your browser.`
  - Waiting page heading: `Connecting to your TV…`
  - Bad address error: `Enter an address like http://192.168.1.50:8400.`
  - Unreachable error: `Couldn't reach a Bowtie server at <address>. Check the address and try again.` (`<address>` HTML-escaped)
  - Expired/locked (403): `This pairing code has expired. Scan the code on your TV again.`
  - TV caption under the QR: `Scan with your phone to set up this TV` and on the next line `Or open <http://ip:port/p?t=…> on your phone`
- All user-supplied text in pages is HTML-escaped (`& < > " '`).
- Commit trailer on every commit: `Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>`. No model names elsewhere.
- Never install or upgrade Homebrew packages. No ADB. Roku device testing only on the **bedroom** Roku after the user confirms its IP (Task 12).

## Shared page markup (all platforms produce this, byte-identical)

```
<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">{REFRESH}<title>Bowtie</title><style>body{font-family:-apple-system,system-ui,sans-serif;background:#101418;color:#f2efe8;margin:0;padding:32px 20px}main{max-width:420px;margin:0 auto}h1{color:#f0a428;font-size:24px}label{display:block;margin:16px 0 8px}input{width:100%;box-sizing:border-box;font-size:18px;padding:12px;border-radius:8px;border:1px solid #9ba5ae;background:#1b2128;color:#f2efe8}button{margin-top:16px;width:100%;font-size:18px;padding:12px;border:0;border-radius:8px;background:#f0a428;color:#101418;font-weight:600}.err{color:#e05050}.hint{color:#9ba5ae;font-size:14px}</style></head><body><main>{BODY}</main></body></html>
```

- `{REFRESH}` is empty, or on the waiting page `<meta http-equiv="refresh" content="1;url=/p/status?t={TOKEN}">`.
- Form body (`{ERROR}` empty or `<p class="err">{escaped message}</p>`; `{VALUE}` the escaped previously-entered address or empty):
  `<h1>Set up Bowtie on your TV</h1>{ERROR}<form method="post" action="/p"><input type="hidden" name="t" value="{TOKEN}"><label for="s">Your Bowtie server address</label><input id="s" name="server" type="text" inputmode="url" autocapitalize="off" autocorrect="off" spellcheck="false" placeholder="http://192.168.1.50:8400" value="{VALUE}" required><button type="submit">Continue</button></form><p class="hint">Use the address you open Bowtie with in your browser.</p>`
- Waiting body: `<h1>Connecting to your TV…</h1>`
- 403 body: `<h1>Set up Bowtie on your TV</h1><p class="err">This pairing code has expired. Scan the code on your TV again.</p>`
- 404 body: `<h1>Not found</h1>`
- HTTP response: `HTTP/1.1 <code> <reason>\r\n` + `Content-Type: text/html; charset=utf-8\r\n` (omitted on 303) + `Content-Length: <bytes>\r\n` + `Cache-Control: no-store\r\n` + `Connection: close\r\n` + (`Location: <url>\r\n` on 303) + `\r\n` + body. Reasons: 200 OK, 303 See Other, 400 Bad Request, 403 Forbidden, 404 Not Found, 413 Payload Too Large.

## Session behaviour table (all platforms)

States: `waiting` (no address yet) → `connecting(address)` → `failed(address, message)` | `ready(linkURL)`; plus `closed` (locked out or stopped).

| Request | State | Response | Action |
|---|---|---|---|
| any | closed | 403 | — |
| `GET /p`, t ok | waiting/failed | 200 form (failed: error + value = address) | — |
| `GET /p`, t ok | connecting/ready | 303 `/p/status?t=T` | — |
| `POST /p`, t ok, `server` normalizes | waiting/failed | 303 `/p/status?t=T` | state → connecting(normalized); emit submit(normalized) |
| `POST /p`, t ok, `server` doesn't normalize | waiting/failed | 200 form with bad-address error, value = raw input | state unchanged |
| `POST /p`, t ok | connecting/ready | 303 `/p/status?t=T` | — |
| `GET /p/status`, t ok | connecting | 200 waiting page | — |
| `GET /p/status`, t ok | failed | 200 form with unreachable error | — |
| `GET /p/status`, t ok | ready | 303 linkURL | — |
| `GET /p/status`, t ok | waiting | 303 `/p?t=T` | — |
| `/p` or `/p/status`, t missing | any | 403 | — (no count) |
| `/p` or `/p/status`, t wrong | any | 403 | badCount += 1; at 20 → closed |
| other method on `/p`, `/p/status` | any | 404 | — |
| other path | any | 404 | — |
| request unparseable | any | 400 body `<h1>Bad request</h1>` | — |
| request > 8192 bytes | any | 413 body `<h1>Too large</h1>` | — |

Session methods (names identical on every platform, cased per language): `handle(rawRequest) -> (response, submitted?)`, `connectFailed(address)`, `codeIssued(linkURL)`, `close()`. `normalize` is the platform's existing server-URL normalizer.

## Review Focus

1. **Phone browser autofills/retypes with a trailing slash or no scheme** (`192.168.1.50:8400/`) — expected to work like typing it on the TV. Test: POST with `server=192.168.1.50%3A8400%2F` → connecting(`http://192.168.1.50:8400`) on each platform (Tasks 2, 4, 7).
2. **Form body arrives in a second TCP read** (iOS Safari sends headers then body) — listener must keep reading until `Content-Length` bytes. Test: localhost socket test sends headers and body in two writes 100 ms apart (Tasks 5, 8; Roku in the PairTask on device).
3. **Phone hits Back/refresh after redirect or reopens the QR later** — status/form must not resubmit or crash: `GET /p` in ready → 303 status → 303 link. Test rows in the session tables (Tasks 2, 4, 7).
4. **TV's sign-in code expires and a new one is issued** (user lingered) — `codeIssued` again replaces linkURL. Test: two `codeIssued` calls, status redirects to the second (Tasks 2, 4, 7).
5. **Percent-encoded and `+` spaces in form data, and HTML in the address** (`"><script>`) — decoded once, rejected as a bad address, and echoed escaped. Test: `server=%22%3E%3Cscript%3E` → 200 form whose body contains `value="&quot;&gt;&lt;script&gt;"` and not `<script>` (Tasks 2, 4, 7).

---

## Task 1: Roku QR encoder (front-loaded risk)

**Files:**
- Create: `roku/source/lib/Qr.bs`
- Create: `roku/test/qr.test.mjs`
- Modify: `roku/package.json` (devDependency `jsqr` `1.4.0`)

**Interfaces:**
- Produces: `bowtie.qr.encode(text as string) as object` → `{ size: integer, rows: [[boolean,…],…] }` (true = dark), or `invalid` when text > 106 bytes. `bowtie.qr.runs(matrix) as object` → array of `{ x, y, w }` (module units; one per horizontal run of dark modules) for drawing.

Algorithm (ISO/IEC 18004, byte mode, ECC level M only):

| Version | Size | Data codewords | EC per block | Blocks (data per block) | Alignment centers |
|---|---|---|---|---|---|
| 1 | 21 | 16 | 10 | 1×16 | — |
| 2 | 25 | 28 | 16 | 1×28 | 6, 18 |
| 3 | 29 | 44 | 26 | 1×44 | 6, 22 |
| 4 | 33 | 64 | 18 | 2×32 | 6, 26 |
| 5 | 37 | 86 | 24 | 2×43 | 6, 30 |
| 6 | 41 | 108 | 16 | 4×27 | 6, 34 |

1. Pick the smallest version whose byte capacity (data codewords − 2) ≥ len(text) (14, 26, 42, 62, 84, 106).
2. Bits: mode `0100`, 8-bit length, the bytes (UTF-8; the inputs are ASCII), terminator of up to 4 zero bits, pad to a byte boundary, then pad bytes `0xEC, 0x11` alternating to the data-codeword count.
3. Split data into blocks; for each block compute Reed-Solomon EC over GF(256) (primitive poly `0x11D`, generator ∏(x − αⁱ), i = 0…ec−1).
4. Interleave: data codewords column-wise across blocks, then EC codewords column-wise.
5. Matrix: finder patterns (7×7) at three corners with white separators, timing patterns on row 6 / column 6, alignment patterns (5×5) at every center pair from the table that doesn't overlap a finder, dark module at (row 4V+9, col 8), reserved format areas.
6. Place bits in the standard zig-zag (2-column strips from the right, skipping column 6, alternating up/down), MSB first.
7. For each mask 0–7 (`(r+c)%2`, `r%2`, `c%3`, `(r+c)%3`, `(r\2+c\3)%2`, `(r*c)%2+(r*c)%3`, `((r*c)%2+(r*c)%3)%2`, `((r+c)%2+(r*c)%3)%2`, each "== 0 → flip"), apply to non-function modules, write format bits, score with penalties N1=3, N2=3, N3=40, N4=10; keep the lowest (ties → lowest mask number).
8. Format bits: 5 bits = EC level M `00` + mask (3 bits); BCH(15,5) remainder with generator `0x537`; XOR `0x5412`; place both copies (around the top-left finder, and split along the bottom-left/top-right).

- [ ] **Step 1: Add the decode dependency**

```bash
cd roku && npm install --save-dev jsqr@1.4.0
```

- [ ] **Step 2: Write the failing tests** (`roku/test/qr.test.mjs`)

```js
// QR encoder (source/lib/Qr.brs), run under brs and decoded with jsqr: the
// pairing QR is drawn on the Roku itself, so it must actually scan.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import jsQR from 'jsqr';
import { runBrs } from './brs-run.mjs';

const LIB = ['source/lib/Qr.brs'];
const lit = (s) => `"${s.replace(/"/g, '""')}"`;

function encode(text) {
    return runBrs(LIB, `out.m = bowtie_qr_encode(${lit(text)})`).m;
}

// Matrix → RGBA image (4 px per module, 4-module quiet zone) → jsqr.
function decode(m) {
    const scale = 4, quiet = 4, n = (m.size + quiet * 2) * scale;
    const data = new Uint8ClampedArray(n * n * 4).fill(255);
    for (let r = 0; r < m.size; r++) for (let c = 0; c < m.size; c++) {
        if (!m.rows[r][c]) continue;
        for (let dy = 0; dy < scale; dy++) for (let dx = 0; dx < scale; dx++) {
            const i = (((r + quiet) * scale + dy) * n + (c + quiet) * scale + dx) * 4;
            data[i] = data[i + 1] = data[i + 2] = 0;
        }
    }
    return jsQR(data, n, n)?.data;
}

const CASES = [
    'A',
    'http://10.0.0.5:8080/p?t=0123456789abcdef01234567',
    'http://192.168.1.123:54321/p?t=0123456789abcdef01234567',
    'http://255.255.255.255:65535/p?t=0123456789abcdef01234567',
    'x'.repeat(84),
    'y'.repeat(106),
];

for (const text of CASES) {
    test(`encodes ${text.length} bytes so it scans back`, () => {
        const m = encode(text);
        assert.equal(m.rows.length, m.size);
        assert.equal(decode(m), text);
    });
}

test('picks the smallest version (v4 = 33 modules for the longest pairing URL)', () => {
    assert.equal(encode('http://255.255.255.255:65535/p?t=0123456789abcdef01234567').size, 33);
    assert.equal(encode('A').size, 21);
});

test('too long returns invalid', () => {
    const got = runBrs(LIB, `out.isinvalid = bowtie_qr_encode(${lit('z'.repeat(107))}) = invalid`);
    assert.equal(got.isinvalid, true);
});

test('runs cover exactly the dark modules', () => {
    const got = runBrs(LIB, `
        m = bowtie_qr_encode("http://192.168.1.2:5000/p?t=abc")
        out.m = snapshot(m)
        out.runs = bowtie_qr_runs(m)
    `);
    let dark = 0;
    for (const row of got.m.rows) for (const v of row) if (v) dark++;
    const covered = got.runs.reduce((s, r) => s + r.w, 0);
    assert.equal(covered, dark);
    for (const r of got.runs) for (let c = r.x; c < r.x + r.w; c++) assert.equal(got.m.rows[r.y][c], true);
});
```

- [ ] **Step 3: Run to verify failure**

Run: `cd roku && npm test 2>&1 | tail -20`
Expected: FAIL — `source/lib/Qr.brs missing from out/staging`.

- [ ] **Step 4: Implement `roku/source/lib/Qr.bs`** in `namespace bowtie.qr` following the algorithm above. Structure it as small functions: `capacityVersion(n)`, `dataCodewords(bytes, version)`, `rsRemainder(data, ecLen)` (with `gfMul` via exp/log tables built once into `m.global`-free local arrays — pass tables as arguments; brs has no module-level state), `interleave`, `newMatrix(version)` returning `{ dark: [[...]], fn: [[...]] }` (function-module flags), `placeData`, `applyMask`, `writeFormat`, `penalty`, `encode`, `runs`. Use `bitwise`: BrightScript has `and`, `or`, `<<`, `>>` on integers; XOR is `(a or b) and not (a and b)`.

- [ ] **Step 5: Run tests until they pass**

Run: `cd roku && npm test 2>&1 | tail -20` and `npm run lint`
Expected: all qr tests PASS; lint clean.

- [ ] **Step 6: Commit**

```bash
git add roku/source/lib/Qr.bs roku/test/qr.test.mjs roku/package.json roku/package-lock.json
git commit -m "feat(roku): QR encoder for codes drawn on the TV itself"
```

## Task 2: Roku pairing session (pure)

**Files:**
- Create: `roku/source/lib/PairHttp.bs`
- Create: `roku/test/pair-http.test.mjs`

**Interfaces:**
- Consumes: `bowtie.client.normalizeBase(raw)` is not reused (it assumes a valid URL); this task adds `bowtie.pair.normalizeServer(raw) as string` (empty string when invalid) mirroring `ConnectScene.normalizeServerUrl` (trim, add `http://` if no `://`, strip trailing `/`, http/https only, host must match `^[A-Za-z0-9.-]+$` with an optional `:port` of digits — so `"><script>` is rejected the same way the Apple/Android URL parsers reject it), and Task 3 switches `ConnectScene` to call it so both paths share one normalizer.
- Produces (namespace `bowtie.pair`):
  - `newSession(token as string) as object` → `{ token, state: "waiting", address: "", message: "", linkUrl: "", bad: 0 }`
  - `handle(session as object, raw as string) as object` → `{ response: string, submitted: string }` (submitted "" unless a new address was accepted). Mutates `session`.
  - `connectFailed(session, address)`; `codeIssued(session, linkUrl)`; `close(session)`
  - `urlDecode(s) as string`, `htmlEscape(s) as string`, `linkUrl(server, userCode) as string` (= server + "/link?code=" + userCode without "-")
  - Constants `MAX_REQUEST = 8192`, `MAX_BAD = 20`

- [ ] **Step 1: Write the failing tests** (`roku/test/pair-http.test.mjs`)

```js
// TV pairing page (source/lib/PairHttp.brs): the tiny HTTP page the Roku
// serves so a phone can type the server address. Pure request → response.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { runBrs } from './brs-run.mjs';

const LIB = ['source/lib/PairHttp.brs'];
const lit = (s) => `"${s.replace(/"/g, '""').replace(/\r/g, '" + chr(13) + "').replace(/\n/g, '" + chr(10) + "')}"`;
const T = '0123456789abcdef01234567';
const get = (path) => `GET ${path} HTTP/1.1\r\nHost: tv\r\n\r\n`;
const post = (body) => `POST /p HTTP/1.1\r\nHost: tv\r\nContent-Type: application/x-www-form-urlencoded\r\nContent-Length: ${Buffer.byteLength(body)}\r\n\r\n${body}`;

// Runs a script of steps against one session; each step is BrightScript.
function run(steps) {
    return runBrs(LIB, `
        s = bowtie_pair_newSession(${lit(T)})
        out.r = []
        ${steps.join('\n')}
        out.state = s.state
        out.address = s.address
    `);
}
const req = (raw) => `out.r.push(snapshot(bowtie_pair_handle(s, ${lit(raw)})))`;
const status = (r) => r.response.split('\r\n')[0];
const header = (r, name) => r.response.split('\r\n').find((l) => l.toLowerCase().startsWith(name.toLowerCase() + ':'))?.split(': ')[1];
const body = (r) => r.response.split('\r\n\r\n').slice(1).join('\r\n\r\n');

test('GET /p with the token shows the form', () => {
    const { r } = run([req(get(`/p?t=${T}`))]);
    assert.equal(status(r[0]), 'HTTP/1.1 200 OK');
    assert.match(body(r[0]), /<h1>Set up Bowtie on your TV<\/h1>/);
    assert.match(body(r[0]), new RegExp(`name="t" value="${T}"`));
    assert.equal(header(r[0], 'Connection'), 'close');
    assert.equal(header(r[0], 'Content-Length'), String(Buffer.byteLength(body(r[0]))));
});

test('POST normalizes the address, submits it once and redirects to status', () => {
    const out = run([req(post(`t=${T}&server=192.168.1.50%3A8400%2F`)), req(post(`t=${T}&server=other`))]);
    assert.equal(status(out.r[0]), 'HTTP/1.1 303 See Other');
    assert.equal(header(out.r[0], 'Location'), `/p/status?t=${T}`);
    assert.equal(out.r[0].submitted, 'http://192.168.1.50:8400');
    assert.equal(out.r[1].submitted, '');
    assert.equal(out.state, 'connecting');
});

test('a bad address re-shows the form with the error and what was typed', () => {
    const { r, state } = run([req(post(`t=${T}&server=ftp%3A%2F%2Fx`))]);
    assert.equal(status(r[0]), 'HTTP/1.1 200 OK');
    assert.match(body(r[0]), /Enter an address like http:\/\/192\.168\.1\.50:8400\./);
    assert.match(body(r[0]), /value="ftp:\/\/x"/);
    assert.equal(state, 'waiting');
});

test('status: waiting page with refresh while connecting, then redirect to link', () => {
    const { r } = run([
        req(post(`t=${T}&server=http%3A%2F%2Fbowtie.lan%3A8400`)),
        req(get(`/p/status?t=${T}`)),
        'bowtie_pair_codeIssued(s, bowtie_pair_linkUrl("http://bowtie.lan:8400", "BCDF-2345"))',
        req(get(`/p/status?t=${T}`)),
        'bowtie_pair_codeIssued(s, bowtie_pair_linkUrl("http://bowtie.lan:8400", "GHJK-6789"))',
        req(get(`/p/status?t=${T}`)),
        req(get(`/p?t=${T}`)),
    ]);
    assert.match(body(r[1]), /Connecting to your TV…/);
    assert.match(body(r[1]), new RegExp(`content="1;url=/p/status\\?t=${T}"`));
    assert.equal(header(r[2], 'Location'), 'http://bowtie.lan:8400/link?code=BCDF2345');
    assert.equal(header(r[3], 'Location'), 'http://bowtie.lan:8400/link?code=GHJK6789');
    assert.equal(header(r[4], 'Location'), `/p/status?t=${T}`);
});

test('connect failure shows the unreachable error and allows a retry', () => {
    const out = run([
        req(post(`t=${T}&server=bowtie.lan`)),
        'bowtie_pair_connectFailed(s, s.address)',
        req(get(`/p/status?t=${T}`)),
        req(post(`t=${T}&server=bowtie2.lan`)),
    ]);
    assert.match(body(out.r[1]), /Couldn&#39;t reach a Bowtie server at http:\/\/bowtie\.lan\. Check the address and try again\./);
    assert.equal(out.r[2].submitted, 'http://bowtie2.lan');
});

test('HTML in the address is rejected and echoed escaped', () => {
    const { r } = run([req(post(`t=${T}&server=%22%3E%3Cscript%3E`))]);
    assert.equal(r[0].submitted, '');
    assert.doesNotMatch(body(r[0]), /<script>/);
    assert.match(body(r[0]), /value="&quot;&gt;&lt;script&gt;"/);
});

test('wrong tokens lock the session after 20; missing tokens and 404s do not count', () => {
    const steps = [];
    for (let i = 0; i < 30; i++) steps.push(req(get('/favicon.ico')), req(get('/p')));
    for (let i = 0; i < 19; i++) steps.push(req(get('/p?t=nope')));
    steps.push(req(get(`/p?t=${T}`)), req(get('/p/status?t=nope')), req(get(`/p?t=${T}`)));
    const { r, state } = run(steps);
    assert.equal(status(r[0]), 'HTTP/1.1 404 Not Found');
    assert.equal(status(r[1]), 'HTTP/1.1 403 Forbidden');
    assert.equal(status(r[60 + 19]), 'HTTP/1.1 200 OK');
    assert.equal(status(r[60 + 21]), 'HTTP/1.1 403 Forbidden');
    assert.equal(state, 'closed');
});

test('garbage is 400, oversize is 413, other methods 404', () => {
    const { r } = run([req('hello'), req(post('t=' + 'a'.repeat(9000))), req(`PUT /p?t=${T} HTTP/1.1\r\n\r\n`)]);
    assert.equal(status(r[0]), 'HTTP/1.1 400 Bad Request');
    assert.equal(status(r[1]), 'HTTP/1.1 413 Payload Too Large');
    assert.equal(status(r[2]), 'HTTP/1.1 404 Not Found');
});

test('urlDecode handles + and %XX; htmlEscape covers the five characters', () => {
    const got = runBrs(LIB, `
        out.d = bowtie_pair_urlDecode("a+b%2Fc%3a%25")
        out.e = bowtie_pair_htmlEscape("<a href=""x"">'&'</a>")
    `);
    assert.equal(got.d, 'a b/c:%');
    assert.equal(got.e, '&lt;a href=&quot;x&quot;&gt;&#39;&amp;&#39;&lt;/a&gt;');
});
```

- [ ] **Step 2: Run to verify failure**

Run: `cd roku && npm test 2>&1 | grep -E "pair|fail" | head`
Expected: FAIL — `source/lib/PairHttp.brs missing`.

- [ ] **Step 3: Implement `roku/source/lib/PairHttp.bs`** (namespace `bowtie.pair`) exactly per the behaviour table and shared markup. Parsing: split at the first `\r\n\r\n` (no blank line and length < MAX → treat as incomplete: return `{ response: "", submitted: "" }` with `incomplete: true` so the PairTask keeps reading); request line `METHOD SP target SP HTTP/x`; path and query split at `?`; query/form params split on `&` then `=`, both sides `urlDecode`d; `Content-Length` header (case-insensitive) — body shorter than it → incomplete. Length > `MAX_REQUEST` → 413 regardless. Token compare: plain string equality is fine on Roku (no timing oracle at LAN scale; note this in a comment).

- [ ] **Step 4: Run tests until they pass**

Run: `cd roku && npm test 2>&1 | tail -15 && npm run lint`
Expected: PASS, lint clean.

- [ ] **Step 5: Commit**

```bash
git add roku/source/lib/PairHttp.bs roku/test/pair-http.test.mjs
git commit -m "feat(roku): pairing page session (form, status, lockout) as pure logic"
```

## Task 3: Roku listener, screens and deploy script

**Files:**
- Create: `roku/components/tasks/PairTask.xml`, `roku/components/tasks/PairTask.bs`
- Create: `roku/components/QrCode.xml`, `roku/components/QrCode.bs` (Group that draws `bowtie.qr.runs` as Rectangles on a white frame)
- Modify: `roku/components/ConnectScene.xml`, `roku/components/ConnectScene.bs` (QR panel, no auto keyboard, shared normalizer, pair submissions)
- Modify: `roku/components/AppScene.xml`, `roku/components/AppScene.bs` (owns the PairTask; starts it on connect phase; stops on signed-in)
- Modify: `roku/components/LoginScene.xml`, `roku/components/LoginScene.bs` (`pairTask` field; send link URL when a code is issued)
- Create: `roku/scripts/deploy.sh`; Modify: `roku/package.json` (`"deploy": "npm run package && ./scripts/deploy.sh"`), `roku/README.md`
- Test: `roku/test/pair-scene.test.mjs` (structure checks like `test/login-scene.test.mjs`)

**Interfaces:**
- Consumes: Task 1 `bowtie.qr.encode/runs`, Task 2 `bowtie.pair.*`.
- Produces: `PairTask` fields: `pairUrl` (string, out: `http://ip:port/p?t=…`, "" if unavailable), `submitted` (string, out, alwaysNotify), `connectFailed` (string in: address), `linkUrl` (string in), `stop` (boolean in). `QrCode` field `text` (string) and `moduleSize` (integer, default 12).

PairTask run loop (`functionName="pairRun"`):
```brightscript
sub pairRun()
    port = CreateObject("roMessagePort")
    m.top.observeField("connectFailed", port)
    m.top.observeField("linkUrl", port)
    m.top.observeField("stop", port)
    ip = lanIp()  ' first value of CreateObject("roDeviceInfo").GetIPAddrs() matching ^\d+\.\d+\.\d+\.\d+$
    token = Left(CreateObject("roDeviceInfo").GetRandomUUID().Replace("-", "").LCase(), 24)
    listener = invalid
    for attempt = 1 to 10
        p = 49152 + Rnd(16000)
        addr = CreateObject("roSocketAddress") : addr.setPort(p)
        s = CreateObject("roStreamSocket")
        s.setMessagePort(port) : s.setAddress(addr) : s.notifyReadable(true)
        if s.listen(4) and s.eOK() then listener = s : exit for
        s.close()
    end for
    if ip = "" or listener = invalid then m.top.pairUrl = "" : return
    session = bowtie_pair_newSession(token)
    m.top.pairUrl = "http://" + ip + ":" + p.toStr() + "/p?t=" + token
    deadline = uptimeSeconds() + 900
    while uptimeSeconds() < deadline and session.state <> "closed"
        msg = wait(1000, port)
        ' roSGNodeEvent: connectFailed → bowtie_pair_connectFailed; linkUrl → bowtie_pair_codeIssued; stop → exit
        ' roSocketEvent on listener and readable: conn = listener.accept(); serve(conn, session)
    end while
    listener.close()
end sub
```
`serve(conn, session)`: blocking-read loop with `conn.receiveStr(4096)` while `conn.getCountRcvBuf() > 0` or until 5 s passed (`roTimespan`), appending to a buffer; after each read call `bowtie_pair_handle`; stop when result isn't `incomplete`; `conn.sendStr(result.response)`; `conn.close()`; if `result.submitted <> ""` set `m.top.submitted = result.submitted`. Sockets aren't available under brs: covered on device (Task 12).

ConnectScene changes:
- Remove the auto-open keyboard timer; focus `enterButton`, relabelled `Type the address instead`.
- New left panel: `QrCode` (id `pairQr`) + labels `Scan with your phone to set up this TV` and `Or open <pairUrl> on your phone`. Hidden when `pairUrl` is "".
- New interface fields `pairTask` (node) and `pairBusy`. Observe `pairTask.pairUrl` (set QR text) and `pairTask.submitted` → call `submitUrl(addr)` with `m.fromPair = true`. On healthz failure from a pair submission: `pairTask.connectFailed = addr` (and show `CONNECT_ERROR` on the TV too). Typed submission: `pairTask.stop = true` before validating.
- `normalizeServerUrl` body replaced by `return bowtie.pair.normalizeServer(raw)`.

AppScene changes: create `PairTask` in `setPhase("connect")` if none running (`control = "run"`), pass to `connectScene.pairTask` and `loginScene.pairTask`; on `onSignedIn` set `stop = true` and drop the reference. LoginScene: in `handleStartResponse`, after `m.session` is set, `if m.top.pairTask <> invalid then m.top.pairTask.linkUrl = bowtie.pair.linkUrl(serverAddress(), resp.data.userCode)`.

`roku/scripts/deploy.sh`:
```bash
#!/usr/bin/env bash
# Sideload out/bowtie-roku.zip to a Roku in developer mode.
#   ROKU_IP=192.168.x.y ROKU_DEV_PASSWORD=… npm run deploy
set -euo pipefail
: "${ROKU_IP:?set ROKU_IP}" "${ROKU_DEV_PASSWORD:?set ROKU_DEV_PASSWORD}"
cd "$(dirname "$0")/.."
curl -sS --fail --digest -u "rokudev:${ROKU_DEV_PASSWORD}" \
  -F "mysubmit=Install" -F "archive=@out/bowtie-roku.zip" \
  "http://${ROKU_IP}/plugin_install" | grep -oE 'Install Success|Identical to previous version|Install Failure[^<]*' || true
```

- [ ] **Step 1: Write failing structure tests** (`roku/test/pair-scene.test.mjs`)

```js
// Wiring checks for TV pairing (sockets can't run under brs; the device run
// in docs/deploy/roku-testing.md covers the listener itself).
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

const read = (p) => readFileSync(new URL(`../${p}`, import.meta.url), 'utf8');

test('PairTask exposes the fields the scenes use', () => {
    const xml = read('components/tasks/PairTask.xml');
    for (const f of ['pairUrl', 'submitted', 'connectFailed', 'linkUrl', 'stop']) assert.match(xml, new RegExp(`id="${f}"`));
    assert.match(xml, /functionName="pairRun"|<script/);
});

test('Connect no longer opens the keyboard by itself and offers typing as a button', () => {
    const bs = read('components/ConnectScene.bs');
    assert.doesNotMatch(bs, /onOpenKeyboardTimer/);
    assert.match(read('components/ConnectScene.xml'), /text="Type the address instead"/);
    assert.match(bs, /bowtie\.pair\.normalizeServer/);
});

test('Connect shows the pairing copy', () => {
    const xml = read('components/ConnectScene.xml');
    assert.match(xml, /Scan with your phone to set up this TV/);
    assert.match(xml, /<QrCode id="pairQr"/);
});

test('Login hands the link URL to the pairing task', () => {
    assert.match(read('components/LoginScene.bs'), /pairTask\.linkUrl = bowtie\.pair\.linkUrl\(/);
});

test('AppScene stops pairing once signed in', () => {
    assert.match(read('components/AppScene.bs'), /pairTask\.stop = true/);
});
```

- [ ] **Step 2: Run to verify failure** — `cd roku && npm test 2>&1 | grep -c "not ok"` → non-zero.
- [ ] **Step 3: Implement** PairTask, QrCode, and the scene changes above. QrCode: white `Rectangle` of `(size+8)*moduleSize`, then one black `Rectangle` per run at `[(x+4)*moduleSize, (y+4)*moduleSize]` sized `[w*moduleSize, moduleSize]`; rebuild children when `text` changes.
- [ ] **Step 4: Run** `cd roku && npm test && npm run lint` → PASS, clean. `npm run package` → `out/bowtie-roku.zip`; `git checkout` any tracked build output.
- [ ] **Step 5: Commit**

```bash
git add roku
git commit -m "feat(roku): set up the Roku from a phone (pairing page, QR on Connect); deploy script"
```

## Task 4: Apple pairing session (pure, BowtieKit)

**Files:**
- Create: `ios/BowtieKit/Sources/BowtieKit/Pairing/PairingSession.swift`, `ios/BowtieKit/Sources/BowtieKit/Pairing/PairingPage.swift` (markup + escaping), `ios/BowtieKit/Sources/BowtieKit/Pairing/HTTPRequest.swift` (minimal parser)
- Test: `ios/BowtieKit/Tests/BowtieKitTests/PairingSessionTests.swift`

**Interfaces:**
- Consumes: `ServerURL.normalize(_:) -> URL?`.
- Produces:
```swift
public struct PairingResponse: Equatable, Sendable { public let status: Int; public let headers: [(String, String)] /* ordered */; public let body: Data; public func serialized() -> Data }
public enum PairingParse: Equatable { case incomplete, complete(HTTPRequest), tooLarge, bad }
public struct HTTPRequest: Equatable, Sendable { public let method: String; public let path: String; public let query: [String: String]; public let form: [String: String]
    public static func parse(_ data: Data, limit: Int = 8192) -> PairingParse }
public final class PairingSession: @unchecked Sendable {   // used only on its owner's actor
    public enum State: Equatable { case waiting, connecting(URL), failed(URL, String), ready(URL), closed }
    public let token: String
    public private(set) var state: State
    public init(token: String = PairingSession.newToken())
    public static func newToken() -> String          // 24 hex from SecRandomCopyBytes
    public func handle(_ parse: PairingParse) -> (PairingResponse, submitted: URL?)
    public func connectFailed(_ address: URL)
    public func codeIssued(_ link: URL)
    public func close()
    public static func linkURL(server: URL, userCode: String) -> URL   // server + "/link?code=" + userCode minus "-"
}
```
`PairingResponse` headers are an array of tuples; implement `Equatable` by comparing `status`, `body` and `headers.map { "\($0.0): \($0.1)" }`.

- [ ] **Step 1: Write failing tests** (`PairingSessionTests.swift`) — same cases as Task 2, in XCTest:

```swift
import XCTest
@testable import BowtieKit

final class PairingSessionTests: XCTestCase {
    private let t = "0123456789abcdef01234567"

    private func get(_ path: String) -> PairingParse { HTTPRequest.parse(Data("GET \(path) HTTP/1.1\r\nHost: tv\r\n\r\n".utf8)) }
    private func post(_ body: String) -> PairingParse {
        HTTPRequest.parse(Data("POST /p HTTP/1.1\r\nContent-Type: application/x-www-form-urlencoded\r\nContent-Length: \(body.utf8.count)\r\n\r\n\(body)".utf8))
    }
    private func text(_ r: PairingResponse) -> String { String(decoding: r.body, as: UTF8.self) }
    private func header(_ r: PairingResponse, _ name: String) -> String? { r.headers.first { $0.0.caseInsensitiveCompare(name) == .orderedSame }?.1 }

    func testFormShowsToken() {
        let s = PairingSession(token: t)
        let (r, sub) = s.handle(get("/p?t=\(t)"))
        XCTAssertEqual(r.status, 200); XCTAssertNil(sub)
        XCTAssertTrue(text(r).contains("<h1>Set up Bowtie on your TV</h1>"))
        XCTAssertTrue(text(r).contains("name=\"t\" value=\"\(t)\""))
        XCTAssertEqual(header(r, "Connection"), "close")
        XCTAssertEqual(header(r, "Content-Length"), String(r.body.count))
    }

    func testPostNormalizesSubmitsOnceAndRedirects() {
        let s = PairingSession(token: t)
        let (r, sub) = s.handle(post("t=\(t)&server=192.168.1.50%3A8400%2F"))
        XCTAssertEqual(r.status, 303)
        XCTAssertEqual(header(r, "Location"), "/p/status?t=\(t)")
        XCTAssertEqual(sub?.absoluteString, "http://192.168.1.50:8400")
        XCTAssertNil(s.handle(post("t=\(t)&server=other")).submitted)
    }

    func testBadAddressReshowsForm() {
        let s = PairingSession(token: t)
        let (r, sub) = s.handle(post("t=\(t)&server=ftp%3A%2F%2Fx"))
        XCTAssertEqual(r.status, 200); XCTAssertNil(sub)
        XCTAssertTrue(text(r).contains("Enter an address like http://192.168.1.50:8400."))
        XCTAssertTrue(text(r).contains("value=\"ftp://x\""))
        XCTAssertEqual(s.state, .waiting)
    }

    func testStatusWaitsThenRedirectsToLatestCode() {
        let s = PairingSession(token: t)
        let server = URL(string: "http://bowtie.lan:8400")!
        _ = s.handle(post("t=\(t)&server=http%3A%2F%2Fbowtie.lan%3A8400"))
        let waiting = s.handle(get("/p/status?t=\(t)")).0
        XCTAssertTrue(text(waiting).contains("Connecting to your TV…"))
        XCTAssertTrue(text(waiting).contains("content=\"1;url=/p/status?t=\(t)\""))
        s.codeIssued(PairingSession.linkURL(server: server, userCode: "BCDF-2345"))
        XCTAssertEqual(header(s.handle(get("/p/status?t=\(t)")).0, "Location"), "http://bowtie.lan:8400/link?code=BCDF2345")
        s.codeIssued(PairingSession.linkURL(server: server, userCode: "GHJK-6789"))
        XCTAssertEqual(header(s.handle(get("/p/status?t=\(t)")).0, "Location"), "http://bowtie.lan:8400/link?code=GHJK6789")
        XCTAssertEqual(header(s.handle(get("/p?t=\(t)")).0, "Location"), "/p/status?t=\(t)")
    }

    func testConnectFailureAllowsRetry() {
        let s = PairingSession(token: t)
        let (_, sub) = s.handle(post("t=\(t)&server=bowtie.lan"))
        s.connectFailed(sub!)
        let page = text(s.handle(get("/p/status?t=\(t)")).0)
        XCTAssertTrue(page.contains("Couldn&#39;t reach a Bowtie server at http://bowtie.lan. Check the address and try again."))
        XCTAssertEqual(s.handle(post("t=\(t)&server=bowtie2.lan")).submitted?.absoluteString, "http://bowtie2.lan")
    }

    func testHTMLInAddressIsRejectedAndEscaped() {
        let (r, sub) = PairingSession(token: t).handle(post("t=\(t)&server=%22%3E%3Cscript%3E"))
        XCTAssertNil(sub)
        XCTAssertFalse(text(r).contains("<script>"))
        XCTAssertTrue(text(r).contains("value=\"&quot;&gt;&lt;script&gt;\""))
    }

    func testLockoutCountsOnlyWrongTokens() {
        let s = PairingSession(token: t)
        for _ in 0..<30 {
            XCTAssertEqual(s.handle(get("/favicon.ico")).0.status, 404)
            XCTAssertEqual(s.handle(get("/p")).0.status, 403)
        }
        for _ in 0..<19 { _ = s.handle(get("/p?t=nope")) }
        XCTAssertEqual(s.handle(get("/p?t=\(t)")).0.status, 200)
        _ = s.handle(get("/p/status?t=nope"))
        XCTAssertEqual(s.handle(get("/p?t=\(t)")).0.status, 403)
        XCTAssertEqual(s.state, .closed)
    }

    func testParserLimits() {
        XCTAssertEqual(HTTPRequest.parse(Data("hello\r\n\r\n".utf8)), .bad)
        XCTAssertEqual(HTTPRequest.parse(Data("GET /p HTTP/1.1\r\nHost: x".utf8)), .incomplete)
        let big = "t=" + String(repeating: "a", count: 9000)
        XCTAssertEqual(HTTPRequest.parse(Data("POST /p HTTP/1.1\r\nContent-Length: \(big.count)\r\n\r\n\(big)".utf8)), .tooLarge)
        XCTAssertEqual(HTTPRequest.parse(Data("POST /p HTTP/1.1\r\nContent-Length: 10\r\n\r\nt=1".utf8)), .incomplete)
        XCTAssertEqual(PairingSession(token: t).handle(HTTPRequest.parse(Data("PUT /p?t=\(t) HTTP/1.1\r\n\r\n".utf8))).0.status, 404)
    }

    func testFormDecodingAndToken() {
        guard case .complete(let r) = HTTPRequest.parse(Data("GET /p?a=x+y%2Fz%3a HTTP/1.1\r\n\r\n".utf8)) else { return XCTFail() }
        XCTAssertEqual(r.query["a"], "x y/z:")
        let tok = PairingSession.newToken()
        XCTAssertEqual(tok.count, 24)
        XCTAssertTrue(tok.allSatisfy { "0123456789abcdef".contains($0) })
    }
}
```

- [ ] **Step 2: Run to verify failure** — `cd ios && swift test --package-path BowtieKit --filter PairingSessionTests 2>&1 | tail -5` → compile error (types missing).
- [ ] **Step 3: Implement** the three files per the behaviour table and shared markup. Token compare in constant time (`zip` + reduce `|` over the UTF-8 bytes, length checked first).
- [ ] **Step 4: Run** `swift test --package-path BowtieKit 2>&1 | tail -5` → all PASS.
- [ ] **Step 5: Commit**

```bash
git add ios/BowtieKit
git commit -m "feat(apple): pairing page session (form, status, lockout) in BowtieKit"
```

## Task 5: Apple listener, LAN address, coordinator

**Files:**
- Create: `ios/BowtieKit/Sources/BowtieKit/Pairing/PairingListener.swift` (NWListener → PairingSession)
- Create: `ios/BowtieKit/Sources/BowtieKit/Pairing/LocalAddress.swift` (`getifaddrs`)
- Create: `ios/BowtieKit/Sources/BowtieKit/Pairing/PairingCoordinator.swift`
- Test: `ios/BowtieKit/Tests/BowtieKitTests/PairingListenerTests.swift`

**Interfaces:**
- Consumes: Task 4 types.
- Produces:
```swift
public enum LocalAddress { public static func lanIPv4() -> String? }   // first up, non-loopback IPv4 on en*/eth*, else first non-loopback IPv4
public final class PairingListener {   // all callbacks on `queue`
    public init(session: PairingSession, queue: DispatchQueue = .main, onSubmit: @escaping (URL) -> Void)
    public func start(completion: @escaping (UInt16?) -> Void)   // nil if it couldn't bind
    public func stop()
}
@MainActor @Observable public final class PairingCoordinator {
    public private(set) var pairURL: URL?          // http://ip:port/p?t=token, nil until listening / when unavailable
    public init(connect: @escaping @MainActor (String) async -> Bool, host: @escaping () -> String? = LocalAddress.lanIPv4, lifetime: Duration = .seconds(900))
    public func start()                          // idempotent
    public func codeIssued(server: URL, userCode: String)
    public func stop()
}
```
Coordinator: on submit → `Task { if await connect(url.absoluteString) == false { session.connectFailed(url) } }`; keeps `lastServer` so `codeIssued` can be checked (`codeIssued` ignored unless `server` equals the submitted address after normalization — a TV that later switched servers by hand mustn't redirect the phone elsewhere); stop after `lifetime` via a `Task.sleep`.

Listener: `NWListener(using: .tcp, on: .any)`; `newConnectionHandler` handles one at a time (queue serial; reject while busy with `cancel()`); per connection `receive(minimumIncompleteLength: 1, maximumLength: 8192)` accumulating into a buffer, calling `HTTPRequest.parse` after each chunk until not `.incomplete`; 5 s `DispatchWorkItem` timeout cancels; send `response.serialized()` then `cancel()` in the send completion.

- [ ] **Step 1: Write failing tests** (`PairingListenerTests.swift`)

```swift
import XCTest
import Network
@testable import BowtieKit

final class PairingListenerTests: XCTestCase {
    /// Raw TCP client so we control how the request is split.
    private func exchange(port: UInt16, parts: [String], gap: TimeInterval = 0.1) async throws -> String {
        let conn = NWConnection(host: "127.0.0.1", port: NWEndpoint.Port(rawValue: port)!, using: .tcp)
        conn.start(queue: .global())
        for (i, p) in parts.enumerated() {
            if i > 0 { try await Task.sleep(nanoseconds: UInt64(gap * 1_000_000_000)) }
            conn.send(content: Data(p.utf8), completion: .contentProcessed { _ in })
        }
        return try await withCheckedThrowingContinuation { cont in
            var acc = Data()
            func read() {
                conn.receive(minimumIncompleteLength: 1, maximumLength: 65536) { data, _, done, err in
                    if let data { acc.append(data) }
                    if done || err != nil { conn.cancel(); cont.resume(returning: String(decoding: acc, as: UTF8.self)) } else { read() }
                }
            }
            read()
        }
    }

    func testBodyInASecondWriteStillSubmits() async throws {
        let t = "0123456789abcdef01234567"
        let session = PairingSession(token: t)
        let submitted = expectation(description: "submit")
        let listener = PairingListener(session: session, queue: .main) { url in
            XCTAssertEqual(url.absoluteString, "http://bowtie.lan:8400"); submitted.fulfill()
        }
        let port: UInt16 = try await withCheckedThrowingContinuation { c in
            listener.start { p in p.map { c.resume(returning: $0) } ?? c.resume(throwing: URLError(.cannotConnectToHost)) }
        }
        defer { listener.stop() }
        let body = "t=\(t)&server=bowtie.lan%3A8400"
        let reply = try await exchange(port: port, parts: [
            "POST /p HTTP/1.1\r\nContent-Type: application/x-www-form-urlencoded\r\nContent-Length: \(body.utf8.count)\r\n\r\n",
            body,
        ])
        XCTAssertTrue(reply.hasPrefix("HTTP/1.1 303 See Other\r\n"))
        await fulfillment(of: [submitted], timeout: 2)
    }

    @MainActor
    func testCoordinatorRedirectsOnlyForTheSubmittedServer() async throws {
        var connected: [String] = []
        let c = PairingCoordinator(connect: { connected.append($0); return true }, host: { "127.0.0.1" })
        c.start()
        for _ in 0..<50 where c.pairURL == nil { try await Task.sleep(nanoseconds: 20_000_000) }
        let pair = try XCTUnwrap(c.pairURL)
        let port = UInt16(pair.port!)
        let token = URLComponents(url: pair, resolvingAgainstBaseURL: false)!.queryItems!.first { $0.name == "t" }!.value!
        let body = "t=\(token)&server=http%3A%2F%2Fbowtie.lan%3A8400"
        _ = try await exchange(port: port, parts: ["POST /p HTTP/1.1\r\nContent-Length: \(body.utf8.count)\r\n\r\n\(body)"])
        for _ in 0..<50 where connected.isEmpty { try await Task.sleep(nanoseconds: 20_000_000) }
        XCTAssertEqual(connected, ["http://bowtie.lan:8400"])
        c.codeIssued(server: URL(string: "http://elsewhere:1")!, userCode: "AAAA-BBBB")
        let waiting = try await exchange(port: port, parts: ["GET /p/status?t=\(token) HTTP/1.1\r\n\r\n"])
        XCTAssertTrue(waiting.contains("Connecting to your TV"))
        c.codeIssued(server: URL(string: "http://bowtie.lan:8400")!, userCode: "BCDF-2345")
        let ready = try await exchange(port: port, parts: ["GET /p/status?t=\(token) HTTP/1.1\r\n\r\n"])
        XCTAssertTrue(ready.contains("Location: http://bowtie.lan:8400/link?code=BCDF2345\r\n"))
        c.stop()
        XCTAssertNil(c.pairURL)
    }

    func testLanAddressLooksLikeIPv4() {
        if let ip = LocalAddress.lanIPv4() {
            XCTAssertEqual(ip.split(separator: ".").count, 4)
            XCTAssertNotEqual(ip, "127.0.0.1")
        }
    }
}
```

- [ ] **Step 2: Run to verify failure** — `cd ios && swift test --package-path BowtieKit --filter PairingListenerTests 2>&1 | tail -5` → compile error.
- [ ] **Step 3: Implement** the three files.
- [ ] **Step 4: Run** full `swift test --package-path BowtieKit` → PASS (run twice to catch flakiness).
- [ ] **Step 5: Commit**

```bash
git add ios/BowtieKit
git commit -m "feat(apple): pairing listener (Network.framework) and coordinator"
```

## Task 6: Apple TV wiring

**Files:**
- Modify: `ios/App/Shared/AppModel.swift` (own `pairing: PairingCoordinator`, lazily created; `stop()` on `.ready` and when `connect` is called from the typed field)
- Modify: `ios/App/Shared/ConnectView.swift` (tvOS-only QR panel)
- Create: `ios/App/tvOS/PairingQRView.swift` (CoreImage QR + caption)
- Modify: `ios/App/tvOS/TVSignInView.swift` (`onWaiting` → `appModel.pairing.codeIssued`)
- Modify: `ios/App/tvOS/Info.plist` (`NSLocalNetworkUsageDescription` = `Bowtie shows a setup page on your network so you can enter your server address from your phone.`)

**Interfaces:**
- Consumes: `PairingCoordinator` (Task 5).
- Produces: `AppModel.pairing` (`PairingCoordinator`), `AppModel.connectTyped(rawURL:)` = `pairing.stop()` + `connect(rawURL:)`.

Changes:
- `AppModel`: `public lazy var pairing = PairingCoordinator(connect: { [weak self] raw in await self?.connect(rawURL: raw) ?? false })` (with `@ObservationIgnored` on the lazy storage if the macro complains — use a stored `private var _pairing: PairingCoordinator?` and computed accessor if needed). In `completeDeviceSignIn` and `signIn` and `start()` success: `_pairing?.stop()`.
- `ConnectView`: `validate()` calls `appModel.connectTyped(rawURL:)`. Under `#if os(tvOS)`, put `PairingQRView(url: appModel.pairing.pairURL)` beside the existing form in an `HStack(spacing: 80)` and `.task { appModel.pairing.start() }`.
- `PairingQRView`: if `url` nil → `EmptyView()`; else white rounded rect (440pt) with `Image(uiImage:)` from `CIFilter.qrCodeGenerator()` (`message = Data(url.absoluteString.utf8)`, `correctionLevel = "M"`, scaled with `CGAffineTransform(scaleX: 12, y: 12)`, `.interpolation(.none)`), then `Text("Scan with your phone to set up this TV")` (Theme.title(32)) and `Text("Or open \(url.absoluteString) on your phone")` (Theme.body(22), Theme.dim). `accessibilityLabel` on the image: `QR code to set up this TV from your phone`.
- `TVPhoneSignInView` `.task`: before `start()`, `current.onWaiting = { [appModel, client] code in appModel.pairing.codeIssued(server: client.serverURL, userCode: code.userCode) }`. Check `DeviceSignInModel.Code` has `userCode` (it does, line ~50) and `BowtieClient.serverURL` exists (`DeviceSignIn.swift` uses `client.serverURL`).

- [ ] **Step 1:** Implement the changes.
- [ ] **Step 2: Build all Apple targets** — `make ios-test` (runs `swift test` + iOS/tvOS/macOS builds). Expected: success.
- [ ] **Step 3: Manual simulator check** — run the tvOS simulator (`xcodebuild -scheme BowtieTV -destination 'platform=tvOS Simulator,name=Apple TV' build` then boot/install/launch with `xcrun simctl`), screenshot the Connect screen (`xcrun simctl io booted screenshot`), open the shown URL in the Mac's browser (simulator shares the Mac's network; the URL may show the Mac's LAN IP), submit `http://127.0.0.1:8400` against a local Bowtie (with `BOWTIE_HDHOMERUN_GUIDE=off`), confirm the redirect lands on `/link?code=…`. Record what happened in the commit message body.
- [ ] **Step 4: Commit**

```bash
git add ios
git commit -m "feat(tvos): set up Apple TV from a phone (pairing QR on Connect)"
```

## Task 7: Android pairing session (pure, :core)

**Files:**
- Create: `android/core/src/main/kotlin/app/bowtie/core/pairing/HttpRequest.kt`, `PairingSession.kt`, `PairingPage.kt`
- Test: `android/core/src/test/kotlin/app/bowtie/core/pairing/PairingSessionTest.kt`

**Interfaces:**
- Consumes: `ServerUrl.normalize(raw): HttpUrl?` (returns no trailing slash in `toString()`? — check: if `HttpUrl.toString()` adds `/`, strip one trailing `/` when producing addresses and link URLs).
- Produces:
```kotlin
sealed interface Parse { data object Incomplete : Parse; data object TooLarge : Parse; data object Bad : Parse; data class Complete(val request: HttpRequest) : Parse }
data class HttpRequest(val method: String, val path: String, val query: Map<String, String>, val form: Map<String, String>) {
    companion object { fun parse(bytes: ByteArray, limit: Int = 8192): Parse; fun urlDecode(s: String): String } }
data class PairingResponse(val status: Int, val headers: List<Pair<String, String>>, val body: ByteArray) { fun serialize(): ByteArray }
class PairingSession(val token: String = newToken()) {
    sealed interface State { data object Waiting : State; data class Connecting(val address: String) : State; data class Failed(val address: String, val message: String) : State; data class Ready(val link: String) : State; data object Closed : State }
    @get:Synchronized var state: State; private set
    @Synchronized fun handle(parse: Parse): Pair<PairingResponse, String?>   // second = submitted address
    @Synchronized fun connectFailed(address: String)
    @Synchronized fun codeIssued(link: String)
    @Synchronized fun close()
    companion object { fun newToken(): String /* SecureRandom 12 bytes → hex */; fun linkUrl(server: String, userCode: String): String; fun htmlEscape(s: String): String }
}
```

- [ ] **Step 1: Write failing tests** — mirror Task 4's nine tests in JUnit (`assertEquals`, `assertTrue`), using helpers:

```kotlin
package app.bowtie.core.pairing

import org.junit.Assert.*
import org.junit.Test

class PairingSessionTest {
    private val t = "0123456789abcdef01234567"
    private fun get(path: String) = HttpRequest.parse("GET $path HTTP/1.1\r\nHost: tv\r\n\r\n".toByteArray())
    private fun post(body: String) = HttpRequest.parse(
        "POST /p HTTP/1.1\r\nContent-Type: application/x-www-form-urlencoded\r\nContent-Length: ${body.toByteArray().size}\r\n\r\n$body".toByteArray())
    private fun text(r: PairingResponse) = r.body.toString(Charsets.UTF_8)
    private fun header(r: PairingResponse, name: String) = r.headers.firstOrNull { it.first.equals(name, true) }?.second

    @Test fun formShowsToken() {
        val (r, sub) = PairingSession(t).handle(get("/p?t=$t"))
        assertEquals(200, r.status); assertNull(sub)
        assertTrue(text(r).contains("<h1>Set up Bowtie on your TV</h1>"))
        assertTrue(text(r).contains("name=\"t\" value=\"$t\""))
        assertEquals("close", header(r, "Connection"))
        assertEquals(r.body.size.toString(), header(r, "Content-Length"))
    }

    @Test fun postNormalizesSubmitsOnceAndRedirects() {
        val s = PairingSession(t)
        val (r, sub) = s.handle(post("t=$t&server=192.168.1.50%3A8400%2F"))
        assertEquals(303, r.status)
        assertEquals("/p/status?t=$t", header(r, "Location"))
        assertEquals("http://192.168.1.50:8400", sub)
        assertNull(s.handle(post("t=$t&server=other")).second)
    }

    @Test fun badAddressReshowsForm() {
        val s = PairingSession(t)
        val (r, sub) = s.handle(post("t=$t&server=ftp%3A%2F%2Fx"))
        assertEquals(200, r.status); assertNull(sub)
        assertTrue(text(r).contains("Enter an address like http://192.168.1.50:8400."))
        assertTrue(text(r).contains("value=\"ftp://x\""))
        assertEquals(PairingSession.State.Waiting, s.state)
    }

    @Test fun statusWaitsThenRedirectsToLatestCode() {
        val s = PairingSession(t)
        s.handle(post("t=$t&server=http%3A%2F%2Fbowtie.lan%3A8400"))
        val waiting = s.handle(get("/p/status?t=$t")).first
        assertTrue(text(waiting).contains("Connecting to your TV…"))
        assertTrue(text(waiting).contains("content=\"1;url=/p/status?t=$t\""))
        s.codeIssued(PairingSession.linkUrl("http://bowtie.lan:8400", "BCDF-2345"))
        assertEquals("http://bowtie.lan:8400/link?code=BCDF2345", header(s.handle(get("/p/status?t=$t")).first, "Location"))
        s.codeIssued(PairingSession.linkUrl("http://bowtie.lan:8400", "GHJK-6789"))
        assertEquals("http://bowtie.lan:8400/link?code=GHJK6789", header(s.handle(get("/p/status?t=$t")).first, "Location"))
        assertEquals("/p/status?t=$t", header(s.handle(get("/p?t=$t")).first, "Location"))
    }

    @Test fun connectFailureAllowsRetry() {
        val s = PairingSession(t)
        val sub = s.handle(post("t=$t&server=bowtie.lan")).second!!
        s.connectFailed(sub)
        val page = text(s.handle(get("/p/status?t=$t")).first)
        assertTrue(page.contains("Couldn&#39;t reach a Bowtie server at http://bowtie.lan. Check the address and try again."))
        assertEquals("http://bowtie2.lan", s.handle(post("t=$t&server=bowtie2.lan")).second)
    }

    @Test fun htmlInAddressIsRejectedAndEscaped() {
        val (r, sub) = PairingSession(t).handle(post("t=$t&server=%22%3E%3Cscript%3E"))
        assertNull(sub)
        assertFalse(text(r).contains("<script>"))
        assertTrue(text(r).contains("value=\"&quot;&gt;&lt;script&gt;\""))
    }

    @Test fun lockoutCountsOnlyWrongTokens() {
        val s = PairingSession(t)
        repeat(30) {
            assertEquals(404, s.handle(get("/favicon.ico")).first.status)
            assertEquals(403, s.handle(get("/p")).first.status)
        }
        repeat(19) { s.handle(get("/p?t=nope")) }
        assertEquals(200, s.handle(get("/p?t=$t")).first.status)
        s.handle(get("/p/status?t=nope"))
        assertEquals(403, s.handle(get("/p?t=$t")).first.status)
        assertEquals(PairingSession.State.Closed, s.state)
    }

    @Test fun parserLimits() {
        assertEquals(Parse.Bad, HttpRequest.parse("hello\r\n\r\n".toByteArray()))
        assertEquals(Parse.Incomplete, HttpRequest.parse("GET /p HTTP/1.1\r\nHost: x".toByteArray()))
        val big = "t=" + "a".repeat(9000)
        assertEquals(Parse.TooLarge, HttpRequest.parse("POST /p HTTP/1.1\r\nContent-Length: ${big.length}\r\n\r\n$big".toByteArray()))
        assertEquals(Parse.Incomplete, HttpRequest.parse("POST /p HTTP/1.1\r\nContent-Length: 10\r\n\r\nt=1".toByteArray()))
        assertEquals(404, PairingSession(t).handle(HttpRequest.parse("PUT /p?t=$t HTTP/1.1\r\n\r\n".toByteArray())).first.status)
    }

    @Test fun decodingAndToken() {
        assertEquals("x y/z:", HttpRequest.urlDecode("x+y%2Fz%3a"))
        val tok = PairingSession.newToken()
        assertEquals(24, tok.length)
        assertTrue(tok.all { it in "0123456789abcdef" })
    }
}
```

- [ ] **Step 2: Run to verify failure** — `cd android && ./gradlew :core:test --tests '*PairingSessionTest*' 2>&1 | tail -5` → compilation failure.
- [ ] **Step 3: Implement** per the behaviour table and shared markup; constant-time token compare via `MessageDigest.isEqual`.
- [ ] **Step 4: Run** `./gradlew :core:test` → PASS.
- [ ] **Step 5: Commit**

```bash
git add android/core
git commit -m "feat(android): pairing page session (form, status, lockout) in core"
```

## Task 8: Android listener, LAN address, coordinator

**Files:**
- Create: `android/core/src/main/kotlin/app/bowtie/core/pairing/PairingServer.kt` (ServerSocket on IO), `LanAddress.kt`, `PairingCoordinator.kt`
- Test: `android/core/src/test/kotlin/app/bowtie/core/pairing/PairingServerTest.kt`

**Interfaces:**
- Produces:
```kotlin
object LanAddress { fun ipv4(): String? }   // NetworkInterface: up, !loopback, site-local Inet4Address; prefer wlan*/eth*
class PairingServer(private val session: PairingSession, private val onSubmit: (String) -> Unit) {
    fun start(scope: CoroutineScope): Int?   // binds ServerSocket(0) on all interfaces, returns port or null; accept loop in scope on Dispatchers.IO
    fun stop()
}
class PairingCoordinator(
    private val scope: CoroutineScope,
    private val connect: suspend (String) -> Boolean,
    private val host: () -> String? = LanAddress::ipv4,
    private val lifetimeMs: Long = 15 * 60 * 1000L,
) {
    val pairUrl: StateFlow<String?>
    fun start()          // idempotent
    fun codeIssued(server: String, userCode: String)   // ignored unless server == submitted address
    fun stop()
}
```
Server: per connection `soTimeout = 5000`, read into a buffer until `HttpRequest.parse` isn't `Incomplete` (or 8192 bytes), write `serialize()`, close; connections handled sequentially in the accept loop. Coordinator: on submit → `scope.launch { if (!connect(addr)) session.connectFailed(addr) }`.

- [ ] **Step 1: Write failing tests** (`PairingServerTest.kt`)

```kotlin
package app.bowtie.core.pairing

import kotlinx.coroutines.*
import org.junit.Assert.*
import org.junit.Test
import java.net.Socket

class PairingServerTest {
    private fun exchange(port: Int, vararg parts: String, gapMs: Long = 100): String =
        Socket("127.0.0.1", port).use { sock ->
            parts.forEachIndexed { i, p -> if (i > 0) Thread.sleep(gapMs); sock.getOutputStream().write(p.toByteArray()); sock.getOutputStream().flush() }
            sock.getInputStream().readBytes().toString(Charsets.UTF_8)
        }

    @Test fun bodyInASecondWriteStillSubmits() {
        val t = "0123456789abcdef01234567"
        val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
        var got: String? = null
        val server = PairingServer(PairingSession(t)) { got = it }
        val port = server.start(scope)!!
        try {
            val body = "t=$t&server=bowtie.lan%3A8400"
            val reply = exchange(port, "POST /p HTTP/1.1\r\nContent-Length: ${body.length}\r\n\r\n", body)
            assertTrue(reply.startsWith("HTTP/1.1 303 See Other\r\n"))
            assertEquals("http://bowtie.lan:8400", got)
        } finally { server.stop(); scope.cancel() }
    }

    @Test fun coordinatorRedirectsOnlyForTheSubmittedServer() = runBlocking {
        val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
        val connected = mutableListOf<String>()
        val c = PairingCoordinator(scope, connect = { connected += it; true }, host = { "127.0.0.1" })
        c.start()
        val pair = withTimeout(2000) { while (c.pairUrl.value == null) delay(10); c.pairUrl.value!! }
        val port = pair.substringAfterLast(':').substringBefore('/').toInt()
        val token = pair.substringAfter("t=")
        val body = "t=$token&server=http%3A%2F%2Fbowtie.lan%3A8400"
        exchange(port, "POST /p HTTP/1.1\r\nContent-Length: ${body.length}\r\n\r\n$body")
        withTimeout(2000) { while (connected.isEmpty()) delay(10) }
        assertEquals(listOf("http://bowtie.lan:8400"), connected)
        c.codeIssued("http://elsewhere:1", "AAAA-BBBB")
        assertTrue(exchange(port, "GET /p/status?t=$token HTTP/1.1\r\n\r\n").contains("Connecting to your TV"))
        c.codeIssued("http://bowtie.lan:8400", "BCDF-2345")
        assertTrue(exchange(port, "GET /p/status?t=$token HTTP/1.1\r\n\r\n").contains("Location: http://bowtie.lan:8400/link?code=BCDF2345\r\n"))
        c.stop()
        assertNull(c.pairUrl.value)
        scope.cancel()
    }
}
```

- [ ] **Step 2: Run to verify failure** — `./gradlew :core:test --tests '*PairingServerTest*'` → compile failure.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `./gradlew :core:test` twice → PASS.
- [ ] **Step 5: Commit**

```bash
git add android/core
git commit -m "feat(android): pairing server (ServerSocket) and coordinator"
```

## Task 9: Android TV wiring

**Files:**
- Modify: `android/gradle/libs.versions.toml` (`zxing = "3.5.3"`, library `zxing-core = { module = "com.google.zxing:core", version.ref = "zxing" }`), `android/tv/build.gradle.kts` (`implementation(libs.zxing.core)`)
- Modify: `android/core/src/main/kotlin/app/bowtie/core/vm/AppViewModel.kt` (`val pairing by lazy { PairingCoordinator(workScope, connect = ::connect) }`; `connectTyped(raw)` stops pairing first; stop pairing in `completeSignIn` and successful `start()`)
- Create: `android/tv/src/main/kotlin/app/bowtie/tv/ui/PairingQr.kt` (`qrBitmap(text, sizePx): ImageBitmap` via `QRCodeWriter().encode(text, BarcodeFormat.QR_CODE, n, n, mapOf(EncodeHintType.ERROR_CORRECTION to ErrorCorrectionLevel.M, EncodeHintType.MARGIN to 2))` and `@Composable PairingQrPanel(url: String?)`)
- Modify: `android/tv/src/main/kotlin/app/bowtie/tv/ui/ConnectScreen.kt` (`LaunchedEffect(Unit) { appViewModel.pairing.start() }`; `Row` with `PairingQrPanel` beside the form; `submit()` uses `connectTyped`)
- Modify: `android/tv/src/main/kotlin/app/bowtie/tv/ui/LoginScreen.kt` (`LaunchedEffect(deviceState)`: on `Waiting` → `appViewModel.pairing.codeIssued(client.server.toString().trimEnd('/'), deviceState.userCode)`)
- Test: `android/core/src/test/kotlin/app/bowtie/core/vm/AppViewModelTest.kt` (add: `connectTyped` stops pairing — `pairing.pairUrl.value == null` after it)
- Test: `android/tv/src/test/kotlin/app/bowtie/tv/ui/PairingQrTest.kt` (zxing round-trip: encode then decode with `QRCodeReader` over a `RGBLuminanceSource` from the bit matrix → same text)

Panel copy per Global Constraints; QR 320.dp on white 352.dp box (`FilterQuality.None`), `contentDescription = "QR code to set up this TV from your phone"`. Check the actual property names first: `BowtieClient.server` (constructor param name `server: HttpUrl` — confirm it's a `val`; if private, add a public `val serverUrl: HttpUrl get() = server`) and `DeviceSignInViewModel.State.Waiting(userCode, link)`.

- [ ] **Step 1: Write the two failing tests** (AppViewModelTest addition and PairingQrTest):

```kotlin
// PairingQrTest.kt
package app.bowtie.tv.ui

import com.google.zxing.*
import com.google.zxing.common.HybridBinarizer
import com.google.zxing.qrcode.QRCodeReader
import com.google.zxing.qrcode.QRCodeWriter
import org.junit.Assert.assertEquals
import org.junit.Test

class PairingQrTest {
    @Test fun matrixScansBack() {
        val text = "http://255.255.255.255:65535/p?t=0123456789abcdef01234567"
        val m = pairingMatrix(text, 300)
        val px = IntArray(m.width * m.height) { i -> if (m.get(i % m.width, i / m.width)) 0xFF000000.toInt() else -1 }
        val bmp = BinaryBitmap(HybridBinarizer(RGBLuminanceSource(m.width, m.height, px)))
        assertEquals(text, QRCodeReader().decode(bmp).text)
    }
}
```
(`pairingMatrix(text, sizePx): BitMatrix` is the pure helper `qrBitmap` uses — put it in `PairingQr.kt`.)

```kotlin
// AppViewModelTest.kt — add
@Test fun connectTypedStopsPairing() = runTest {
    val vm = makeVmWithScope(this)
    vm.pairing.start()
    vm.connectTyped("http://127.0.0.1:1")
    advanceUntilIdle()
    assertNull(vm.pairing.pairUrl.value)
}
```
- [ ] **Step 2: Run to verify failure** — `./gradlew :core:test :tv:testDebugUnitTest` → compile failures.
- [ ] **Step 3: Implement** the changes above.
- [ ] **Step 4: Run** `./gradlew :core:test :app:testDebugUnitTest :tv:testDebugUnitTest :app:assembleDebug :tv:assembleDebug` → all PASS / build OK.
- [ ] **Step 5: Commit**

```bash
git add android
git commit -m "feat(androidtv): set up Android TV / Fire TV from a phone (pairing QR on Connect)"
```

## Task 10: Docs

**Files:**
- Modify: `CHANGELOG.md` (`## [Unreleased]` → `### Added` bullet: "TV setup from your phone: Apple TV, Android TV/Fire TV and Roku show a QR code on the Connect screen; scan it, type your server address on the phone, and sign in — no typing on the TV. Works on the same Wi-Fi; typing the address on the TV still works.")
- Modify: `docs/roadmap.md` (table row "Easy sign-in on a TV" → "Yes — scan a QR code with your phone, which also enters the server address")
- Modify: `docs/deploy/roku-testing.md` (pairing checklist + `npm run deploy` usage)
- Modify: `roku/README.md` (deploy script)

- [ ] **Step 1:** Edit the files.
- [ ] **Step 2: Commit**

```bash
git add CHANGELOG.md docs roku/README.md
git commit -m "docs: TV setup from your phone"
```

## Task 11: Whole-branch verification

- [ ] **Step 1:** `cd roku && npm test && npm run lint`
- [ ] **Step 2:** `make ios-test`
- [ ] **Step 3:** `cd android && ./gradlew :core:test :app:testDebugUnitTest :tv:testDebugUnitTest :app:assembleDebug :tv:assembleDebug`
- [ ] **Step 4:** `cd server && go test ./...` (OpenAPI coverage must still pass — no routes added).
- [ ] **Step 5:** `git status` clean apart from intended changes; restore `server/internal/web/dist/index.html` if any build touched it.
- [ ] **Step 6:** Open PR `feat/tv-pairing` → `main`; gate on `gh pr checks <N> --json bucket`.

## Task 12: Bedroom Roku end-to-end (needs the user)

- [ ] **Step 1:** Ask the user to confirm the bedroom Roku's IP and that it's free to use (and the dev password if not already provided by them). Never use the living-room Roku.
- [ ] **Step 2:** `cd roku && ROKU_IP=<ip> ROKU_DEV_PASSWORD=<pw> npm run deploy` (Bowtie must not be signed in on that Roku for Connect to show; if it is, use Settings → Change server).
- [ ] **Step 3:** Screenshot via `curl -s --digest -u rokudev:<pw> -F mysubmit=Screenshot http://<ip>/plugin_inspect` then fetch the image; confirm QR + caption render.
- [ ] **Step 4:** Decode the screenshot's QR (jsqr in node) to get the pair URL; from the Mac, `curl` the form, POST the production/local server address, follow `/p/status` until a 303 to `/link?code=…`. Then ask the user to approve on their phone (or scan for real) and confirm the Roku lands on Home.
- [ ] **Step 5:** Fix anything found (with tests), push, merge when CI green, release per the release lessons.
