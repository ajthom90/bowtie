// All tuners busy and the weak-signal note (source/lib/Tuners.bs), run under
// brs. The server marks a channel watchable:false when every tuner on its
// HDHomeRun is busy with other channels and nobody in Bowtie is on this one,
// so starting it would fail; older servers omit the field (watchable). The
// heartbeat (?signal=1) reports the antenna's reception for the live viewer.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { runBrs } from './brs-run.mjs';

const LIB = ['source/lib/Tuners.brs'];
const WITH_FAV = ['source/lib/Favorites.brs', 'source/lib/Tuners.brs'];

// A JSON value as a BrightScript expression.
const bs = (v) => `ParseJson("${JSON.stringify(v).replace(/"/g, '""')}")`;

const ids = (channels) => channels.map((c) => c.id);

const SOME_NOTE = 'All tuners are in use — showing channels you can join.';
const ALL_NOTE = 'All tuners are in use. Try again in a few minutes.';

test('isWatchable: only an explicit false hides a channel (missing = older server)', () => {
    const out = runBrs(LIB, `
        w = bowtie_tuners_isWatchable
        out.t = [w({ id: 1 }), w({ id: 2, watchable: true }), w({ id: 3, watchable: false }), w(${bs({ id: 4, watchable: null })}), w({ id: 5, watchable: "false" }), w(invalid)]
    `);
    assert.deepEqual(out.t, [true, true, false, true, true, false]);
});

test('watchableOnly keeps the joinable channels in order', () => {
    const out = runBrs(LIB, `
        out.l = snapshot(bowtie_tuners_watchableOnly(${bs([
            { id: 7, watchable: true },
            { id: 4, watchable: false },
            { id: 2 },
            { id: 9, watchable: false },
        ])}))
        out.none = snapshot(bowtie_tuners_watchableOnly(invalid))
    `);
    assert.deepEqual(ids(out.l), [7, 2]);
    assert.deepEqual(out.none, []);
});

test('busyNote: nothing when every channel is joinable, else the plain-words note', () => {
    const out = runBrs(LIB, `
        n = bowtie_tuners_busyNote
        out.t = [
            n(${bs([{ id: 1, watchable: true }, { id: 2 }])}),
            n(${bs([{ id: 1, watchable: false }, { id: 2, watchable: true }])}),
            n(${bs([{ id: 1, watchable: false }, { id: 2, watchable: false }])}),
            n([]),
            n(invalid),
        ]
        out.all = [
            bowtie_tuners_allBusy(${bs([{ id: 1, watchable: false }])}),
            bowtie_tuners_allBusy(${bs([{ id: 1, watchable: false }, { id: 2 }])}),
            bowtie_tuners_allBusy([]),
        ]
    `);
    assert.deepEqual(out.t, ['', SOME_NOTE, ALL_NOTE, '', '']);
    assert.deepEqual(out.all, [true, false, false]);
});

test('normalizeChannels carries watchable (missing → true) onto the rail', () => {
    const out = runBrs(WITH_FAV, `
        st = bowtie_favorites_newState(${bs([
            { id: 4, guideNumber: '10.1', name: 'TEN', favorite: false, watchable: false },
            { id: 7, guideNumber: '9.1', name: 'FOX9', favorite: true, watchable: true },
            { id: 2, guideNumber: '2.1', name: 'WCCO', favorite: false },
        ])})
        out.rail = snapshot(st.channels)
        out.joinable = snapshot(bowtie_tuners_watchableOnly(st.channels))
    `);
    assert.deepEqual(out.rail.map((c) => [c.id, c.watchable]), [[7, true], [4, false], [2, true]]);
    assert.deepEqual(ids(out.joinable), [7, 2]);
});

test('applyWatchable: a re-check updates the flags in place and says if any changed', () => {
    const out = runBrs(LIB, `
        rail = ${bs([{ id: 7, watchable: true }, { id: 4, watchable: false }, { id: 2, watchable: true }])}
        ' Tuner freed on 4; 2 now busy; 99 is new (left for the full reload).
        out.changed = bowtie_tuners_applyWatchable(rail, ${bs({ channels: [{ id: 4, watchable: true }, { id: 2, watchable: false }, { id: 99, watchable: false }] })})
        out.after = snapshot(rail)
        out.same = bowtie_tuners_applyWatchable(rail, ${bs([{ id: 7, watchable: true }, { id: 4 }, { id: 2, watchable: false }])})
        ' Older server: no field anywhere → everything joinable.
        old = ${bs([{ id: 7, watchable: false }])}
        out.old = bowtie_tuners_applyWatchable(old, ${bs([{ id: 7 }])})
        out.oldafter = snapshot(old)
        out.junk = bowtie_tuners_applyWatchable(rail, invalid)
    `);
    assert.equal(out.changed, true);
    assert.deepEqual(out.after.map((c) => [c.id, c.watchable]), [[7, true], [4, true], [2, false]]);
    assert.equal(out.same, false);
    assert.equal(out.old, true);
    assert.equal(out.oldafter[0].watchable, true);
    assert.equal(out.junk, false);
});

test('weak-signal note: shown only for a live heartbeat that says weak', () => {
    const out = runBrs(LIB, `
        s = bowtie_tuners_showWeakSignal
        weak = { "known": true, "weak": true }
        fine = { "known": true, "weak": false }
        unknown = { "known": false, "weak": false }
        out.t = [s(weak, false), s(fine, false), s(unknown, false), s(invalid, false), s(weak, true)]
        out.text = bowtie_tuners_weakSignalText(invalid)
    `);
    assert.deepEqual(out.t, [true, false, false, false, false]);
    assert.equal(out.text, 'Weak signal — the picture may break up.');
});

test('weak note leads with the quality percent when the reading has one', () => {
    const out = runBrs(LIB, `
        w = bowtie_tuners_weakSignalText
        out.t = [
            w({ "known": true, "weak": true, "strength": 96, "quality": 46, "symbolQuality": 0 }),
            w({ "known": true, "weak": true, "strength": 96 }),
            w({ "known": true, "weak": true, "quality": 45.6 }),
            w({ "known": true, "weak": true, "quality": 140 }),
            w({ "known": false, "weak": false, "quality": 46 }),
            w(invalid),
        ]
    `);
    assert.deepEqual(out.t, [
        'Weak signal (46%) — the picture may break up.',
        'Weak signal — the picture may break up.',
        'Weak signal (46%) — the picture may break up.',
        'Weak signal (100%) — the picture may break up.',
        'Weak signal — the picture may break up.',
        'Weak signal — the picture may break up.',
    ]);
});

test('signal stats: quality first, then strength and error-free (symbolQuality); hidden when unknown or a recording', () => {
    const out = runBrs(LIB, `
        s = bowtie_tuners_signalStatsText
        full = { "known": true, "weak": true, "strength": 96, "quality": 46, "symbolQuality": 0 }
        out.t = [
            s(full, false),
            s({ "known": true, "weak": false, "strength": 100, "quality": 90, "symbolQuality": 100 }, false),
            s({ "known": true, "weak": false, "quality": 88 }, false),
            s({ "known": true, "weak": false, "strength": -5, "symbolQuality": 99.5 }, false),
            s({ "known": true, "weak": false }, false),
            s({ "known": false, "weak": false, "quality": 46 }, false),
            s(invalid, false),
            s(full, true),
        ]
    `);
    assert.deepEqual(out.t, [
        'Signal quality 46% · strength 96% · error-free 0%',
        'Signal quality 90% · strength 100% · error-free 100%',
        'Signal quality 88%',
        'Signal strength 0% · error-free 100%',
        '',
        '',
        '',
        '',
    ]);
});
