// Skip ad pure logic (source/lib/Commercials.bs), run under brs. Recordings
// may carry "commercials": [{start, end}] (float seconds on the playback
// timeline); absent or empty means nothing to skip.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { runBrs } from './brs-run.mjs';

const LIB = ['source/lib/Commercials.brs'];
const RECORDINGS = ['source/lib/Recordings.brs'];

// A JSON value as a BrightScript expression.
const bs = (v) => `ParseJson("${JSON.stringify(v).replace(/"/g, '""')}")`;

const SEGS = [
    { start: 600.5, end: 780.25 },
    { start: 1500, end: 1620 },
];

test('normalize: missing, empty or junk input means nothing to skip', () => {
    const out = runBrs(LIB, `
        n = bowtie_ads_normalize
        out.t = [n(invalid), n([]), n("x"), n(${bs({ start: 1, end: 2 })}), n(${bs([null, 'junk', { start: '1', end: 2 }, { start: 5 }, { end: 9 }, { start: 30, end: 30 }, { start: 40, end: 20 }])})]
    `);
    for (const segs of out.t) assert.deepEqual(segs, []);
});

test('normalize: sorts by start, merges overlapping and touching segments, clamps a negative start', () => {
    const out = runBrs(LIB, `
        out.segs = bowtie_ads_normalize(${bs([
            { start: 1500, end: 1620 },
            { start: 600, end: 700 },
            { start: -3, end: 10 },
            { start: 650, end: 780 }, // overlaps 600–700
            { start: 1620, end: 1650 }, // touches 1500–1620
            { start: 2000, end: 2030.5 },
        ])})
    `);
    assert.deepEqual(out.segs, [
        { start: 0, end: 10 },
        { start: 600, end: 780 },
        { start: 1500, end: 1650 },
        { start: 2000, end: 2030.5 },
    ]);
});

test('segmentAt: start inclusive, end exclusive', () => {
    const out = runBrs(LIB, `
        segs = bowtie_ads_normalize(${bs(SEGS)})
        out.t = []
        for each p in [0, 600.4, 600.5, 700, 780.2, 780.25, 1000, 1500, 1619.9, 1620, 99999]
            out.t.push(bowtie_ads_segmentAt(segs, p))
        end for
        out.none = bowtie_ads_segmentAt([], 10)
    `);
    assert.deepEqual(out.t, [-1, -1, 0, 0, 0, -1, -1, 1, 1, -1, -1]);
    assert.equal(out.none, -1);
});

test('manual: inside a segment shows the hint; OK target is the segment end', () => {
    const out = runBrs(LIB, `
        segs = bowtie_ads_normalize(${bs(SEGS)})
        mem = bowtie_ads_newMemory()
        out.before = bowtie_ads_decide(segs, 599, false, mem)
        out.inside = bowtie_ads_decide(segs, 650, false, mem)
        out.again = bowtie_ads_decide(segs, 651, false, mem)
        out.hint = bowtie_ads_hintText()
    `);
    assert.equal(out.before.action, 'none');
    assert.equal(out.inside.action, 'hint');
    assert.equal(out.inside.index, 0);
    assert.equal(out.inside.target, 780.25);
    // Manual mode never skips on its own.
    assert.equal(out.again.action, 'hint');
    assert.equal(out.hint, 'Skip ad ▸ (OK)');
});

test('auto-skip: each segment once per playback; seeking back in shows the hint instead', () => {
    const out = runBrs(LIB, `
        segs = bowtie_ads_normalize(${bs(SEGS)})
        mem = bowtie_ads_newMemory()
        out.first = bowtie_ads_decide(segs, 600.5, true, mem)
        ' Landed exactly at the end: out of the segment.
        out.landed = bowtie_ads_decide(segs, 780.25, true, mem)
        out.second = bowtie_ads_decide(segs, 1501, true, mem)
        ' Viewer rewinds into the first ad: no second auto-skip.
        out.back = bowtie_ads_decide(segs, 620, true, mem)
        out.fresh = bowtie_ads_decide(segs, 620, true, bowtie_ads_newMemory())
        out.text = bowtie_ads_skippedText()
    `);
    assert.deepEqual(out.first, { action: 'skip', index: 0, target: 780.25 });
    assert.equal(out.landed.action, 'none');
    assert.deepEqual(out.second, { action: 'skip', index: 1, target: 1620 });
    assert.equal(out.back.action, 'hint');
    // A new playback starts with a clean memory.
    assert.equal(out.fresh.action, 'skip');
    assert.equal(out.text, 'Skipped ad');
});

test('a seek that lands just short of the end does not flash the hint or skip again', () => {
    const out = runBrs(LIB, `
        segs = bowtie_ads_normalize(${bs(SEGS)})
        mem = bowtie_ads_newMemory()
        out.skip = bowtie_ads_decide(segs, 610, true, mem)
        out.short = bowtie_ads_decide(segs, 779, true, mem)
        ' VOD keyframes are every 4 s: a seek can land up to ~4 s short.
        out.keyframeShort = bowtie_ads_decide(segs, 776.5, true, mem)
        m2 = bowtie_ads_newMemory()
        out.hint = bowtie_ads_decide(segs, 700, false, m2)
        bowtie_ads_noteSkip(m2, out.hint.target)
        out.manualShort = bowtie_ads_decide(segs, 779.5, false, m2)
        out.wayBack = bowtie_ads_decide(segs, 700, false, m2)
    `);
    assert.equal(out.skip.action, 'skip');
    assert.equal(out.short.action, 'none');
    assert.equal(out.keyframeshort.action, 'none');
    assert.equal(out.manualshort.action, 'none');
    assert.equal(out.wayback.action, 'hint');
});

test('dismiss hides the hint until playback leaves that segment', () => {
    const out = runBrs(LIB, `
        segs = bowtie_ads_normalize(${bs(SEGS)})
        mem = bowtie_ads_newMemory()
        bowtie_ads_dismiss(mem, 0)
        out.t = []
        for each p in [610, 700, 900, 650, 1550]
            out.t.push(bowtie_ads_decide(segs, p, false, mem).action)
        end for
    `);
    assert.deepEqual(out.t, ['none', 'none', 'none', 'hint', 'hint']);
});

test('recordings keep the server commercials list (missing → empty)', () => {
    const out = runBrs(RECORDINGS, `
        l = bowtie_recordings_parseList(${bs([{ id: 1, state: 'ready', commercials: SEGS }, { id: 2, state: 'ready' }, { id: 3, commercials: 'junk' }])})
        out.a = l[0].commercials
        out.b = l[1].commercials
        out.c = l[2].commercials
    `);
    assert.deepEqual(out.a, SEGS);
    assert.deepEqual(out.b, []);
    assert.deepEqual(out.c, []);
});
