// ApiTask's queue rules (bowtie.apiqueue), run under brs: a channel start
// can take up to a minute and ApiTask runs one request at a time, so a zap
// must not leave old starts queued, and a DELETE must not wait behind the
// next channel's start (the old viewer would hold a tuner and count against
// the account's stream limit while the new one starts).
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { runBrs } from './brs-run.mjs';

const LIB = ['source/lib/ApiQueue.brs'];
const task = readFileSync(new URL('../components/tasks/ApiTask.bs', import.meta.url), 'utf8');
const taskXml = readFileSync(new URL('../components/tasks/ApiTask.xml', import.meta.url), 'utf8');
const scene = readFileSync(new URL('../components/PlayerScene.bs', import.meta.url), 'utf8');
const body = (src, start) => {
    const at = src.indexOf(start);
    assert.ok(at >= 0, `${start} not found`);
    const rest = src.slice(at + start.length);
    return rest.slice(0, rest.search(/\n(end sub|end function)/));
};
const ids = (q) => q.map((r) => r.id);

test('a newer channel start replaces the starts still waiting', () => {
    const out = runBrs(LIB, `
        q = []
        add = bowtie_apiqueue_add
        add(q, { id: "player-create-1-1", kind: "createSession" })
        add(q, { id: "home-channels", kind: "channels" })
        d1 = add(q, { id: "player-create-2-2", kind: "createSession" })
        d2 = add(q, { id: "player-create-3-3", kind: "createSession" })
        out.q = snapshot(q)
        out.d1 = snapshot(d1)
        out.d2 = snapshot(d2)
        out.resp = bowtie_apiqueue_supersededResponse()
    `);
    assert.deepEqual(ids(out.q), ['home-channels', 'player-create-3-3']);
    assert.deepEqual(ids(out.d1), ['player-create-1-1']);
    assert.deepEqual(ids(out.d2), ['player-create-2-2']);
    assert.deepEqual(out.resp, { ok: false, error: { code: 0, kind: 'superseded', message: '' } });
});

test('a DELETE goes ahead of a waiting channel start; otherwise first in, first out', () => {
    const out = runBrs(LIB, `
        q = []
        add = bowtie_apiqueue_add
        add(q, { id: "hb", kind: "heartbeat" })
        add(q, { id: "player-create-2-5", kind: "createSession" })
        add(q, { id: "guide", kind: "guide" })
        add(q, { id: "player-delete-a", kind: "deleteSession" })
        add(q, { id: "player-delete-b", kind: "deleteSession" })
        out.q = snapshot(q)
        p = []
        add(p, { id: "x", kind: "channels" })
        add(p, { id: "player-delete-c", kind: "deleteSession" })
        out.p = snapshot(p)
    `);
    assert.deepEqual(ids(out.q), ['hb', 'player-delete-a', 'player-delete-b', 'player-create-2-5', 'guide']);
    assert.deepEqual(ids(out.p), ['x', 'player-delete-c']);
});

test('a cancel removes a waiting request by id', () => {
    const out = runBrs(LIB, `
        q = []
        bowtie_apiqueue_add(q, { id: "a", kind: "channels" })
        bowtie_apiqueue_add(q, { id: "player-create-1-1", kind: "createSession" })
        r = bowtie_apiqueue_removeId(q, "player-create-1-1")
        out.removed = r.id
        out.none = (bowtie_apiqueue_removeId(q, "nope") = invalid)
        out.q = snapshot(q)
    `);
    assert.equal(out.removed, 'player-create-1-1');
    assert.equal(out.none, true);
    assert.deepEqual(ids(out.q), ['a']);
});

test('ApiTask applies the rules and answers dropped requests "superseded"', () => {
    const ev = body(task, 'sub onTaskEvent(');
    assert.match(ev, /dropped = bowtie\.apiqueue\.add\(m\.queue, data\)/);
    assert.match(ev, /emitResponse\(bowtie\.apiqueue\.idOf\(item\), bowtie\.apiqueue\.supersededResponse\(\)\)/);
    assert.match(ev, /kindOf\(data\) = "createSession" and m\.currentKind = "createSession"\n\s*m\.cancelCurrent = true/);
    assert.match(ev, /else if field = "cancel"[^]*if data = m\.currentId\n\s*m\.cancelCurrent = true[^]*removeId\(m\.queue, data\)/);
    assert.match(taskXml, /<field id="cancel" type="string" alwaysNotify="true" \/>/);
    assert.match(body(task, 'sub taskRun()'), /observeField\("cancel", m\.port\)/);
});

test('an in-flight channel start is hung up on cancel (no waiting out 60 s)', () => {
    const http = body(task, 'function doHttp(');
    // Waits on the task port so requests and cancels arrive mid-transfer.
    assert.match(http, /httpPort = m\.port/);
    assert.match(http, /onTaskEvent\(msg\)\n\s*if m\.cancelCurrent = true and built\.cancellable = true\n\s*[^\n]*\n\s*[^\n]*\n\s*xfer\.AsyncCancel\(\)\n\s*return \{ httpCode: 0, body: "", cancelled: true \}/);
    // Only this transfer's answer counts.
    assert.match(http, /msg\.GetSourceIdentity\(\) = myId/);
    // A cancelled start is answered "superseded", never parsed as no answer.
    for (const at of ['sub handleRequest(', 'sub retryRequest(']) {
        assert.match(body(task, at), /if result\.cancelled = true\n\s*emitResponse\([^)]*, bowtie\.apiqueue\.supersededResponse\(\)\)\n\s*return/);
    }
});

test('the player cancels its pending start on zap and on leave', () => {
    assert.match(body(scene, 'sub scheduleReplace()'), /^\s*'[^\n]*\n\s*cancelPendingCreate\(\)\n\s*m\.gen = m\.gen \+ 1/);
    assert.match(body(scene, 'sub teardownSession('), /cancelPendingCreate\(\)\n\s*m\.pendingCreateId = ""/);
    assert.match(body(scene, 'sub cancelPendingCreate()'), /task\.cancel = m\.pendingCreateId/);
});
