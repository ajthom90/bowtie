// Scene wiring for the sleep timer (live + recording players) and Skip ad
// (recording player, Settings toggle). The logic runs under brs
// (sleep-timer.test.mjs, commercials.test.mjs); this pins what can't run
// off-device: which key opens what, how the timer fires, and focus.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

const read = (p) => readFileSync(new URL(`../${p}`, import.meta.url), 'utf8');
const player = read('components/PlayerScene.bs');
const playerXml = read('components/PlayerScene.xml');
const recs = read('components/RecordingsScene.bs');
const recsXml = read('components/RecordingsScene.xml');
const settings = read('components/SettingsScene.bs');
const settingsXml = read('components/SettingsScene.xml');
const registry = read('source/lib/Registry.bs');
const home = read('components/HomeScene.bs');
const app = read('components/AppScene.bs');

function body(src, signature) {
    const start = src.indexOf(signature);
    assert.ok(start >= 0, `missing ${signature}`);
    const end = src.indexOf(signature.startsWith('function') ? 'end function' : 'end sub', start);
    return src.slice(start, end);
}

// ── Live player ─────────────────────────────────────────────────────────────

test('live: the Right-key Options dialog ends with Sleep timer, which opens its own menu', () => {
    assert.match(player, /import "pkg:\/source\/lib\/SleepTimer\.bs"/);
    const open = body(player, 'sub openQualityDialog(');
    assert.match(open, /buttons\.push\("Sleep timer"\)/);
    assert.match(open, /dialog\.message = optionsMessage\(\)/);
    assert.match(body(player, 'function optionsMessage('), /bowtie\.sleep\.statusText\(m\.sleep/);
    assert.match(body(player, 'sub onQualityButton('), /idx = options\.count\(\)[\s\S]*?openSleepDialog\(\)/);
    const sleep = body(player, 'sub openSleepDialog(');
    assert.match(sleep, /bowtie\.sleep\.options\(currentProgramEndSec\(\), nowSec\(\)\)/);
    assert.match(sleep, /bowtie\.sleep\.buttonLabels\(/);
});

test('live: End of this program comes from the guide Home already has', () => {
    assert.match(body(home, 'sub selectChannelAt('), /guide: m\.guideById/);
    assert.match(body(app, 'sub onSelectedChannel('), /m\.playerScene\.guide = guide/);
    assert.match(playerXml, /<field id="guide" type="assocarray" \/>/);
    const end = body(player, 'function currentProgramEndSec(');
    assert.match(end, /bowtie\.guide\.nowNext\(programs, isoNow\(\)\)\.now/);
    assert.match(end, /return 0/);
});

test('live: when it runs out the player leaves exactly as Back does', () => {
    const tick = body(player, 'sub onSleepTick(');
    assert.match(tick, /bowtie\.sleep\.tick\(m\.sleep, nowSec\(\)\)/);
    assert.match(tick, /r\.fired = true[\s\S]*?leavePlayer\(\)/);
    assert.match(body(player, 'sub leavePlayer('), /teardownSession\(true\)/);
    assert.match(tick, /bowtie\.sleep\.promptText\(r\.remaining\)/);
    assert.match(playerXml, /<Timer id="sleepTimer" duration="1" repeat="true" \/>/);
});

test('live: OK while "Still watching?" shows keeps watching instead of pausing', () => {
    const keys = body(player, 'function onKeyEvent(');
    const warn = keys.indexOf('m.sleepWarning.visible = true');
    assert.ok(warn >= 0 && warn < keys.indexOf('togglePlayPause()'));
    assert.match(keys.slice(warn - 60, warn + 80), /key = "OK"[\s\S]*?keepWatching\(\)/);
    assert.match(body(player, 'sub keepWatching('), /bowtie\.sleep\.extend\(m\.sleep\)/);
});

test('live: survives channel changes, resets on entering and leaving', () => {
    for (const sig of ['sub zap(', 'sub scheduleReplace(', 'sub teardownSession(']) {
        assert.doesNotMatch(body(player, sig), /sleep/i, `${sig} must not touch the sleep timer`);
    }
    const visible = body(player, 'sub onVisible(');
    assert.equal(visible.match(/resetSleep\(\)/g)?.length, 2);
    assert.match(body(player, 'sub resetSleep('), /bowtie\.sleep\.cancel\(m\.sleep\)/);
});

// ── Recording player ────────────────────────────────────────────────────────

test('recording: Down opens the sleep timer (no End of this program); it stops playback when it runs out', () => {
    const keys = body(recs, 'function onKeyEvent(');
    assert.match(keys, /key = "down"[\s\S]*?openSleepDialog\(\)/);
    assert.match(body(recs, 'sub openSleepDialog('), /bowtie\.sleep\.options\(0, nowSec\(\)\)/);
    assert.match(body(recs, 'sub onDialogButton('), /mode = "sleep"[\s\S]*?chooseSleep\(idx\)/);
    assert.match(body(recs, 'sub onSleepTick('), /r\.fired = true[\s\S]*?stopPlayback\(\)/);
    assert.match(body(recs, 'sub stopPlayback('), /endPlayback\(\)/);
    assert.match(body(recs, 'sub endPlayback('), /resetPlaybackOverlays\(\)/);
    assert.match(body(recs, 'sub startPlayback('), /resetPlaybackOverlays\(\)/);
    assert.match(body(recs, 'sub resetPlaybackOverlays('), /bowtie\.sleep\.cancel\(m\.sleep\)/);
    assert.match(recsXml, /text="Down: sleep timer"/);
});

test('recording: the warning takes focus so OK reaches the screen, not the Video', () => {
    assert.match(body(recs, 'sub onSleepTick('), /m\.sleepWarning\.setFocus\(true\)/);
    const keys = body(recs, 'function onKeyEvent(');
    assert.match(keys, /key = "OK" and m\.sleepWarning\.visible = true[\s\S]*?keepWatching\(\)/);
});

test('recording: commercials load per playback with a fresh memory and the device setting', () => {
    const start = body(recs, 'sub startPlayback(');
    assert.match(start, /m\.adSegs = bowtie\.ads\.normalize\(r\.commercials\)/);
    assert.match(start, /m\.adMemory = bowtie\.ads\.newMemory\(\)/);
    assert.match(start, /m\.autoSkip = bowtie\.registry\.loadAutoSkipAds\(\)/);
    assert.match(body(recs, 'sub onVideoPosition('), /updateAds\(p\)/);
});

test('recording: auto-skip seeks and says Skipped ad; the hint is a focused Skip ad button', () => {
    const ads = body(recs, 'sub updateAds(');
    assert.match(ads, /bowtie\.ads\.decide\(m\.adSegs, positionSec, m\.autoSkip, m\.adMemory\)/);
    assert.match(ads, /d\.action = "skip"[\s\S]*?m\.video\.seek = d\.target[\s\S]*?m\.adToast\.visible = true/);
    assert.match(ads, /d\.action = "hint"[\s\S]*?showSkipHint\(/);
    assert.match(body(recs, 'sub showSkipHint('), /m\.skipAdButton\.setFocus\(true\)/);
    const skip = body(recs, 'sub onSkipAd(');
    assert.match(skip, /bowtie\.ads\.noteSkip\(m\.adMemory, target\)/);
    assert.match(skip, /m\.video\.seek = target/);
    assert.match(recsXml, /id="skipAdButton"[\s\S]*?text="Skip ad ▸ \(OK\)"/);
    assert.match(recsXml, /text="Skipped ad"/);
    // Overlays are drawn over the Video.
    const video = recsXml.indexOf('id="video"');
    for (const id of ['skipAdButton', 'adToast', 'sleepWarning']) {
        assert.ok(recsXml.indexOf(`id="${id}"`) > video, `${id} must come after the Video`);
    }
});

test('recording: trick-play keys on Skip ad put it away for that ad and return to the Video', () => {
    const keys = body(recs, 'function onKeyEvent(');
    assert.match(keys, /m\.skipAdButton\.hasFocus\(\)[\s\S]*?key = "left" or key = "right"[\s\S]*?bowtie\.ads\.dismiss\(/);
    assert.match(body(recs, 'sub playbackFocus('), /m\.video\.setFocus\(true\)/);
    assert.match(body(recs, 'sub restoreFocus('), /playbackFocus\(\)/);
});

// ── Settings ────────────────────────────────────────────────────────────────

test('Settings: Skip ads automatically toggles a registry pref (default off) that sign-out keeps', () => {
    assert.match(settingsXml, /id="autoSkipButton"[\s\S]*?text="Skip ads automatically: Off"/);
    assert.match(body(settings, 'sub onAutoSkipButton('), /bowtie\.registry\.saveAutoSkipAds\(not bowtie\.registry\.loadAutoSkipAds\(\)\)/);
    assert.match(body(settings, 'sub onVisible('), /updateAutoSkip\(\)/);
    const load = body(registry, 'function loadAutoSkipAds(');
    assert.match(load, /sec\.Read\("autoSkipAds"\) = "true"/);
    assert.match(load, /return false/);
    assert.doesNotMatch(body(registry, 'function clearAll('), /autoSkipAds/);
    assert.doesNotMatch(body(registry, 'function clearRefreshToken('), /autoSkipAds/);
});

test('Settings: Up/Down reach every button, including the new toggle', () => {
    assert.match(body(settings, 'sub init('), /m\.buttons = \[m\.backButton, m\.changeServerButton, m\.changePasswordButton, m\.signOutButton, m\.autoSkipButton\]/);
    const keys = body(settings, 'function onKeyEvent(');
    assert.match(keys, /key = "up" and at > 0/);
    assert.match(keys, /key = "down" and at < m\.buttons\.count\(\) - 1/);
});
