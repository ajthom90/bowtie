// On Roku TVs the system takes * (options) while video plays and opens its own
// picture/sound panel, so the app never sees "options" during playback. The
// Options dialog (quality, sleep timer) needs a key the app actually receives
// there (Right).
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

const src = readFileSync(new URL('../components/PlayerScene.bs', import.meta.url), 'utf8');
const handler = src.slice(src.indexOf('function onKeyEvent'));

test('Right opens the options dialog (quality, sleep timer) during playback', () => {
    assert.match(handler, /key = "right"[^\n]*\n\s*openQualityDialog\(\)/);
});

test('chrome hint names the Right key for options', () => {
    assert.match(src, /"Quality: " \+ label \+ " · OK pause · Rew\/FF 10 s · Right: options"/);
});

test('* (options) reaches the system menu (audio tracks, closed captioning)', () => {
    assert.doesNotMatch(handler, /key = "options"/);
});

// Rewind / Fast Forward on live TV (bowtie.live; logic in live-seek.test.mjs).
// The app takes these keys so the Video node's own trick play can't jump to
// the start of the server's window with no way back.
test('Rewind, Left and Instant Replay skip back 10 s; Fast Forward skips ahead', () => {
    assert.match(handler, /key = "rewind" or key = "left" or key = "replay"\s*\n\s*skipLive\(-bowtie\.live\.skipSec\(\)\)/);
    assert.match(handler, /key = "fastforward"\s*\n\s*skipLive\(bowtie\.live\.skipSec\(\)\)/);
    assert.match(handler, /if key = "right"\n\s*openQualityDialog\(\)/);
});

test('skips seek the Video node and only once a stream is playing', () => {
    const skip = src.slice(src.indexOf('sub skipLive('), src.indexOf('end sub', src.indexOf('sub skipLive(')));
    assert.match(skip, /if not canSeekLive\(\) then return/);
    assert.match(skip, /bowtie\.live\.skip\(m\.live, m\.video\.position, deltaSec, nowSec\(\)\)/);
    assert.match(skip, /m\.video\.seek = r\.target/);
    const can = src.slice(src.indexOf('function canSeekLive('), src.indexOf('end function', src.indexOf('function canSeekLive(')));
    assert.match(can, /m\.uiPhase = "playing" and m\.awaitingFirstFrame <> true/);
});

test('Options starts with Go to live while behind; Go to live resumes a pause', () => {
    const open = src.slice(src.indexOf('sub openQualityDialog('), src.indexOf('end sub', src.indexOf('sub openQualityDialog(')));
    assert.match(open, /if canSeekLive\(\) and behindLive\(\)\s*\n\s*buttons\.push\("Go to live"\)\s*\n\s*m\.optionIds\.push\("live"\)/);
    const pick = src.slice(src.indexOf('sub onQualityButton('), src.indexOf('end sub', src.indexOf('sub onQualityButton(')));
    assert.match(pick, /if chosen = "live"\s*\n\s*goToLive\(\)/);
    const go = src.slice(src.indexOf('sub goToLive('), src.indexOf('end sub', src.indexOf('sub goToLive(')));
    assert.match(go, /m\.video\.seek = r\.target/);
    assert.match(go, /if m\.video\.state = "paused" then m\.video\.control = "resume"/);
});

// The live point is the Video's duration (less a margin); see LiveSeek.bs.
test('a new stream starts at live: reset on start, seek to live on the first frame', () => {
    const start = src.slice(src.indexOf('sub startPlayback('), src.indexOf('end sub', src.indexOf('sub startPlayback(')));
    assert.match(start, /bowtie\.live\.reset\(m\.live\)/);
    const state = src.slice(src.indexOf('sub onVideoState('), src.indexOf('end sub', src.indexOf('sub onVideoState(')));
    assert.match(state, /bowtie\.live\.seekLanded\(m\.live\)/);
    assert.match(state, /if firstFrame then startAtLive\(\)/);
    const at = src.slice(src.indexOf('sub startAtLive('), src.indexOf('end sub', src.indexOf('sub startAtLive(')));
    assert.match(at, /bowtie\.live\.startTarget\(m\.live, m\.video\.position, nowSec\(\)\)/);
    assert.match(at, /if target >= 0 then m\.video\.seek = target/);
});

test('the live point follows the Video duration, noted once a second while playing', () => {
    const obs = src.slice(src.indexOf('sub observeLive('), src.indexOf('end sub', src.indexOf('sub observeLive(')));
    assert.match(obs, /bowtie\.live\.observe\(m\.live, m\.video\.duration, nowSec\(\)\)/);
    assert.match(src, /m\.liveTimer\.observeField\("fire", "refreshLiveLabel"\)/);
    const refresh = src.slice(src.indexOf('sub refreshLiveLabel('), src.indexOf('end sub', src.indexOf('sub refreshLiveLabel(')));
    assert.match(refresh, /observeLive\(\)/);
    assert.match(refresh, /m\.liveTimer\.control = "start"/);
});
