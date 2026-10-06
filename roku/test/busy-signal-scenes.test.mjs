// Scene wiring for the weak-signal note, all tuners busy, and plain-words
// errors. The rules run under brs in tuners.test.mjs and
// client-signal-errors.test.mjs; this pins what can't run off-device.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { runBrs } from './brs-run.mjs';

const read = (p) => readFileSync(new URL(`../${p}`, import.meta.url), 'utf8');
const player = read('components/PlayerScene.bs');
const playerXml = read('components/PlayerScene.xml');
const home = read('components/HomeScene.bs');
const homeXml = read('components/HomeScene.xml');
const recordings = read('components/RecordingsScene.bs');
const settings = read('components/SettingsScene.bs');

function body(src, signature) {
    const start = src.indexOf(signature);
    assert.ok(start >= 0, `missing ${signature}`);
    const end = src.indexOf(signature.startsWith('function') ? 'end function' : 'end sub', start);
    return src.slice(start, end);
}

function tag(xml, id) {
    const at = xml.indexOf(`id="${id}"`);
    assert.ok(at >= 0, `no element ${id}`);
    return xml.slice(xml.lastIndexOf('<', at), xml.indexOf('>', at));
}

// ── Weak signal (player) ────────────────────────────────────────────────────

test('player: a hidden note under the top bar, drawn beneath the error screens', () => {
    assert.match(tag(playerXml, 'signalNote'), /visible="false"/);
    assert.match(tag(playerXml, 'signalNoteLabel'), /text="Weak signal — the picture may break up\."/);
    const at = playerXml.indexOf('id="signalNote"');
    assert.ok(playerXml.indexOf('id="chromeGroup"') < at, 'note before the top bar');
    assert.ok(at < playerXml.indexOf('id="errorGroup"'), 'error screen covers the note');
    assert.ok(at < playerXml.indexOf('id="tunersGroup"'), 'busy screen covers the note');
    // Outside the auto-hiding top bar, so it stays while the signal is weak.
    const chromeEnd = playerXml.indexOf('</Group>', playerXml.indexOf('id="chromeGroup"'));
    assert.ok(at > chromeEnd);
});

test('player: the latest heartbeat decides the note; parental stop unchanged', () => {
    assert.match(player, /import "pkg:\/source\/lib\/Tuners\.bs"/);
    const resp = body(player, 'sub onApiResponse()');
    assert.match(resp, /rid = m\.lastHeartbeatId[\s\S]*?resp\.ok = true[\s\S]*?updateSignal\(resp\.data\)/);
    const update = body(player, 'sub updateSignal(');
    // A zap since it was sent: that reading was for the old channel.
    assert.match(update, /m\.lastHeartbeatViewerId <> m\.activeViewerId/);
    assert.match(update, /m\.signalNoteLabel\.text = bowtie\.tuners\.weakSignalText\(data\)/);
    assert.match(update, /bowtie\.tuners\.showWeakSignal\(data, false\)/);
    assert.match(update, /m\.signalStats = bowtie\.tuners\.signalStatsText\(data, false\)/);
    assert.match(update, /m\.signalLabel\.text = m\.signalStats/);
    assert.match(update, /m\.signalLabel\.visible = \(m\.signalStats <> ""\)/);
    const set = body(player, 'sub setWeakSignal(');
    assert.match(set, /m\.signalNote\.visible = show/);
    // Announced for Audio Guide when it appears.
    assert.match(set, /announce\(m\.signalNoteLabel\.text\)/);
    assert.match(body(player, 'sub announce('), /roAudioGuide/);
});

test('player: the percentages sit in the channel-info bar and the info overlay', () => {
    const label = tag(playerXml, 'signalLabel');
    assert.match(label, /visible="false"/);
    const chromeStart = playerXml.indexOf('id="chromeGroup"');
    const chromeEnd = playerXml.indexOf('</Group>', chromeStart);
    const at = playerXml.indexOf('id="signalLabel"');
    assert.ok(at > chromeStart && at < chromeEnd, 'signalLabel inside the top bar');
    // Inside the 140 px bar.
    const y = Number(/translation="\[\s*\d+\s*,\s*(\d+)\s*\]"/.exec(label)[1]);
    const h = Number(/height="(\d+)"/.exec(label)[1]);
    assert.ok(y + h <= 140);
    assert.match(body(player, 'sub updateDebugOverlay('), /m\.signalStats/);
});

test('player: the note and percentages go with the channel (zap, leave, failure, busy)', () => {
    assert.match(body(player, 'sub clearSignal('), /updateSignal\(invalid\)/);
    for (const sig of ['sub scheduleReplace(', 'sub teardownSession(', 'sub showFailed(', 'sub showTunersBusy(', 'sub handleParentalStop(', 'sub hideAllOverlays(']) {
        assert.match(body(player, sig), /clearSignal\(\)/, sig);
    }
});

// ── All tuners busy (home rail) ─────────────────────────────────────────────

test('home: the rail, Recent and zapping use only the joinable channels', () => {
    assert.match(home, /import "pkg:\/source\/lib\/Tuners\.bs"/);
    const render = body(home, 'sub renderRail(');
    assert.match(render, /m\.joinable = bowtie\.tuners\.watchableOnly\(m\.channels\)/);
    assert.match(render, /bowtie\.guideFilter\.channelsMatching\(m\.joinable, m\.guideById, m\.filter, atIso\)/);
    assert.match(render, /updateRecents\(\)/);
    assert.match(body(home, 'sub updateRecents('), /bowtie\.favorites\.indexOfChannel\(m\.joinable, r\.channelId\)/);
    assert.match(body(home, 'sub handleRecentsResponse('), /m\.allRecents = /);
    const select = body(home, 'sub selectChannelAt(');
    assert.match(select, /ch = m\.joinable\[idx\]/);
    assert.match(select, /channels: m\.joinable/);
});

test('home: the busy note sits right of the chips; all busy replaces the rail', () => {
    const note = tag(homeXml, 'busyNote');
    assert.match(note, /visible="false"/);
    assert.match(note, /wrap="true"/);
    // Right of the six chips (80 + 6 × 160 + 5 × 16 = 1120) within the 1840 edge.
    const x = Number(/translation="\[\s*(\d+)/.exec(note)[1]);
    const w = Number(/width="(\d+)"/.exec(note)[1]);
    assert.ok(x >= 1120 && x + w <= 1840);
    const rows = body(home, 'sub updateRows(');
    assert.match(rows, /note = bowtie\.tuners\.busyNote\(m\.channels\)/);
    assert.match(rows, /allBusy = bowtie\.tuners\.allBusy\(m\.channels\)/);
    assert.match(rows, /m\.busyNote\.translation = \[\d+, layout\.filtersY\]/);
    assert.match(rows, /m\.busyNote\.visible = \(listShown and note <> "" and not allBusy\)/);
    // All busy: the plain words where the rail was, and Try again re-checks.
    assert.match(rows, /if allBusy[\s\S]*?m\.filterEmptyLabel\.text = note[\s\S]*?m\.showAllButton\.text = "Try again"/);
    assert.match(rows, /m\.showAllButton\.text = "Show all"/);
    assert.match(body(home, 'sub onShowAll('), /bowtie\.tuners\.allBusy\(m\.channels\)[\s\S]*?checkWatchable\(\)/);
});

test('home: re-checks every 30 s while shown and keeps focus on the same channel', () => {
    const timer = tag(homeXml, 'watchableTimer');
    assert.match(timer, /duration="30"/);
    assert.match(timer, /repeat="true"/);
    assert.match(body(home, 'sub init('), /m\.watchableTimer\.observeField\("fire", "onWatchableTimer"\)/);
    const vis = body(home, 'sub onVisible(');
    assert.match(vis, /m\.watchableTimer\.control = "start"/);
    assert.match(vis, /m\.watchableTimer\.control = "stop"/);
    const fire = body(home, 'sub onWatchableTimer(');
    assert.match(fire, /m\.busy = true/);
    assert.match(fire, /checkWatchable\(\)/);
    assert.match(body(home, 'sub checkWatchable('), /kind: "channels"/);
    const handle = body(home, 'sub handleWatchableResponse(');
    assert.match(handle, /bowtie\.tuners\.applyWatchable\(m\.channels, resp\.data\)/);
    assert.match(handle, /renderRail\(focusedId\)/);
    // A full reload in flight wins; it supersedes the check.
    assert.match(body(home, 'sub loadChannels('), /m\.pendingCheckId = ""/);
});

// ── Plain-words errors ──────────────────────────────────────────────────────

test('player: no error codes or raw messages on screen (they go to the log)', () => {
    for (const sig of ['sub handleVideoError(', 'sub startBoundedRetry(', 'sub startPlayback(']) {
        const b = body(player, sig);
        assert.doesNotMatch(b, /showFailed\([^)]*errorCode=/, sig);
        assert.doesNotMatch(b, /showFailed\([^)]*"Missing playlist URL"/, sig);
    }
    assert.match(body(player, 'sub startBoundedRetry('), /showFailed\(bowtie\.client\.streamStoppedText\(\), ""\)/);
    assert.match(body(player, 'sub startBoundedRetry('), /print "\[player\]/);
    assert.match(body(player, 'sub handleCreateError('), /bowtie\.client\.viewerMessage\(err, PLAYBACK_FAILED\)/);
    assert.doesNotMatch(player, /PLAYBACK_AUTH_FAILED/);
});

test('recordings: a playback error says the stream stopped, not the player\'s message', () => {
    assert.match(recordings, /import "pkg:\/source\/lib\/BowtieClient\.bs"/);
    const state = body(recordings, 'sub onVideoState()');
    assert.doesNotMatch(state, /\[msg\]/);
    assert.match(state, /bowtie\.client\.streamStoppedText\(\)/);
    assert.match(body(recordings, 'function listErrorText('), /bowtie\.client\.viewerMessage\(err, /);
    assert.match(body(recordings, 'function actionErrorText('), /bowtie\.client\.viewerMessage\(err, /);
    assert.match(body(recordings, 'sub handlePlayResponse('), /bowtie\.client\.viewerMessage\(resp\.error, /);
});

test('home and settings: request errors go through viewerMessage', () => {
    assert.match(home, /import "pkg:\/source\/lib\/BowtieClient\.bs"/);
    assert.match(body(home, 'function errorMessage('), /bowtie\.client\.viewerMessage\(/);
    assert.match(body(home, 'sub handleRecordResponse('), /bowtie\.client\.viewerMessage\(err, /);
    assert.match(body(home, 'sub handleRemoveResponse('), /bowtie\.client\.viewerMessage\(resp\.error, /);
    assert.match(settings, /import "pkg:\/source\/lib\/BowtieClient\.bs"/);
    assert.match(body(settings, 'sub onApiResponse('), /bowtie\.client\.viewerMessage\(resp\.error, PASSWORD_FAILED\)/);
});

test('the busy note fits the space right of the chips at the layouts in use', () => {
    const l = runBrs(['source/lib/Recordings.brs', 'source/lib/ContinueWatching.brs'], `
        out.l = [snapshot(bowtie_continueWatching_homeLayout(false, false)), snapshot(bowtie_continueWatching_homeLayout(true, true))]
    `).l;
    const h = Number(/height="(\d+)"/.exec(tag(homeXml, 'busyNote'))[1]);
    for (const layout of l) assert.ok(layout.filtersY + h <= layout.railY, 'note overlaps the rail');
});
