// HomeScene wiring for favorites/recents. The pure logic is exercised under
// brs (favorites.test.mjs); this pins the scene-level contract that can't run
// off-device: which keys HomeScene takes, the recent row, the star.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

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

test('* (options) toggles a favorite only while the rail has focus', () => {
    const handler = onKeyEvent(home);
    const opt = handler.indexOf('key = "options"');
    assert.ok(opt >= 0, 'options is not handled');
    // The options branch sits inside the channelList focus block.
    const gate = handler.lastIndexOf('m.channelList.hasFocus()', opt);
    assert.ok(gate >= 0, 'options is not gated on channelList focus');
    assert.doesNotMatch(handler.slice(gate, opt), /\n\s*else\b|\n\s*end if/, 'options escapes the channelList gate');
    assert.match(handler.slice(opt, opt + 200), /toggleFocusedFavorite\(\)/);
});

test('up/down hand focus between settings, recentList and the rail', () => {
    const handler = onKeyEvent(home);
    assert.match(handler, /m\.recentList\.hasFocus\(\)/);
    assert.match(handler, /m\.recentList\.setFocus\(true\)/);
    assert.match(handler, /m\.channelList\.setFocus\(true\)/);
    assert.match(handler, /m\.settingsButton\.setFocus\(true\)/);
});

test('a toggle re-renders in place instead of reloading (no spinner, focus kept)', () => {
    const start = home.indexOf('function toggleFocusedFavorite');
    assert.ok(start >= 0);
    const body = home.slice(start, home.indexOf('end function', start));
    assert.doesNotMatch(body, /loadChannels\(\)/);
    assert.match(body, /renderRail\(/);
    assert.match(body, /sendFavorite\(/);
});

test('the player is handed the sorted rail for zapping', () => {
    assert.match(home, /m\.channels = m\.fav\.channels/);
    assert.match(home, /channels: m\.channels/);
});

test('recentList sits above the rail and starts hidden', () => {
    const recent = homeXml.indexOf('id="recentList"');
    const rail = homeXml.indexOf('id="channelList"');
    assert.ok(recent >= 0 && rail >= 0);
    const tag = homeXml.slice(homeXml.lastIndexOf('<', recent), homeXml.indexOf('/>', recent));
    assert.match(tag, /visible="false"/);
    const recentY = Number(/translation="\[\s*\d+\s*,\s*(\d+)\s*\]"/.exec(tag)[1]);
    const recentH = Number(/itemSize="\[\s*\d+\s*,\s*(\d+)\s*\]"/.exec(tag)[1]);
    // While the row shows, HomeScene moves the rail down below it.
    const railY = Number(/const RAIL_Y_WITH_RECENTS = (\d+)/.exec(home)[1]);
    const railRows = Number(/const RAIL_ROWS_WITH_RECENTS = (\d+)/.exec(home)[1]);
    assert.ok(recentY >= 128, 'recentList overlaps the header');
    assert.ok(recentY + recentH < railY, 'recentList must be above channelList');
    assert.ok(railY + railRows * 128 - 8 <= 1080, 'shifted rail runs off the 1080 canvas');
    assert.match(home, /m\.channelList\.translation = \[80, RAIL_Y_WITH_RECENTS\]/);
});

test('rail items show a star for favorites', () => {
    assert.match(railXml, /id="favoriteStar"/);
    assert.match(railBs, /m\.favoriteStar\.visible = /);
    assert.match(home, /favorite: ch\.favorite/);
});
