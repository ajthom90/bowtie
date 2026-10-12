// Slow channel starts: createSession waits longer than the server's own
// start bound (so a slow start isn't "Can't reach"), and the player's
// "Starting…" becomes a plain-words note after 5 s. Library logic runs under
// brs; the scene wiring is checked in the source (as the other scene tests do).
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { runBrs } from './brs-run.mjs';

const LIB = ['source/lib/BowtieClient.brs'];
const BASE = 'http://bowtie.local:8080/';
const NOTE = 'Still starting — the stream is buffering. This can take a little while.';

const scene = readFileSync(new URL('../components/PlayerScene.bs', import.meta.url), 'utf8');
const xml = readFileSync(new URL('../components/PlayerScene.xml', import.meta.url), 'utf8');
const body = (src, start) => {
    const at = src.indexOf(start);
    assert.ok(at >= 0, `${start} not found`);
    const rest = src.slice(at + start.length);
    const end = rest.search(/\n(end sub|end function)/);
    return rest.slice(0, end);
};

test('createSession waits 60 s, longer than the server takes to start a stream', () => {
    const out = runBrs(LIB, `
        out.create = bowtie_client_buildRequest("createSession", { channelId: 9, caps: {} }, "a", "${BASE}").timeoutMs
        out.channels = bowtie_client_buildRequest("channels", {}, "a", "${BASE}").timeoutMs
        out.health = bowtie_client_buildRequest("healthz", {}, "", "${BASE}").timeoutMs
    `);
    // Server: device answer 12 s + first playlist 15 s + one fallback 15 s.
    assert.equal(out.create, 60000);
    assert.ok(out.create > (12 + 15 + 15) * 1000);
    assert.equal(out.channels, 10000);
    assert.equal(out.health, 2000);
});

test('a request with no answer at all still says can\'t reach', () => {
    const out = runBrs(LIB, `
        err = bowtie_client_parseResponse("createSession", 0, "", "${BASE}").error
        out.msg = bowtie_client_viewerMessage(err, "Playback failed. Try again or pick a lower quality.")
    `);
    assert.equal(out.msg, "Can't reach your Bowtie server. Check your connection and try again.");
});

test('slow-start note: copy, 5 s, and only while the first start is on', () => {
    const out = runBrs(LIB, `
        s = bowtie_client_showsSlowStartNote
        out.text = bowtie_client_slowStartText()
        out.secs = bowtie_client_slowStartSeconds()
        out.t = [
            s("starting", false, 0),
            s("playing", true, 0),
            s("playing", false, 0),
            s("starting", false, 1),
            s("playing", true, 2),
            s("failed", false, 0),
            s("tunersBusy", false, 0),
            s("idle", false, 0),
        ]
    `);
    assert.equal(out.text, NOTE);
    assert.equal(out.secs, 5);
    assert.deepEqual(out.t, [true, true, false, false, false, false, false, false]);
});

test('player has a one-shot slow-start Timer, observed in init', () => {
    assert.match(xml, /<Timer id="slowStartTimer" duration="5" repeat="false" \/>/);
    const init = body(scene, 'sub init()');
    assert.match(init, /m\.slowStartTimer = m\.top\.findNode\("slowStartTimer"\)/);
    assert.match(init, /m\.slowStartTimer\.duration = bowtie\.client\.slowStartSeconds\(\)/);
    assert.match(init, /m\.slowStartTimer\.observeField\("fire", "onSlowStartFire"\)/);
});

test('every new start or zap resets the note: "Starting…" and a fresh 5 s', () => {
    const replace = body(scene, 'sub scheduleReplace()');
    assert.match(replace, /m\.awaitingFirstFrame = false\n\s*showStarting\("Starting…"\)\n\s*restartSlowStartTimer\(\)/);
    assert.match(body(scene, 'sub restartSlowStartTimer()'), /control = "stop"\n\s*m\.slowStartTimer\.control = "start"/);
});

test('the note replaces the label only when showsSlowStartNote says so', () => {
    const fire = body(scene, 'sub onSlowStartFire()');
    assert.match(fire, /if m\.loadingGroup\.visible <> true then return/);
    assert.match(fire, /if not bowtie\.client\.showsSlowStartNote\(m\.uiPhase, m\.awaitingFirstFrame, m\.retryAttempt\) then return/);
    assert.match(fire, /m\.loadingLabel\.text = bowtie\.client\.slowStartText\(\)/);
});

test('a reconnect keeps its "Reconnecting… (n/3)" text', () => {
    const retry = body(scene, 'sub startBoundedRetry()');
    assert.match(retry, /stopSlowStartTimer\(\)\n\s*showStarting\("Reconnecting… \("/);
});

test('the spinner stays up through "buffering" until the first frame plays', () => {
    const start = body(scene, 'sub startPlayback(');
    assert.match(start, /m\.awaitingFirstFrame = true\n\s*m\.loadingGroup\.visible = true/);
    assert.doesNotMatch(start, /m\.loadingGroup\.visible = false/);
    const state = body(scene, 'sub onVideoState()');
    assert.doesNotMatch(state, /st = "buffering"/);
    assert.match(state, /else if st = "playing"\n[^]*?if m\.uiPhase = "starting" or m\.awaitingFirstFrame = true\n[^]*?hideLoading\(\)/);
});

test('leaving the start (first frame, error, busy, leave) stops the timer', () => {
    assert.match(body(scene, 'sub hideLoading()'), /m\.awaitingFirstFrame = false\n\s*stopSlowStartTimer\(\)/);
    assert.match(body(scene, 'sub showFailed('), /hideLoading\(\)/);
    assert.match(body(scene, 'sub showTunersBusy('), /hideLoading\(\)/);
    assert.match(body(scene, 'sub hideAllOverlays()'), /hideLoading\(\)/);
    assert.match(body(scene, 'sub teardownSession('), /stopSlowStartTimer\(\)/);
});

test('the loading label wraps so the note fits on screen', () => {
    const label = xml.slice(xml.indexOf('id="loadingLabel"'), xml.indexOf('/>', xml.indexOf('id="loadingLabel"')));
    assert.match(label, /wrap="true"/);
});
