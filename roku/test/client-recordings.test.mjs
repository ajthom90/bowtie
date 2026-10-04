// BowtieClient request builders / parsers for the DVR endpoints (tag dvr in
// docs/api/openapi.yaml), run under brs against source/lib/BowtieClient.brs.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { runBrs } from './brs-run.mjs';

const LIB = ['source/lib/BowtieClient.brs'];
const BASE = 'http://bowtie.local:8080/';
const API = 'http://bowtie.local:8080/api/v1';

// A string as a BrightScript string literal.
const lit = (s) => `"${s.replace(/"/g, '""')}"`;

// Builds a request into out[name] with its JSON body decoded in brs (a JSON
// string nested in the result does not survive the trip back to node).
const build = (name, kind, params) => `
        r = bowtie_client_buildRequest("${kind}", ${params}, "tok", "${BASE}")
        r.body = ParseJson(r.body)
        out.${name} = r`;

test('recordings → GET /recordings with an optional state filter', () => {
    const out = runBrs(LIB, `
        out.tab = bowtie_client_buildRequest("recordings", { state: "failed" }, "tok", "${BASE}")
        out.all = bowtie_client_buildRequest("recordings", {}, "tok", "${BASE}")
    `);
    assert.equal(out.tab.method, 'GET');
    assert.equal(out.tab.url, `${API}/recordings?state=failed`);
    assert.equal(out.tab.headers.Authorization, 'Bearer tok');
    assert.equal(out.all.url, `${API}/recordings`);
    assert.equal(out.tab.error, undefined);
});

test('createRecording → POST {channelId, programStart}, force only when set', () => {
    const out = runBrs(LIB, `
        ${build('plain', 'createRecording', '{ channelId: 3, programStart: "2026-10-05T00:00:00Z" }')}
        ${build('forced', 'createRecording', '{ channelId: 3, programStart: "2026-10-05T00:00:00Z", force: true }')}
    `);
    assert.equal(out.plain.method, 'POST');
    assert.equal(out.plain.url, `${API}/recordings`);
    assert.equal(out.plain.headers.Authorization, 'Bearer tok');
    assert.equal(out.plain.headers['Content-Type'], 'application/json');
    assert.deepEqual(out.plain.body, { channelId: 3, programStart: '2026-10-05T00:00:00Z' });
    assert.deepEqual(out.forced.body, { channelId: 3, programStart: '2026-10-05T00:00:00Z', force: true });
});

test('deleteRecording / stopRecording / playRecording hit the recording id', () => {
    const out = runBrs(LIB, `
        out.del = bowtie_client_buildRequest("deleteRecording", { id: 7 }, "tok", "${BASE}")
        out.stop = bowtie_client_buildRequest("stopRecording", { id: 7 }, "tok", "${BASE}")
        out.play = bowtie_client_buildRequest("playRecording", { id: 7 }, "tok", "${BASE}")
    `);
    assert.equal(out.del.method, 'DELETE');
    assert.equal(out.del.url, `${API}/recordings/7`);
    assert.equal(out.stop.method, 'POST');
    assert.equal(out.stop.url, `${API}/recordings/7/stop`);
    assert.equal(out.play.method, 'POST');
    assert.equal(out.play.url, `${API}/recordings/7/play`);
    for (const r of [out.del, out.stop, out.play]) assert.equal(r.headers.Authorization, 'Bearer tok');
});

test('setRecordingPosition → PUT {positionSec} as a whole number', () => {
    const out = runBrs(LIB, `
        ${build('req', 'setRecordingPosition', '{ id: 7, positionSec: 412.7 }')}
    `);
    assert.equal(out.req.method, 'PUT');
    assert.equal(out.req.url, `${API}/recordings/7/position`);
    assert.equal(out.req.headers['Content-Type'], 'application/json');
    assert.deepEqual(out.req.body, { positionSec: 412 });
});

test('protectRecording → PATCH {protected}', () => {
    const out = runBrs(LIB, `
        ${build('keep', 'protectRecording', '{ id: 7, keep: true }')}
        ${build('unkeep', 'protectRecording', '{ id: 7, keep: false }')}
    `);
    assert.equal(out.keep.method, 'PATCH');
    assert.equal(out.keep.url, `${API}/recordings/7`);
    assert.equal(out.keep.headers['Content-Type'], 'application/json');
    assert.deepEqual(out.keep.body, { protected: true });
    assert.deepEqual(out.unkeep.body, { protected: false });
});

test('recordings: 200 array is data; 404 (older server) is notFound', () => {
    const body = JSON.stringify([{ id: 7, title: 'Jeopardy!', state: 'ready' }]);
    const out = runBrs(LIB, `
        out.ok = bowtie_client_parseResponse("recordings", 200, ${lit(body)})
        out.old = bowtie_client_parseResponse("recordings", 404, "404 page not found")
    `);
    assert.equal(out.ok.ok, true);
    assert.equal(out.ok.data[0].id, 7);
    assert.equal(out.old.ok, false);
    assert.equal(out.old.error.kind, 'notFound');
});

test('createRecording: 201 → {recording, warnings}', () => {
    const body = JSON.stringify({
        recording: { id: 9, title: 'News', state: 'scheduled' },
        warnings: [{ code: 'usesAllTuners', message: 'If Plex is using a tuner then, this may not record.' }],
    });
    const out = runBrs(LIB, `
        out.r = bowtie_client_parseResponse("createRecording", 201, ${lit(body)})
    `);
    assert.equal(out.r.ok, true);
    assert.equal(out.r.data.recording.id, 9);
    assert.equal(out.r.data.warnings[0].code, 'usesAllTuners');
});

test('createRecording: 409 keeps the message, tunerCount and conflicts', () => {
    const body = JSON.stringify({
        error: 'Only 2 tuners: other recordings already need them then. Record anyway to try if one frees up.',
        tunerCount: 2,
        conflicts: [
            { id: 1, title: 'A', channelName: '5.1 KSTP', start: '2026-10-05T00:00:00Z', stop: '2026-10-05T00:30:00Z' },
            { id: 2, title: 'B', channelName: '9.1 FOX9', start: '2026-10-05T00:00:00Z', stop: '2026-10-05T01:00:00Z' },
        ],
    });
    const out = runBrs(LIB, `
        out.r = bowtie_client_parseResponse("createRecording", 409, ${lit(body)})
        out.junk = bowtie_client_parseResponse("createRecording", 409, "nope")
    `);
    assert.equal(out.r.ok, false);
    assert.equal(out.r.error.code, 409);
    assert.equal(out.r.error.kind, 'conflict');
    assert.match(out.r.error.message, /^Only 2 tuners/);
    assert.equal(out.r.error.tunercount, undefined, 'camelCase key lost its case');
    assert.equal(out.r.error.tunerCount, 2);
    assert.deepEqual(out.r.error.conflicts.map((c) => c.id), [1, 2]);
    // A 409 without the JSON shape still reads as a conflict with no list.
    assert.equal(out.junk.error.kind, 'conflict');
    assert.deepEqual(out.junk.error.conflicts, []);
    assert.equal(out.junk.error.tunerCount, 0);
});

test('createRecording: 503 (no DVR) and 404 (program gone) are plain errors', () => {
    const out = runBrs(LIB, `
        out.off = bowtie_client_parseResponse("createRecording", 503, "{""error"":""recording is not available""}")
        out.gone = bowtie_client_parseResponse("createRecording", 404, "{""error"":""program not found in the guide""}")
    `);
    assert.equal(out.off.ok, false);
    assert.equal(out.off.error.code, 503);
    assert.equal(out.off.error.message, 'recording is not available');
    assert.equal(out.gone.error.code, 404);
    assert.equal(out.gone.error.message, 'program not found in the guide');
});

test('204 kinds succeed with no body; 403/404 surface (never swallowed)', () => {
    const out = runBrs(LIB, `
        out.del = bowtie_client_parseResponse("deleteRecording", 204, "")
        out.stop = bowtie_client_parseResponse("stopRecording", 204, "")
        out.pos = bowtie_client_parseResponse("setRecordingPosition", 204, "")
        out.forbidden = bowtie_client_parseResponse("deleteRecording", 403, "{""error"":""forbidden""}")
        out.missing = bowtie_client_parseResponse("stopRecording", 404, "{""error"":""recording not found""}")
    `);
    assert.equal(out.del.ok, true);
    assert.equal(out.stop.ok, true);
    assert.equal(out.pos.ok, true);
    assert.equal(out.forbidden.ok, false);
    assert.equal(out.forbidden.error.code, 403);
    assert.equal(out.forbidden.error.message, 'forbidden');
    assert.equal(out.missing.ok, false);
    assert.equal(out.missing.error.code, 404);
});

test('protectRecording: 200 returns the updated recording', () => {
    const out = runBrs(LIB, `
        out.r = bowtie_client_parseResponse("protectRecording", 200, "{""id"":7,""protected"":true}")
    `);
    assert.equal(out.r.ok, true);
    assert.equal(out.r.data.id, 7);
    assert.equal(out.r.data.protected, true);
});

test('playRecording: server-relative playlistUrl resolves against the base; 409 = not ready', () => {
    const body = JSON.stringify({ playlistUrl: '/api/v1/recordings/7/hls/index.m3u8?token=abc.def', positionSec: 412, durationSec: 1980 });
    const out = runBrs(LIB, `
        out.r = bowtie_client_parseResponse("playRecording", 200, ${lit(body)}, "${BASE}")
        out.busy = bowtie_client_parseResponse("playRecording", 409, "{""error"":""this recording isn't ready to play yet""}", "${BASE}")
    `);
    assert.equal(out.r.ok, true);
    assert.equal(out.r.data.playlistUrl, `${API}/recordings/7/hls/index.m3u8?token=abc.def`);
    assert.equal(out.r.data.positionSec, 412);
    assert.equal(out.r.data.durationSec, 1980);
    assert.equal(out.busy.ok, false);
    assert.equal(out.busy.error.code, 409);
    assert.equal(out.busy.error.message, "this recording isn't ready to play yet");
});

// ── Series recording rules ──────────────────────────────────────────────────

test('recordingRules → GET /recording-rules; createRecordingRule → POST {channelId, programStart}', () => {
    const out = runBrs(LIB, `
        out.list = bowtie_client_buildRequest("recordingRules", {}, "tok", "${BASE}")
        ${build('create', 'createRecordingRule', '{ channelId: 3, programStart: "2026-10-05T00:00:00Z" }')}
        out.del = bowtie_client_buildRequest("deleteRecordingRule", { id: 5 }, "tok", "${BASE}")
    `);
    assert.equal(out.list.method, 'GET');
    assert.equal(out.list.url, `${API}/recording-rules`);
    assert.equal(out.list.headers.Authorization, 'Bearer tok');
    assert.equal(out.create.method, 'POST');
    assert.equal(out.create.url, `${API}/recording-rules`);
    assert.equal(out.create.headers.Authorization, 'Bearer tok');
    assert.equal(out.create.headers['Content-Type'], 'application/json');
    // This channel, new episodes only: the server's defaults.
    assert.deepEqual(out.create.body, { channelId: 3, programStart: '2026-10-05T00:00:00Z' });
    assert.equal(out.del.method, 'DELETE');
    assert.equal(out.del.url, `${API}/recording-rules/5`);
    assert.equal(out.del.headers.Authorization, 'Bearer tok');
});

test('recording rules: 200 list, 201 {rule, scheduled}, 204 delete, 403 surfaces', () => {
    const rule = { id: 5, title: 'Jeopardy!', seriesId: 'SH01', channelId: 3, channelName: '5.1 KSTP', newOnly: true, keepLatest: 0, scheduledBy: 'andrew', canManage: true, createdAt: '2026-10-04T00:00:00Z' };
    const out = runBrs(LIB, `
        out.list = bowtie_client_parseResponse("recordingRules", 200, ${lit(JSON.stringify([rule]))})
        out.created = bowtie_client_parseResponse("createRecordingRule", 201, ${lit(JSON.stringify({ rule, scheduled: 3 }))})
        out.del = bowtie_client_parseResponse("deleteRecordingRule", 204, "")
        out.forbidden = bowtie_client_parseResponse("deleteRecordingRule", 403, "{""error"":""only the person who set it up (or an admin) can remove this rule""}")
        out.gone = bowtie_client_parseResponse("createRecordingRule", 404, "{""error"":""program not found in the guide""}")
    `);
    assert.equal(out.list.ok, true);
    assert.equal(out.list.data[0].title, 'Jeopardy!');
    assert.equal(out.created.ok, true);
    assert.equal(out.created.data.scheduled, 3);
    assert.equal(out.created.data.rule.id, 5);
    assert.equal(out.del.ok, true);
    assert.equal(out.forbidden.ok, false);
    assert.equal(out.forbidden.error.code, 403);
    assert.equal(out.forbidden.error.message, 'only the person who set it up (or an admin) can remove this rule');
    assert.equal(out.gone.error.code, 404);
});
