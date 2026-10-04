// Scene wiring for Continue watching. The pure logic (selection, order,
// labels, layout) runs under brs in continue-watching.test.mjs; this pins
// what can't run off-device: Home's row and keys, the remove call, and the
// Home → AppScene → RecordingsScene resume path.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { runBrs } from './brs-run.mjs';

const read = (p) => readFileSync(new URL(`../components/${p}`, import.meta.url), 'utf8');
const home = read('HomeScene.bs');
const homeXml = read('HomeScene.xml');
const app = read('AppScene.bs');
const recs = read('RecordingsScene.bs');
const recsXml = read('RecordingsScene.xml');
const itemXml = read('ContinueItem.xml');
const itemBs = read('ContinueItem.bs');

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

// The branch of onKeyEvent guarded by `if <node>.hasFocus()`.
function focusBranch(handler, node) {
    const start = handler.indexOf(`if m.${node}.hasFocus()`);
    assert.ok(start >= 0, `onKeyEvent has no ${node} branch`);
    let depth = 0;
    const lines = handler.slice(start).split('\n');
    const out = [];
    for (const line of lines) {
        out.push(line);
        const t = line.trim();
        if (/^if\b/.test(t) && !/\bthen\b/.test(t)) depth++;
        if (/^end if\b/.test(t) && --depth === 0) break;
    }
    return out.join('\n');
}

// ── Home: the row ───────────────────────────────────────────────────────────

test('the Continue row is a one-row RowList above Recent, hidden until it has items', () => {
    const list = tag(homeXml, 'continueList');
    assert.match(list, /<RowList/);
    assert.match(list, /itemComponentName="ContinueItem"/);
    assert.match(list, /numRows="1"/);
    assert.match(list, /visible="false"/);
    // Up/Down must reach HomeScene (the default fixedFocusWrap wraps instead).
    assert.match(list, /vertFocusAnimationStyle="floatingFocus"/);
    assert.match(list, /rowFocusAnimationStyle="floatingFocus"/);
    assert.match(tag(homeXml, 'continueLabel'), /text="Continue watching"/);
    assert.match(tag(homeXml, 'continueLabel'), /visible="false"/);
    assert.ok(homeXml.indexOf('id="continueList"') < homeXml.indexOf('id="recentList"'));
    assert.ok(homeXml.indexOf('id="recentList"') < homeXml.indexOf('id="channelList"'));
    // Its authored spot is where the layout puts it.
    const y = Number(/translation="\[\s*\d+\s*,\s*(\d+)\s*\]"/.exec(list)[1]);
    const layout = runBrs(['source/lib/Recordings.brs', 'source/lib/ContinueWatching.brs'], `
        out.l = snapshot(bowtie_continueWatching_homeLayout(true, true))
    `).l;
    assert.equal(y, layout.continueY);
    const h = Number(/rowItemSize="\[\[\s*\d+\s*,\s*(\d+)\s*\]\]"/.exec(list)[1]);
    assert.equal(h, 112, 'card height differs from the layout');
});

test('Home loads the recorded list with the channels (before the guide) and keeps part-watched ones', () => {
    const load = body(home, 'sub loadContinue(');
    assert.match(load, /kind: "recordings"/);
    assert.match(load, /state: "recorded"/);
    const channels = body(home, 'sub handleChannelsResponse(');
    assert.ok(channels.indexOf('loadContinue()') >= 0);
    assert.ok(channels.indexOf('loadContinue()') < channels.indexOf('loadGuide()'), 'Continue must be requested before the guide');
    const handle = body(home, 'sub handleContinueResponse(');
    assert.match(handle, /bowtie\.continueWatching\.fromResponse\(resp\.data\)/);
    assert.match(handle, /resp\.ok = true/);
    assert.match(body(home, 'sub onApiResponse('), /handleContinueResponse\(resp\)/);
});

test('the row shows only beside a visible rail with items; focus never stays on a hidden row', () => {
    const rows = body(home, 'sub updateRows(');
    // "Beside a visible rail": while the list (chips + rail, or the filter's
    // empty state) is shown.
    assert.match(rows, /showContinue = \(listShown and m\.continueItems\.count\(\) > 0\)/);
    assert.match(rows, /m\.continueList\.visible = showContinue/);
    assert.match(rows, /not showContinue and m\.continueList\.hasFocus\(\)/);
    assert.match(rows, /not showRecent and m\.recentList\.hasFocus\(\)/);
});

test('cards carry the title, detail and progress', () => {
    const render = body(home, 'sub renderContinue(');
    assert.match(render, /bowtie\.recordings\.titleLine\(r\)/);
    assert.match(render, /detail: bowtie\.continueWatching\.cardDetail\(r\)/);
    assert.match(render, /progress: bowtie\.continueWatching\.progress\(r\)/);
    for (const field of ['itemContent', 'width', 'height', 'focusPercent', 'rowListHasFocus', 'itemHasFocus']) {
        assert.match(itemXml, new RegExp(`<field id="${field}"`));
    }
    assert.match(itemBs, /content\.detail/);
    assert.match(itemBs, /content\.progress/);
    assert.match(itemBs, /m\.progressFill\.width = /);
});

// ── Home: keys ──────────────────────────────────────────────────────────────

test('Up/Down chain: header ↔ Continue ↔ Recent ↔ filter chips ↔ rail, skipping hidden rows', () => {
    const handler = body(home, 'function onKeyEvent');
    const rail = focusBranch(handler, 'channelList');
    assert.match(rail, /key = "up"[\s\S]*?m\.filterList\.setFocus\(true\)/);
    const chips = focusBranch(handler, 'filterList');
    assert.match(chips, /key = "up"[\s\S]*?focusAboveFilters\(\)/);
    const above = body(home, 'sub focusAboveFilters(');
    assert.match(above, /m\.recentList\.visible = true[\s\S]*?m\.recentList\.setFocus\(true\)[\s\S]*?m\.continueList\.visible = true[\s\S]*?m\.continueList\.setFocus\(true\)[\s\S]*?focusHeader\(\)/);
    const recent = focusBranch(handler, 'recentList');
    assert.match(recent, /key = "down"[\s\S]*?m\.filterList\.setFocus\(true\)/);
    assert.match(recent, /key = "up"[\s\S]*?m\.continueList\.visible = true[\s\S]*?m\.continueList\.setFocus\(true\)[\s\S]*?focusHeader\(\)/);
    const cont = focusBranch(handler, 'continueList');
    assert.match(cont, /key = "down"[\s\S]*?m\.recentList\.setFocus\(true\)[\s\S]*?m\.filterList\.setFocus\(true\)[\s\S]*?m\.channelList\.setFocus\(true\)/);
    assert.match(cont, /key = "up"[\s\S]*?focusHeader\(\)/);
    const header = handler.slice(handler.indexOf('m.recordingsButton.hasFocus() or m.settingsButton.hasFocus()'));
    assert.match(header, /key = "down"[\s\S]*?m\.continueList\.visible = true[\s\S]*?m\.continueList\.setFocus\(true\)[\s\S]*?m\.recentList\.setFocus\(true\)[\s\S]*?m\.filterList\.setFocus\(true\)[\s\S]*?m\.channelList\.setFocus\(true\)/);
});

test('* on a Continue item offers Remove from Continue watching', () => {
    const cont = focusBranch(body(home, 'function onKeyEvent'), 'continueList');
    assert.match(cont, /key = "options"[\s\S]*?openContinueOptions\(\)/);
    const open = body(home, 'function openContinueOptions');
    assert.match(open, /bowtie\.continueWatching\.optionButtons\(\)/);
    assert.match(open, /showDialog\("continue"/);
    assert.match(open, /m\.returnFocus = m\.continueList/);
    assert.match(body(home, 'sub onDialogButton('), /mode = "continue"[\s\S]*?"remove"[\s\S]*?sendRemoveContinue\(m\.optionsRecording\)/);
    // The dialog hands focus back to the row it came from.
    assert.match(body(home, 'sub restoreRailFocus('), /m\.returnFocus/);
});

test('remove resets the saved position to 0 and drops the item once the server agrees', () => {
    const send = body(home, 'sub sendRemoveContinue(');
    assert.match(send, /kind: "setRecordingPosition"/);
    assert.match(send, /positionSec: 0/);
    const handle = body(home, 'sub handleRemoveResponse(');
    assert.match(handle, /resp\.ok <> true[\s\S]*?showDialog\(/);
    assert.match(handle, /bowtie\.continueWatching\.without\(m\.continueItems, pending\.recordingId\)/);
    assert.match(handle, /updateRows\(\)/);
    assert.match(body(home, 'sub onApiResponse('), /handleRemoveResponse\(resp\)/);
});

// ── Resume: Home → AppScene → RecordingsScene ───────────────────────────────

test('OK on an item hands the recording to AppScene, which opens Recordings in resume mode', () => {
    assert.match(homeXml, /<field id="resumeRecording" type="assocarray" alwaysNotify="true"/);
    assert.match(home, /m\.continueList\.observeField\("rowItemSelected", "onContinueSelected"\)/);
    const sel = body(home, 'sub onContinueSelected(');
    assert.match(sel, /m\.top\.resumeRecording = r/);
    assert.match(sel, /m\.focusContinueOnShow = true/);
    assert.match(app, /m\.homeScene\.observeField\("resumeRecording", "onResumeRecording"\)/);
    const onResume = body(app, 'sub onResumeRecording(');
    const hand = onResume.indexOf('m.recordingsScene.resumeRecording = rec');
    assert.ok(hand >= 0);
    assert.ok(hand < onResume.indexOf('setPhase("recordings")'), 'the recording must be set before the scene shows');
    assert.match(recsXml, /<field id="resumeRecording" type="assocarray"/);
});

test('Recordings in resume mode skips the list and the Resume question', () => {
    const vis = body(recs, 'sub onVisible(');
    assert.match(vis, /m\.top\.resumeRecording = invalid/);
    assert.match(vis, /startResume\(resume\)\s*\n\s*return/);
    const start = body(recs, 'sub startResume(');
    assert.match(start, /m\.resumeMode = true/);
    assert.match(start, /requestPlay\(r\)/);
    assert.doesNotMatch(start, /loadTab\(\)/);
    const play = body(recs, 'sub handlePlayResponse(');
    const resume = play.indexOf('if m.resumeMode = true');
    assert.ok(resume >= 0);
    assert.ok(resume < play.indexOf('showDialog("resume"'), 'resume mode must bypass the Resume dialog');
    assert.match(play.slice(resume), /startPlayback\(r, url, bowtie\.continueWatching\.startPosition\(data\.positionSec, r\.positionSec, durationSec\)\)/);
    // A focused tab's OK would load a tab under the pending play.
    assert.match(body(recs, 'sub onTabSelected('), /if m\.resumeMode = true then return/);
    const keys = body(recs, 'function onKeyEvent(');
    const back = keys.indexOf('m.top.closed = true');
    const guard = keys.indexOf('if m.resumeMode = true then return true');
    assert.ok(back >= 0 && guard > back, 'Back must still close while waiting on /play');
});

test('when resumed playback ends, Recordings closes back to Home (which reloads the row)', () => {
    assert.match(body(recs, 'sub leavePlayback('), /m\.resumeMode = true[\s\S]*?finishResume\(\)/);
    assert.match(body(recs, 'sub finishResume('), /m\.top\.closed = true/);
    // A failed video shows its error first; dismissing it closes.
    const state = body(recs, 'sub onVideoState(');
    assert.match(state, /endPlayback\(\)\s*\n\s*if m\.resumeMode <> true then leavePlayback\(\)\s*\n\s*showDialog\("error"/);
    assert.match(body(recs, 'sub onDialogButton('), /resumeDialogDone\(\)/);
    assert.match(body(recs, 'sub onDialogClosed('), /resumeDialogDone\(\)/);
    assert.match(body(recs, 'sub resumeDialogDone('), /m\.video\.visible <> true[\s\S]*?finishResume\(\)/);
    // Hiding the scene resets the mode and forgets a pending /play.
    const vis = body(recs, 'sub onVisible(');
    const hidden = vis.slice(vis.indexOf('else'));
    assert.match(hidden, /m\.resumeMode = false/);
    assert.match(hidden, /m\.pendingPlayId = ""/);
    // Home reloads on every show; back from resuming it focuses the row.
    assert.match(body(home, 'sub onVisible('), /loadChannels\(\)/);
    assert.match(body(home, 'sub showList('), /m\.focusContinueOnShow = true and m\.continueList\.visible = true[\s\S]*?m\.continueList\.setFocus\(true\)/);
});
