// Scene wiring for DVR recordings. The pure logic runs under brs
// (recordings.test.mjs); this pins what can't run off-device: the Recordings
// screen's player, its keys, AppScene routing, and Home's * dialog.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

const read = (p) => readFileSync(new URL(`../components/${p}`, import.meta.url), 'utf8');
const scene = read('RecordingsScene.bs');
const sceneXml = read('RecordingsScene.xml');
const app = read('AppScene.bs');
const appXml = read('AppScene.xml');
const home = read('HomeScene.bs');
const homeXml = read('HomeScene.xml');
const apiTask = read('tasks/ApiTask.bs');

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

// ── AppScene ────────────────────────────────────────────────────────────────

test('AppScene hosts RecordingsScene as its own phase', () => {
    assert.match(appXml, /<RecordingsScene id="recordingsScene" visible="false"/);
    assert.match(app, /m\.recordingsScene\.apiTask = m\.apiTask/);
    assert.match(app, /m\.homeScene\.observeField\("openRecordings", "onOpenRecordings"\)/);
    assert.match(app, /m\.recordingsScene\.observeField\("closed", "onRecordingsClosed"\)/);
    const phase = body(app, 'sub setPhase(');
    assert.match(phase, /m\.recordingsScene\.visible = false/);
    assert.match(phase, /phase = "recordings"/);
});

// ── Home: Recordings button and * dialog ────────────────────────────────────

test('Home has a Recordings button left of Settings on the same row', () => {
    const rec = tag(homeXml, 'recordingsButton');
    const set = tag(homeXml, 'settingsButton');
    const xy = (t) => /translation="\[\s*(\d+)\s*,\s*(\d+)\s*\]"/.exec(t).slice(1).map(Number);
    const [rx, ry] = xy(rec);
    const [sx, sy] = xy(set);
    assert.equal(ry, sy);
    const recWidth = Number(/minWidth="(\d+)"/.exec(rec)[1]);
    assert.ok(rx + recWidth < sx, 'Recordings overlaps Settings');
    assert.match(homeXml, /<field id="openRecordings" type="boolean" alwaysNotify="true"/);
    assert.match(home, /m\.top\.openRecordings = true/);
});

test('Home header: left/right between Recordings and Settings, down to the rail', () => {
    const handler = body(home, 'function onKeyEvent');
    assert.match(handler, /m\.recordingsButton\.hasFocus\(\)/);
    assert.match(handler, /key = "right"[\s\S]*?m\.settingsButton\.setFocus\(true\)/);
    assert.match(handler, /key = "left"[\s\S]*?m\.recordingsButton\.setFocus\(true\)/);
});

test('* on the rail opens the channel dialog instead of toggling at once', () => {
    const handler = body(home, 'function onKeyEvent');
    const opt = handler.indexOf('key = "options"');
    assert.ok(opt >= 0);
    assert.match(handler.slice(opt, opt + 200), /openChannelOptions\(\)/);
    const open = body(home, 'function openChannelOptions');
    assert.match(open, /bowtie\.recordings\.homeOptions\(/);
    assert.match(open, /bowtie\.guide\.nowNext\(/);
    assert.match(open, /showDialog\("channel"/);
    assert.match(body(home, 'sub showDialog('), /StandardMessageDialog/);
});

test('Home schedules the current program by its exact start; 409 offers Record anyway', () => {
    const send = body(home, 'sub sendRecord(');
    assert.match(send, /kind: "createRecording"/);
    assert.match(send, /programStart: program\.start/);
    assert.match(send, /force: force/);
    assert.match(home, /"Record anyway"/);
    assert.match(home, /bowtie\.recordings\.conflictMessage\(/);
    assert.match(home, /sendRecord\([^)]*, true\)/);
});

// ── Recordings screen ───────────────────────────────────────────────────────

test('the recordings Video node uses the standard trick-play UI', () => {
    const video = tag(sceneXml, 'video');
    assert.match(video, /enableUI="true"/);
    assert.match(video, /visible="false"/);
    // VOD: never flagged live, starts at the resume point.
    assert.doesNotMatch(scene, /\.Live = true/);
    assert.match(scene, /streamFormat = "hls"/);
    assert.match(scene, /PlayStart = /);
});

test('position is saved every 15 s and on the way out, before the stop', () => {
    const timer = tag(sceneXml, 'positionTimer');
    assert.match(timer, /duration="15"/);
    assert.match(timer, /repeat="true"/);
    assert.match(scene, /kind: "setRecordingPosition"/);
    // stopPlayback = endPlayback (save + stop) then leavePlayback (where to).
    assert.match(body(scene, 'sub stopPlayback('), /endPlayback\(\)[\s\S]*?leavePlayback\(\)/);
    const stop = body(scene, 'sub endPlayback(');
    const save = stop.indexOf('savePosition(');
    const halt = stop.indexOf('control = "stop"');
    assert.ok(save >= 0 && halt > save, 'position must be read before the Video stops');
    // A finished recording saves too, so the next open starts over.
    assert.match(body(scene, 'sub onVideoState('), /"finished"/);
});

test('OK on a ready recording asks Resume / Start over via resumeDecision', () => {
    assert.match(scene, /bowtie\.recordings\.resumeDecision\(/);
    assert.match(scene, /"Resume from " \+ bowtie\.recordings\.formatClock\(/);
    assert.match(scene, /"Start over"/);
    assert.match(scene, /kind: "playRecording"/);
});

test('* on a recording opens its menu; Delete asks first', () => {
    const handler = body(scene, 'function onKeyEvent');
    assert.match(handler, /key = "options"[\s\S]*?openOptions\(/);
    assert.match(scene, /bowtie\.recordings\.optionButtons\(/);
    assert.match(scene, /bowtie\.recordings\.deleteConfirmText\(/);
    for (const kind of ['stopRecording', 'deleteRecording', 'protectRecording']) {
        assert.match(scene, new RegExp(`"${kind}"`));
    }
});

test('Back leaves playback first, then the screen', () => {
    const handler = body(scene, 'function onKeyEvent');
    const playing = handler.indexOf('m.video.visible');
    const close = handler.indexOf('m.top.closed = true');
    assert.ok(playing >= 0 && close > playing);
    assert.match(handler.slice(playing, close), /stopPlayback\(/);
});

test('tabs load with the server state filter', () => {
    assert.match(scene, /kind: "recordings"/);
    assert.match(scene, /state: bowtie\.recordings\.tabQuery\(/);
    assert.match(scene, /bowtie\.recordings\.filterForTab\(/);
});

// ── Series ──────────────────────────────────────────────────────────────────

test('Home: Record series posts a rule for the current program and says how many were scheduled', () => {
    const choose = body(home, 'sub onDialogButton(');
    assert.match(choose, /choice = "series"[\s\S]*?sendSeries\(m\.optionsChannelId, m\.optionsProgram\)/);
    const send = body(home, 'sub sendSeries(');
    assert.match(send, /kind: "createRecordingRule"/);
    assert.match(send, /channelId: channelId, programStart: program\.start/);
    assert.match(send, /series: true/);
    const handle = body(home, 'sub handleSeriesResponse(');
    assert.match(handle, /bowtie\.recordings\.seriesScheduledMessage\(resp\.data\)/);
    assert.match(handle, /bowtie\.recordings\.seriesErrorText\(/);
    assert.match(body(home, 'sub onApiResponse('), /pendingRecord\.series = true[\s\S]*?handleSeriesResponse\(resp\)/);
});

test('Recordings has a fourth tab, Shows, right of Missed', () => {
    const shows = tag(sceneXml, 'showsTab');
    assert.match(shows, /text="Shows"/);
    assert.match(shows, /translation="\[860, 112\]"/);
    assert.match(body(scene, 'sub init()'), /m\.tabButtons = \[m\.upcomingTab, m\.recordedTab, m\.missedTab, m\.showsTab\]/);
});

test('Shows lists GET /recording-rules; * offers Stop recording this show (DELETE)', () => {
    const load = body(scene, 'sub loadTab()');
    assert.match(load, /bowtie\.recordings\.isRulesTab\(currentTab\(\)\)[\s\S]*?kind: "recordingRules"/);
    assert.match(body(scene, 'sub handleListResponse('), /bowtie\.recordings\.parseRules\(resp\.data\)/);
    const keys = body(scene, 'function onKeyEvent');
    assert.match(keys, /key = "options"[\s\S]*?openRuleOptions\(/);
    const open = body(scene, 'sub openRuleOptions(');
    assert.match(open, /bowtie\.recordings\.ruleOptionButtons\(/);
    assert.match(open, /showDialog\("ruleOptions"/);
    assert.match(body(scene, 'sub onDialogButton('), /choice = "stopRule"[\s\S]*?sendAction\("deleteRecordingRule", \{ id: m\.target\.id \}\)/);
});

test('rows: Series / Skipped come from the logic; skipped rows are not tinted red', () => {
    const render = body(scene, 'sub renderList()');
    assert.match(render, /bowtie\.recordings\.ruleTitleLine\(/);
    assert.match(render, /bowtie\.recordings\.ruleDetailLine\(/);
    assert.match(render, /missed: bowtie\.recordings\.missedTint\(r\)/);
});

// ── ApiTask ─────────────────────────────────────────────────────────────────

test('ApiTask sends a PATCH with its body', () => {
    const http = body(apiTask, 'function doHttp(');
    assert.match(http, /method = "PATCH"[\s\S]*?AsyncPostFromString\(body\)/);
});
