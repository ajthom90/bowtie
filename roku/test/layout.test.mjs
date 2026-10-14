// A LayoutGroup with horizAlignment="center" centers its children ON its
// translation x (it is not a left edge). The channel is authored at 1920x1080
// (ui_resolutions=fhd), so a screen-centered group must sit at x=960; x=460
// pushed Connect/Login into the left third of the screen.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync, readdirSync } from 'node:fs';

const dir = new URL('../components/', import.meta.url).pathname;

test('center-aligned LayoutGroups are centered on the 1920 canvas', () => {
    const offenders = [];
    for (const name of readdirSync(dir).filter((f) => f.endsWith('.xml'))) {
        const xml = readFileSync(dir + name, 'utf8');
        for (const m of xml.matchAll(/<LayoutGroup\b([^>]*)>/g)) {
            const attrs = m[1];
            if (!/horizAlignment="center"/.test(attrs)) continue;
            const t = /translation="\[\s*(-?\d+)\s*,/.exec(attrs);
            const x = t ? Number(t[1]) : 0;
            if (x !== 960) offenders.push(`${name}: LayoutGroup x=${x}`);
        }
    }
    assert.deepEqual(offenders, []);
});

// A MarkupList wraps by default (fixedFocusWrap): Up on its first item went
// to its last, so focus never left the list for the chips, the tabs or the
// header (Recordings, Settings). The scenes' onKeyEvent moves focus up from
// there, which needs the list to hand Up through.
test('every MarkupList hands Up/Down through at its ends instead of wrapping', () => {
    const offenders = [];
    for (const name of readdirSync(dir).filter((f) => f.endsWith('.xml'))) {
        const xml = readFileSync(dir + name, 'utf8');
        for (const m of xml.matchAll(/<MarkupList\b([^>]*)>/g)) {
            if (!/vertFocusAnimationStyle="floatingFocus"/.test(m[1])) {
                offenders.push(`${name}: ${/id="([^"]+)"/.exec(m[1])?.[1]}`);
            }
        }
    }
    assert.deepEqual(offenders, []);
});
