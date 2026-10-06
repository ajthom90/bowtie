// HomeScene wiring for favorites/recents. The pure logic is exercised under
// brs (favorites.test.mjs); this pins the scene-level contract that can't run
// off-device: which keys HomeScene takes, the recent row, the star.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { runBrs } from './brs-run.mjs';

const read = (p) => readFileSync(new URL(`../components/${p}`, import.meta.url), 'utf8');
const home = read('HomeScene.bs');
const homeXml = read('HomeScene.xml');
const railXml = read('ChannelRailItem.xml');
const railBs = read('ChannelRailItem.bs');

function onKeyEvent(src) {
    const start = src.indexOf('function onKeyEvent');
    assert.ok(start >= 0, 'HomeScene has no onKeyEvent');
    return src.slice(start, src.indexOf('end function', start));
}

test('* (options) opens the channel dialog only while the rail has focus', () => {
    const handler = onKeyEvent(home);
    const opt = handler.indexOf('key = "options"');
    assert.ok(opt >= 0, 'options is not handled');
    // The options branch sits inside the channelList focus block.
    const gate = handler.lastIndexOf('m.channelList.hasFocus()', opt);
    assert.ok(gate >= 0, 'options is not gated on channelList focus');
    assert.doesNotMatch(handler.slice(gate, opt), /\n\s*else\b|\n\s*end if/, 'options escapes the channelList gate');
    assert.match(handler.slice(opt, opt + 200), /openChannelOptions\(\)/);
});

test('the dialog\'s Favorite button toggles the channel captured when it opened', () => {
    const start = home.indexOf('sub onDialogButton');
    const body = home.slice(start, home.indexOf('end sub', start));
    assert.match(body, /choice = "favorite"[\s\S]*?toggleFavorite\(m\.optionsChannelId\)/);
});

test('up/down hand focus between settings, recentList and the rail', () => {
    const handler = onKeyEvent(home);
    assert.match(handler, /m\.recentList\.hasFocus\(\)/);
    assert.match(handler, /m\.recentList\.setFocus\(true\)/);
    assert.match(handler, /m\.channelList\.setFocus\(true\)/);
    assert.match(handler, /m\.settingsButton\.setFocus\(true\)/);
});

test('a toggle re-renders in place instead of reloading (no spinner, focus kept)', () => {
    const start = home.indexOf('function toggleFavorite');
    assert.ok(start >= 0);
    const body = home.slice(start, home.indexOf('end function', start));
    assert.doesNotMatch(body, /loadChannels\(\)/);
    assert.match(body, /renderRail\(/);
    assert.match(body, /sendFavorite\(/);
});

test('the player is handed the sorted rail (joinable channels) for zapping', () => {
    assert.match(home, /m\.channels = m\.fav\.channels/);
    assert.match(home, /m\.joinable = bowtie\.tuners\.watchableOnly\(m\.channels\)/);
    assert.match(home, /channels: m\.joinable/);
});

test('recentList sits above the rail and starts hidden', () => {
    const recent = homeXml.indexOf('id="recentList"');
    const rail = homeXml.indexOf('id="channelList"');
    assert.ok(recent >= 0 && rail >= 0);
    const tag = homeXml.slice(homeXml.lastIndexOf('<', recent), homeXml.indexOf('/>', recent));
    assert.match(tag, /visible="false"/);
    const recentY = Number(/translation="\[\s*\d+\s*,\s*(\d+)\s*\]"/.exec(tag)[1]);
    const recentH = Number(/itemSize="\[\s*\d+\s*,\s*(\d+)\s*\]"/.exec(tag)[1]);
    // While the row shows, HomeScene moves the rail down below it
    // (bowtie.continueWatching.homeLayout; every combination is checked in
    // continue-watching.test.mjs).
    const layout = runBrs(['source/lib/Recordings.brs', 'source/lib/ContinueWatching.brs'], `
        out.l = snapshot(bowtie_continueWatching_homeLayout(false, true))
    `).l;
    assert.equal(layout.recentY, recentY, 'XML start position differs from the layout');
    assert.ok(recentY >= 128, 'recentList overlaps the header');
    assert.ok(recentY + recentH < layout.railY, 'recentList must be above channelList');
    assert.ok(layout.railY + layout.railRows * 128 - 8 <= 1080, 'shifted rail runs off the 1080 canvas');
    assert.match(home, /layout = bowtie\.continueWatching\.homeLayout\(showContinue, showRecent\)/);
    assert.match(home, /m\.channelList\.translation = \[80, layout\.railY\]/);
    assert.match(home, /m\.channelList\.numRows = layout\.railRows/);
});

test('rail items show a star for favorites', () => {
    assert.match(railXml, /id="favoriteStar"/);
    assert.match(railBs, /m\.favoriteStar\.visible = /);
    assert.match(home, /favorite: ch\.favorite/);
});
