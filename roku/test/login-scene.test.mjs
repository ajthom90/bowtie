// Scene wiring for quick sign-in. The poll and response decisions run under
// brs (device-auth.test.mjs); this pins what can't run off-device: the
// Login screen opens on "Sign in with your phone" with a large QR, polls
// through ApiTask on a Timer, and ApiTask completes a device sign-in exactly
// like a password login.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

const read = (p) => readFileSync(new URL(`../components/${p}`, import.meta.url), 'utf8');
const scene = read('LoginScene.bs');
const xml = read('LoginScene.xml');
const apiTask = read('tasks/ApiTask.bs');

function body(src, signature) {
    const start = src.indexOf(signature);
    assert.ok(start >= 0, `missing ${signature}`);
    const end = src.indexOf(signature.startsWith('function') ? 'end function' : 'end sub', start);
    return src.slice(start, end);
}

function tag(src, id) {
    const at = src.indexOf(`id="${id}"`);
    assert.ok(at >= 0, `no element ${id}`);
    return src.slice(src.lastIndexOf('<', at), src.indexOf('>', at));
}

// ── Layout ──────────────────────────────────────────────────────────────────

test('phone sign-in: a large QR Poster, the link prompt, the code and a status line', () => {
    const qr = tag(xml, 'qrPoster');
    assert.match(qr, /^<Poster/);
    // The server draws a 512 px PNG: show it 1:1 so the modules stay crisp.
    assert.match(qr, /width="512"/);
    assert.match(qr, /height="512"/);
    for (const id of ['linkLabel', 'codeLabel', 'statusLabel']) assert.match(tag(xml, id), /^<Label/);
    assert.match(tag(xml, 'pollTimer'), /^<Timer/);
    assert.match(tag(xml, 'pollTimer'), /repeat="false"/);
});

test('phone sign-in buttons: Get a new code, Use a password instead, Change server', () => {
    assert.match(tag(xml, 'quickButtons'), /^<ButtonGroup/);
    // Set from code: string arrays in XML attributes are easy to get wrong.
    assert.match(body(scene, 'sub init()'), /m\.quickButtons\.buttons = \["Get a new code", "Use a password instead", "Change server"\]/);
});

test('password sign-in stays, with a way back to the phone', () => {
    for (const id of ['passwordGroup', 'usernameButton', 'passwordButton', 'signInButton', 'changeServerButton', 'phoneButton']) {
        tag(xml, id);
    }
    assert.match(tag(xml, 'phoneButton'), /text="Sign in with your phone"/);
    assert.match(tag(xml, 'quickGroup'), /visible="false"/);
    assert.match(tag(xml, 'passwordGroup'), /visible="false"/);
});

// ── Behavior ────────────────────────────────────────────────────────────────

test('showing Login starts phone sign-in (the default), not the password keyboard', () => {
    const show = body(scene, 'sub onVisible()');
    assert.match(show, /showQuick\(\)/);
    assert.doesNotMatch(show, /openUsernameKeyboard|onOpenUsernameTimer/);
    // Hiding stops polling.
    assert.match(show, /stopQuick\(\)/);
});

test('phone sign-in asks for a code named after this Roku', () => {
    const start = body(scene, 'sub requestCode()');
    assert.match(start, /kind: "deviceStart"/);
    assert.match(start, /bowtie\.deviceauth\.deviceName\(/);
    assert.match(scene, /CreateObject\("roDeviceInfo"\)/);
    assert.match(scene, /\.GetFriendlyName\(\)/);
});

test('the code response shows the QR from the server and starts the poll timer', () => {
    const handle = body(scene, 'sub handleStartResponse(');
    assert.match(handle, /bowtie\.deviceauth\.startOutcome\(/);
    assert.match(handle, /bowtie\.deviceauth\.newSession\(/);
    assert.match(handle, /m\.qrPoster\.uri = /);
    assert.match(handle, /m\.codeLabel\.text = /);
    assert.match(handle, /schedulePoll\(\)/);
    const schedule = body(scene, 'sub schedulePoll()');
    assert.match(schedule, /m\.pollTimer\.duration = m\.session\.intervalSec/);
    assert.match(schedule, /m\.pollTimer\.control = "start"/);
});

test('each poll posts deviceToken through ApiTask; a lapsed code stops polling', () => {
    const fire = body(scene, 'sub onPollTimer()');
    assert.match(fire, /bowtie\.deviceauth\.isExpired\(/);
    assert.match(fire, /showExpired\(\)/);
    assert.match(fire, /kind: "deviceToken"/);
    assert.match(fire, /deviceCode: m\.session\.deviceCode/);
});

test('poll results: signed in like a password login, wait again, or Code expired + Get a new code', () => {
    const handle = body(scene, 'sub handlePollResponse(');
    assert.match(handle, /bowtie\.deviceauth\.pollOutcome\(/);
    assert.match(handle, /"signedIn"[\s\S]*?m\.top\.signedIn = true/);
    assert.match(handle, /"wait"[\s\S]*?schedulePoll\(\)/);
    assert.match(handle, /"expired"[\s\S]*?showExpired\(\)/);
    const expired = body(scene, 'sub showExpired()');
    // expiredText() is "Code expired" (device-auth.test.mjs).
    assert.match(expired, /m\.statusLabel\.text = bowtie\.deviceauth\.expiredText\(\)/);
    // The dead QR is hidden so nobody scans it.
    assert.match(expired, /clearCode\(\)/);
    assert.match(expired, /m\.pollTimer\.control = "stop"/);
    assert.match(expired, /m\.quickButtons\.focusButton = 0/);
});

test('ApiTask completes a device sign-in exactly as login, and never refreshes on its 401', () => {
    const handle = body(apiTask, 'sub handleRequest(');
    assert.match(handle, /\(kind = "login" or kind = "deviceToken"\) and parsed\.ok = true[\s\S]*?applyLoginOk\(parsed\.data\)/);
    assert.match(handle, /kind <> "deviceStart" and kind <> "deviceToken"/);
});
