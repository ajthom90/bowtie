// Quick sign-in ("Sign in with your phone"), run under brs: the request
// kinds and parsers in source/lib/BowtieClient.brs and the pure poll / screen
// decisions in source/lib/DeviceAuth.brs (POST /auth/device, then poll
// /auth/device/token: 428 pending, 200 tokens, 410 expired).
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { runBrs } from './brs-run.mjs';

const CLIENT = ['source/lib/BowtieClient.brs'];
const LIB = ['source/lib/BowtieClient.brs', 'source/lib/DeviceAuth.brs'];
const BASE = 'http://bowtie.local:8080/';
const API = 'http://bowtie.local:8080/api/v1';

// A JSON value as a BrightScript expression.
const bs = (v) => `ParseJson("${JSON.stringify(v).replace(/"/g, '""')}")`;
// A string as a BrightScript string literal.
const lit = (s) => `"${s.replace(/"/g, '""')}"`;

const START = {
    deviceCode: 'dc-secret',
    userCode: 'BCDF-2345',
    verifyUrl: 'http://bowtie.local:8080/link?code=BCDF-2345',
    qrUrl: '/api/v1/auth/device/qr/BCDF-2345.png',
    expiresIn: 600,
    interval: 5,
};

const TOKENS = {
    accessToken: 'acc',
    refreshToken: 'ref',
    user: { id: 1, username: 'andrew', role: 'admin', maxQuality: 'high' },
};

// ── Requests ────────────────────────────────────────────────────────────────

test('deviceStart → POST /auth/device {deviceName}, no Bearer', () => {
    const out = runBrs(CLIENT, `
        r = bowtie_client_buildRequest("deviceStart", { deviceName: "Living Room Roku" }, "tok", "${BASE}")
        r.body = ParseJson(r.body)
        out.req = r
    `);
    assert.equal(out.req.method, 'POST');
    assert.equal(out.req.url, `${API}/auth/device`);
    assert.equal(out.req.headers.Authorization, undefined);
    assert.equal(out.req.headers['Content-Type'], 'application/json');
    assert.deepEqual(out.req.body, { deviceName: 'Living Room Roku' });
    assert.equal(out.req.error, undefined);
});

test('deviceToken → POST /auth/device/token {deviceCode}, no Bearer', () => {
    const out = runBrs(CLIENT, `
        r = bowtie_client_buildRequest("deviceToken", { deviceCode: "dc-secret" }, "tok", "${BASE}")
        r.body = ParseJson(r.body)
        out.req = r
    `);
    assert.equal(out.req.method, 'POST');
    assert.equal(out.req.url, `${API}/auth/device/token`);
    assert.equal(out.req.headers.Authorization, undefined);
    assert.deepEqual(out.req.body, { deviceCode: 'dc-secret' });
});

// ── Responses ───────────────────────────────────────────────────────────────

test('deviceStart 200: codes, with qrUrl resolved against the server', () => {
    const out = runBrs(CLIENT, `
        out.r = bowtie_client_parseResponse("deviceStart", 200, ${lit(JSON.stringify(START))}, "${BASE}")
    `);
    assert.equal(out.r.ok, true);
    assert.deepEqual(out.r.data, {
        deviceCode: 'dc-secret',
        userCode: 'BCDF-2345',
        verifyUrl: 'http://bowtie.local:8080/link?code=BCDF-2345',
        qrUrl: 'http://bowtie.local:8080/api/v1/auth/device/qr/BCDF-2345.png',
        expiresIn: 600,
        interval: 5,
    });
});

test('deviceStart: 429 too many, 404 older server', () => {
    const out = runBrs(CLIENT, `
        out.busy = bowtie_client_parseResponse("deviceStart", 429, "{""error"":""too many sign-ins in progress; try again shortly""}", "${BASE}")
        out.old = bowtie_client_parseResponse("deviceStart", 404, "404 page not found", "${BASE}")
        out.bad = bowtie_client_parseResponse("deviceStart", 200, "not json", "${BASE}")
    `);
    assert.equal(out.busy.ok, false);
    assert.equal(out.busy.error.code, 429);
    assert.equal(out.old.ok, false);
    assert.equal(out.old.error.code, 404);
    assert.equal(out.bad.ok, false);
});

test('deviceToken: 428 pending, 410 expired, 200 is a TokenPair like login', () => {
    const out = runBrs(CLIENT, `
        out.pending = bowtie_client_parseResponse("deviceToken", 428, "{""error"":""authorization_pending""}", "${BASE}")
        out.expired = bowtie_client_parseResponse("deviceToken", 410, "{""error"":""expired_token""}", "${BASE}")
        out.ok = bowtie_client_parseResponse("deviceToken", 200, ${lit(JSON.stringify(TOKENS))}, "${BASE}")
        out.login = bowtie_client_parseResponse("login", 200, ${lit(JSON.stringify(TOKENS))}, "${BASE}")
    `);
    assert.equal(out.pending.ok, false);
    assert.equal(out.pending.error.code, 428);
    assert.equal(out.pending.error.kind, 'pending');
    assert.equal(out.expired.ok, false);
    assert.equal(out.expired.error.code, 410);
    assert.equal(out.expired.error.kind, 'expired');
    assert.equal(out.ok.ok, true);
    assert.deepEqual(out.ok.data, out.login.data);
    // Key case differs between brs and the device; ApiTask reads it either way.
    assert.equal(out.ok.data.accessToken ?? out.ok.data.accesstoken, 'acc');
});

// ── Pure decisions ──────────────────────────────────────────────────────────

test('deviceName: the Roku friendly name, else "Roku"', () => {
    const out = runBrs(LIB, `
        n = bowtie_deviceauth_deviceName
        out.t = [n("  Living Room Roku "), n(""), n(invalid), n("   ")]
    `);
    assert.deepEqual(out.t, ['Living Room Roku', 'Roku', 'Roku', 'Roku']);
});

test('startOutcome: show the code, fall back to password on an older server, else retry', () => {
    const out = runBrs(LIB, `
        s = bowtie_deviceauth_startOutcome
        out.show = s({ ok: true, data: ${bs(START)} })
        out.old = s({ ok: false, error: { code: 404, kind: "notFound", message: "404 page not found" } })
        out.busy = s({ ok: false, error: { code: 429, kind: "server", message: "too many sign-ins in progress; try again shortly" } })
        out.offline = s({ ok: false, error: { code: 0, kind: "server", message: "" } })
        out.empty = s({ ok: true, data: { deviceCode: "" } })
        out.none = s(invalid)
    `);
    assert.equal(out.show.action, 'show');
    assert.equal(out.old.action, 'password');
    assert.equal(out.old.message, "This server doesn't support phone sign-in yet. Sign in with your password.");
    assert.equal(out.busy.action, 'retry');
    assert.equal(out.busy.message, 'Too many sign-ins are in progress. Try again in a minute.');
    assert.equal(out.offline.action, 'retry');
    assert.equal(out.offline.message, "Couldn't reach the server.");
    assert.equal(out.empty.action, 'retry');
    assert.equal(out.empty.message, "Couldn't get a sign-in code.");
    assert.equal(out.none.action, 'retry');
});

test('newSession: QR address, link prompt, poll interval and local expiry', () => {
    const out = runBrs(LIB, `
        data = bowtie_client_parseResponse("deviceStart", 200, ${lit(JSON.stringify(START))}, "${BASE}").data
        out.s = bowtie_deviceauth_newSession(data, "${BASE}", 1000)
        out.slow = bowtie_deviceauth_newSession({ deviceCode: "d", userCode: "U", qrUrl: "/q.png", expiresIn: 0, interval: 0 }, "${BASE}", 1000)
    `);
    assert.equal(out.s.deviceCode, 'dc-secret');
    assert.equal(out.s.userCode, 'BCDF-2345');
    assert.equal(out.s.qrUri, 'http://bowtie.local:8080/api/v1/auth/device/qr/BCDF-2345.png');
    assert.equal(out.s.linkText, 'Or go to http://bowtie.local:8080/link and enter');
    assert.equal(out.s.intervalSec, 5);
    assert.equal(out.s.expiresAtSec, 1600);
    // Missing interval / expiry: poll every 5 s, give up after 10 minutes.
    assert.equal(out.slow.intervalSec, 5);
    assert.equal(out.slow.expiresAtSec, 1600);
    assert.equal(out.slow.qrUri, 'http://bowtie.local:8080/q.png');
});

test('linkText: the server address without a trailing slash', () => {
    const out = runBrs(LIB, `
        out.t = [bowtie_deviceauth_linkText("http://10.0.0.5:8080"), bowtie_deviceauth_linkText("https://tv.example.com/")]
    `);
    assert.deepEqual(out.t, [
        'Or go to http://10.0.0.5:8080/link and enter',
        'Or go to https://tv.example.com/link and enter',
    ]);
});

test('pollOutcome: 200 signs in, 428 waits, 410 expires, network trouble keeps polling', () => {
    const out = runBrs(LIB, `
        p = bowtie_deviceauth_pollOutcome
        out.ok = p({ ok: true, data: ${bs(TOKENS)} })
        out.pending = p({ ok: false, error: { code: 428, kind: "pending", message: "authorization_pending" } })
        out.expired = p({ ok: false, error: { code: 410, kind: "expired", message: "expired_token" } })
        out.offline = p({ ok: false, error: { code: 0, kind: "server", message: "" } })
        out.down = p({ ok: false, error: { code: 502, kind: "server", message: "bad gateway" } })
        out.slow = p({ ok: false, error: { code: 429, kind: "server", message: "slow down" } })
        out.bad = p({ ok: false, error: { code: 400, kind: "server", message: "invalid request body" } })
        out.none = p(invalid)
    `);
    assert.equal(out.ok.action, 'signedIn');
    assert.equal(out.pending.action, 'wait');
    assert.equal(out.pending.message, 'Waiting for you to approve it on your phone…');
    assert.equal(out.expired.action, 'expired');
    assert.equal(out.expired.message, 'Code expired');
    assert.equal(out.offline.action, 'wait');
    assert.equal(out.offline.message, "Can't reach the server. Still trying…");
    assert.equal(out.down.action, 'wait');
    assert.equal(out.slow.action, 'wait');
    assert.equal(out.bad.action, 'expired');
    assert.equal(out.none.action, 'wait');
});

test('isExpired: the code lapses at expiresAtSec even if the server never says 410', () => {
    const out = runBrs(LIB, `
        s = { "expiresAtSec": 1600 }
        out.t = [bowtie_deviceauth_isExpired(s, 1599), bowtie_deviceauth_isExpired(s, 1600), bowtie_deviceauth_isExpired(invalid, 0)]
    `);
    assert.deepEqual(out.t, [false, true, true]);
});
