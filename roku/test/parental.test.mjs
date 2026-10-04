// Parental controls on Roku. The server answers 403 {"error", "code":
// "parental"} when a channel, the program on now, or a recording is blocked
// for the caller (session create, recording play, and a live viewer's
// heartbeat once it is stopped), and marks blocked recordings `locked`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { runBrs } from './brs-run.mjs';

const CLIENT = ['source/lib/BowtieClient.brs'];
const RECORDINGS = ['source/lib/Recordings.brs'];
const BASE = 'http://bowtie.local:8080/';

// A JSON value as a BrightScript expression.
const bs = (v) => `ParseJson("${JSON.stringify(v).replace(/"/g, '""')}")`;
// A string as a BrightScript string literal.
const lit = (s) => `"${s.replace(/"/g, '""')}"`;

const BLOCKED = JSON.stringify({ error: 'Blocked by parental controls (rated TV-MA)', code: 'parental' });

const read = (p) => readFileSync(new URL(`../components/${p}`, import.meta.url), 'utf8');
function body(src, signature) {
    const start = src.indexOf(signature);
    assert.ok(start >= 0, `missing ${signature}`);
    const end = src.indexOf(signature.startsWith('function') ? 'end function' : 'end sub', start);
    return src.slice(start, end);
}

const rec = (over = {}) => ({
    id: 7,
    title: 'Late Movie',
    subtitle: '',
    channelId: 3,
    channelName: '5.1 KSTP',
    start: '2026-10-05T00:00:00Z',
    stop: '2026-10-05T02:00:00Z',
    state: 'ready',
    partial: false,
    failure: '',
    failureDetail: '',
    durationSec: 7200,
    sizeBytes: 3800000000,
    protected: false,
    positionSec: 0,
    scheduledBy: 'andrew',
    canManage: true,
    ...over,
});

// ── BowtieClient ────────────────────────────────────────────────────────────

test('403 code "parental" becomes kind "parental" with the server message', () => {
    const out = runBrs(CLIENT, `
        out.create = bowtie_client_parseResponse("createSession", 403, ${lit(BLOCKED)}, "${BASE}")
        out.play = bowtie_client_parseResponse("playRecording", 403, ${lit(BLOCKED)}, "${BASE}")
        out.beat = bowtie_client_parseResponse("heartbeat", 403, ${lit(BLOCKED)}, "${BASE}")
        out.other = bowtie_client_parseResponse("createSession", 403, "{""error"":""forbidden""}", "${BASE}")
    `);
    for (const r of [out.create, out.play, out.beat]) {
        assert.equal(r.ok, false);
        assert.equal(r.error.code, 403);
        assert.equal(r.error.kind, 'parental');
        assert.equal(r.error.message, 'Blocked by parental controls (rated TV-MA)');
    }
    assert.equal(out.other.error.kind, 'server');
    assert.equal(out.other.error.message, 'forbidden');
});

test('isParental: only a parental block', () => {
    const out = runBrs(CLIENT, `
        p = bowtie_client_isParental
        out.t = [p({ code: 403, kind: "parental", message: "x" }), p({ code: 403, kind: "server" }), p({ code: 503 }), p(invalid)]
    `);
    assert.deepEqual(out.t, [true, false, false, false]);
});

// ── Recordings ──────────────────────────────────────────────────────────────

test('locked recordings say Locked and cannot play', () => {
    const now = '2026-10-04T15:00:00Z';
    const off = -5 * 3600;
    const out = runBrs(RECORDINGS, `
        l = bowtie_recordings_parseList(${bs([rec({ locked: true }), rec({ id: 8 })])})
        out.flags = [l[0].locked, l[1].locked]
        out.status = bowtie_recordings_statusText(l[0])
        out.sched = bowtie_recordings_statusText(${bs(rec({ state: 'scheduled', locked: true }))})
        out.line = bowtie_recordings_detailLine(l[0], "${now}", ${off})
        out.play = [bowtie_recordings_rowActions(l[0]).play, bowtie_recordings_rowActions(l[1]).play]
        out.menu = bowtie_recordings_optionButtons(l[0])
        out.text = bowtie_recordings_lockedText()
    `);
    assert.deepEqual(out.flags, [true, false]);
    assert.equal(out.status, 'Locked');
    assert.equal(out.sched, 'Scheduled · Locked');
    assert.equal(out.line, 'Today 7:00–9:00 PM · 5.1 KSTP · 2 h · 3.8 GB · Locked');
    assert.deepEqual(out.play, [false, true]);
    // Still manageable (Keep / Delete) by whoever scheduled it.
    assert.deepEqual(out.menu.map((b) => b.id), ['keep', 'delete', 'close']);
    assert.equal(out.text, "Locked by parental controls — it can't be played on this account.");
});

// ── Scenes ──────────────────────────────────────────────────────────────────

test('PlayerScene: a parental 403 from session start shows its message, no retry loop', () => {
    const player = read('PlayerScene.bs');
    const handle = body(player, 'sub handleCreateError(');
    const at = handle.indexOf('code = 403 and bowtie.client.isParental(err)');
    assert.ok(at >= 0, 'no parental case');
    // Before the generic fallback, and it shows the server's message.
    assert.ok(at < handle.indexOf('msg = PLAYBACK_FAILED'));
    assert.match(handle.slice(at), /showFailed\(parentalTitle\(err\), PARENTAL_DETAIL\)/);
    assert.match(body(player, 'function parentalTitle('), /err\.message/);
});

test('PlayerScene: a parental 403 on the heartbeat stops the live viewer and says why', () => {
    const player = read('PlayerScene.bs');
    const resp = body(player, 'sub onApiResponse()');
    assert.match(resp, /m\.pendingHeartbeatPrefix[\s\S]*?bowtie\.client\.isParental\(resp\.error\)[\s\S]*?handleParentalStop\(resp\.error\)/);
    const stop = body(player, 'sub handleParentalStop(');
    assert.match(stop, /stopHeartbeat\(\)/);
    assert.match(stop, /showFailed\(parentalTitle\(err\), PARENTAL_DETAIL\)/);
    assert.match(player, /import "pkg:\/source\/lib\/BowtieClient\.bs"/);
});

test('RecordingsScene: a locked recording says why instead of playing', () => {
    const scene = read('RecordingsScene.bs');
    const open = body(scene, 'sub openOptions(');
    assert.match(open, /r\.locked = true[\s\S]*?bowtie\.recordings\.lockedText\(\)/);
    // A play that is refused anyway (locked since the list loaded) shows the server's words.
    assert.match(body(scene, 'sub handlePlayResponse('), /resp\.error\.message/);
});
