// On Roku TVs the system takes * (options) while video plays and opens its own
// picture/sound panel, so the app never sees "options" during playback. The
// quality dialog needs a key the app actually receives there (Right).
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

const src = readFileSync(new URL('../components/PlayerScene.bs', import.meta.url), 'utf8');
const handler = src.slice(src.indexOf('function onKeyEvent'));

test('Right opens the quality dialog during playback', () => {
    assert.match(handler, /key = "right"[^\n]*\n\s*openQualityDialog\(\)/);
});

test('chrome hint names the Right key for quality', () => {
    assert.match(src, /"Quality: " \+ label \+ " · OK play\/pause · Right: quality"/);
});

test('* (options) reaches the system menu (audio tracks, closed captioning)', () => {
    assert.doesNotMatch(handler, /key = "options"/);
});
