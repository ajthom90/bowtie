// BowtieClient: the heartbeat's antenna report (?signal=1) and the words a
// viewer sees for a failed request, run under brs against the transpiled
// source/lib/BowtieClient.brs.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { runBrs } from './brs-run.mjs';

const LIB = ['source/lib/BowtieClient.brs'];
const BASE = 'http://bowtie.local:8080/';

// A string as a BrightScript string literal.
const lit = (s) => `"${s.replace(/"/g, '""')}"`;

const CANT_REACH = "Can't reach your Bowtie server. Check your connection and try again.";

// ── Heartbeat ───────────────────────────────────────────────────────────────

test('heartbeat asks for the signal report, stream token only (no Bearer)', () => {
    const out = runBrs(LIB, `
        out.req = bowtie_client_buildRequest("heartbeat", { viewerId: "v-1", token: "tok" }, "access", "${BASE}")
    `);
    assert.equal(out.req.method, 'POST');
    assert.equal(out.req.url, 'http://bowtie.local:8080/api/v1/sessions/v-1/heartbeat?signal=1&token=tok');
    assert.equal(out.req.headers.Authorization, undefined);
});

test('heartbeat 200 carries the reception; weak is read as given', () => {
    const weak = JSON.stringify({ signal: { strength: 96, quality: 46, symbolQuality: 0, weak: true } });
    const fine = JSON.stringify({ signal: { strength: 100, quality: 90, symbolQuality: 100, weak: false } });
    const out = runBrs(LIB, `
        out.weak = bowtie_client_parseResponse("heartbeat", 200, ${lit(weak)}, "${BASE}")
        out.fine = bowtie_client_parseResponse("heartbeat", 200, ${lit(fine)}, "${BASE}")
        out.bare = bowtie_client_parseResponse("heartbeat", 200, ${lit('{"signal":{"weak":true,"quality":"46"}}')}, "${BASE}")
    `);
    // A missing or non-number reading is left out (not shown as 0%).
    assert.deepEqual(out.bare.data, { known: true, weak: true });
    assert.equal(out.weak.ok, true);
    assert.deepEqual(out.weak.data, { known: true, weak: true, strength: 96, quality: 46, symbolQuality: 0 });
    assert.equal(out.fine.ok, true);
    assert.equal(out.fine.data.known, true);
    assert.equal(out.fine.data.weak, false);
});

test('heartbeat: 204 (older server), null signal or an odd body are success with reception unknown', () => {
    const out = runBrs(LIB, `
        out.old = bowtie_client_parseResponse("heartbeat", 204, "", "${BASE}")
        out.nul = bowtie_client_parseResponse("heartbeat", 200, ${lit('{"signal":null}')}, "${BASE}")
        out.junk = bowtie_client_parseResponse("heartbeat", 200, "not json", "${BASE}")
        out.str = bowtie_client_parseResponse("heartbeat", 200, ${lit('{"signal":{"weak":"yes"}}')}, "${BASE}")
        out.acc = bowtie_client_parseResponse("heartbeat", 202, "", "${BASE}")
    `);
    for (const k of ['old', 'nul', 'junk', 'str', 'acc']) {
        assert.equal(out[k].ok, true, k);
        assert.equal(out[k].data.known, false, k);
        assert.equal(out[k].data.weak, false, k);
    }
});

test('heartbeat errors are still errors (parental 403 keeps its kind)', () => {
    const blocked = JSON.stringify({ error: 'Blocked by parental controls', code: 'parental' });
    const out = runBrs(LIB, `
        out.p = bowtie_client_parseResponse("heartbeat", 403, ${lit(blocked)}, "${BASE}")
        out.gone = bowtie_client_parseResponse("heartbeat", 404, "", "${BASE}")
    `);
    assert.equal(out.p.ok, false);
    assert.equal(out.p.error.kind, 'parental');
    assert.equal(out.gone.ok, false);
    assert.equal(out.gone.error.code, 404);
});

// ── Plain-words errors ──────────────────────────────────────────────────────

test('a body that is not the server\'s JSON error never becomes the message', () => {
    const out = runBrs(LIB, `
        out.html = bowtie_client_parseResponse("channels", 502, "<html><body>502 Bad Gateway</body></html>", "${BASE}")
        out.go404 = bowtie_client_parseResponse("recordings", 404, "404 page not found", "${BASE}")
        out.server = bowtie_client_parseResponse("channels", 500, ${lit('{"error":"The guide is still loading."}')}, "${BASE}")
        out.obj = bowtie_client_parseResponse("channels", 500, ${lit('{"error":{"detail":"x"}}')}, "${BASE}")
    `);
    assert.equal(out.html.error.message, '');
    assert.equal(out.go404.error.message, '');
    assert.equal(out.go404.error.kind, 'notFound');
    assert.equal(out.server.error.message, 'The guide is still loading.');
    assert.equal(out.obj.error.message, '');
});

test('viewerMessage: no answer → can\'t reach; server words as-is; anything else → the fallback', () => {
    const out = runBrs(LIB, `
        v = bowtie_client_viewerMessage
        fb = "Couldn't load channels."
        out.t = [
            v({ code: 0, kind: "server", message: "" }, fb),
            v({ code: 500, kind: "server", message: "The guide is still loading." }, fb),
            v({ code: 502, kind: "server", message: "" }, fb),
            v({ code: 200, message: "invalid json" }, fb),
            v({ code: 0, kind: "build", message: "unknown kind: x" }, fb),
            v(invalid, fb),
            v({ code: 404, kind: "notFound", message: "  " }, fb),
        ]
        out.cant = bowtie_client_cantReachText()
        out.wrong = bowtie_client_somethingWrongText()
        out.stopped = bowtie_client_streamStoppedText()
    `);
    assert.deepEqual(out.t, [
        CANT_REACH,
        'The guide is still loading.',
        "Couldn't load channels.",
        "Couldn't load channels.",
        "Couldn't load channels.",
        "Couldn't load channels.",
        "Couldn't load channels.",
    ]);
    assert.equal(out.cant, CANT_REACH);
    assert.equal(out.wrong, 'Something went wrong. Try again.');
    assert.equal(out.stopped, 'The stream stopped. Try again.');
});
