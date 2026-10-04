// "Continue watching" pure logic (source/lib/ContinueWatching.bs), run under
// brs. Cases mirror web/src/recordings/continueModel.test.ts and
// ios/BowtieKit/Tests/BowtieKitTests/ContinueWatchingTests.swift so every app
// picks, orders and labels the same items.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { runBrs } from './brs-run.mjs';

const LIB = ['source/lib/Recordings.brs', 'source/lib/ContinueWatching.brs'];

// A JSON value as a BrightScript expression.
const bs = (v) => `ParseJson("${JSON.stringify(v).replace(/"/g, '""')}")`;

let nextId = 1;
const rec = (over = {}) => ({
    id: nextId++,
    title: 'Jeopardy!',
    subtitle: '',
    channelId: 3,
    channelName: '5.1 KSTP',
    start: '2026-10-03T00:00:00Z',
    stop: '2026-10-03T01:00:00Z',
    state: 'ready',
    durationSec: 3600,
    positionSec: 600,
    positionUpdatedAt: '2026-10-03T12:00:00Z',
    scheduledBy: 'andrew',
    canManage: true,
    ...over,
});

// isEligible over normalized recordings (parseList), one boolean per input.
function eligible(list) {
    return runBrs(LIB, `
        out.got = []
        for each r in bowtie_recordings_parseList(${bs(list)})
            out.got.push(bowtie_continueWatching_isEligible(r))
        end for
    `).got;
}

// The row's items (ids) for a raw GET /recordings body.
function itemIds(list) {
    return runBrs(LIB, `
        out.ids = []
        for each r in bowtie_continueWatching_fromResponse(${bs(list)})
            out.ids.push(r.id)
        end for
    `).ids;
}

test('constants match the other apps', () => {
    const out = runBrs(LIB, `
        out.min = bowtie_continueWatching_minPositionSec()
        out.margin = bowtie_continueWatching_endMarginSec()
        out.max = bowtie_continueWatching_maxItems()
    `);
    assert.deepEqual(out, { min: 60, margin: 120, max: 10 });
});

test('needs at least a minute watched (59 s is not enough, 60 s is)', () => {
    assert.deepEqual(eligible([rec({ positionSec: 59 }), rec({ positionSec: 60 }), rec({ positionSec: 0 })]), [false, true, false]);
});

test('drops recordings within two minutes of the end (exactly duration - 120 is out)', () => {
    assert.deepEqual(
        eligible([
            rec({ durationSec: 3600, positionSec: 3479 }),
            rec({ durationSec: 3600, positionSec: 3480 }),
            rec({ durationSec: 3600, positionSec: 3600 }),
        ]),
        [true, false, false],
    );
});

test('only ready recordings', () => {
    const states = ['scheduled', 'waiting', 'recording', 'converting', 'failed'];
    assert.deepEqual(eligible(states.map((state) => rec({ state }))), states.map(() => false));
});

test('excludes parental-locked recordings', () => {
    assert.deepEqual(eligible([rec({ locked: true }), rec({ locked: false }), rec()]), [false, true, true]);
});

test('needs a known duration', () => {
    assert.deepEqual(
        eligible([rec({ durationSec: 0, positionSec: 600 }), rec({ durationSec: 150, positionSec: 60 }), rec({ durationSec: undefined })]),
        [false, false, false],
    );
});

test('older servers: no position fields means nothing qualifies', () => {
    assert.deepEqual(eligible([rec({ positionSec: undefined, positionUpdatedAt: undefined })]), [false]);
});

test('sorts by positionUpdatedAt, newest first; missing or unparseable timestamps last', () => {
    const old = rec({ positionUpdatedAt: '2026-10-01T00:00:00Z' });
    const none = rec({ positionUpdatedAt: undefined });
    const fresh = rec({ positionUpdatedAt: '2026-10-03T20:00:00Z' });
    const bad = rec({ positionUpdatedAt: 'not a date' });
    const mid = rec({ positionUpdatedAt: '2026-10-02T09:30:00+02:00' });
    const nul = rec({ positionUpdatedAt: null });
    const ids = itemIds([old, none, fresh, bad, mid, nul]);
    assert.deepEqual(ids.slice(0, 3), [fresh.id, mid.id, old.id]);
    // The three without a usable time: same start, so newest id first.
    assert.deepEqual(ids.slice(3), [nul.id, bad.id, none.id]);
});

test('last saved first, then unsaved by start (newest recording first)', () => {
    // ios testSortsByLastSavedNewestFirstThenUnsavedByStart
    const rows = [
        rec({ id: 1, start: '2026-10-01T00:00:00Z', positionUpdatedAt: undefined }),
        rec({ id: 2, start: '2026-10-03T00:00:00Z', positionUpdatedAt: '2026-10-04T10:00:00Z' }),
        rec({ id: 3, start: '2026-10-02T00:00:00Z', positionUpdatedAt: undefined }),
        rec({ id: 4, start: '2026-10-01T00:00:00Z', positionUpdatedAt: '2026-10-04T12:00:00Z' }),
        rec({ id: 5, positionSec: 0, positionUpdatedAt: '2026-10-04T13:00:00Z' }),
        rec({ id: 6, locked: true, positionUpdatedAt: '2026-10-04T14:00:00Z' }),
    ];
    assert.deepEqual(itemIds(rows), [4, 2, 3, 1]);
});

test('same save time falls back to start, then id (newest first)', () => {
    const rows = [
        rec({ id: 1, start: '2026-10-01T00:00:00Z', positionUpdatedAt: '2026-10-04T10:00:00Z' }),
        rec({ id: 2, start: '2026-10-02T00:00:00Z', positionUpdatedAt: '2026-10-04T10:00:00Z' }),
        rec({ id: 3, start: '2026-10-02T00:00:00Z', positionUpdatedAt: '2026-10-04T10:00:00Z' }),
    ];
    assert.deepEqual(itemIds(rows), [3, 2, 1]);
});

test('sub-second and offset timestamps order correctly', () => {
    const rows = [
        rec({ id: 1, positionUpdatedAt: '2026-10-04T10:00:00.250Z' }),
        rec({ id: 2, positionUpdatedAt: '2026-10-04T10:00:00.750Z' }),
        // 05:00:01 at -05:00 is 10:00:01Z: newest.
        rec({ id: 3, positionUpdatedAt: '2026-10-04T05:00:01-05:00' }),
    ];
    assert.deepEqual(itemIds(rows), [3, 2, 1]);
});

test('filters with the selection rules', () => {
    const keep = rec();
    const rows = [
        keep,
        rec({ positionSec: 59 }),
        rec({ positionSec: 3500 }),
        rec({ locked: true }),
        rec({ state: 'converting' }),
    ];
    assert.deepEqual(itemIds(rows), [keep.id]);
});

test('shows at most 10', () => {
    const rows = Array.from({ length: 14 }, (_, i) =>
        rec({ id: 100 + i, positionUpdatedAt: new Date(Date.UTC(2026, 9, 1, i)).toISOString() }),
    );
    const ids = itemIds(rows);
    assert.equal(ids.length, 10);
    assert.equal(ids[0], 113);
    assert.equal(ids[9], 104);
});

test('is empty when nothing qualifies or the body is not a list', () => {
    assert.deepEqual(itemIds([]), []);
    assert.deepEqual(itemIds([rec({ positionSec: 0 })]), []);
    const out = runBrs(LIB, `
        out.a = bowtie_continueWatching_fromResponse(invalid).count()
        out.b = bowtie_continueWatching_fromResponse({}).count()
    `);
    assert.deepEqual(out, { a: 0, b: 0 });
});

test('parseTime: RFC 3339 to epoch seconds; invalid when missing or unparseable', () => {
    const cases = [
        '1970-01-01T00:00:00Z',
        '2026-10-03T12:00:00Z',
        '2026-10-02T09:30:00+02:00',
        '2026-10-04T05:00:01-05:00',
        '2026-10-03T12:00:00.5Z',
        '2024-02-29T23:59:59z',
        '2026-10-03 12:00:00Z',
    ];
    const bad = ['', 'not a date', '2026-10-03', '2026-13-03T12:00:00Z', '2026-10-03T12:00:00', '2026-10-03T12:00:00+0200', '2026-1O-03T12:00:00Z'];
    const out = runBrs(LIB, `
        out.good = []
        for each s in ${bs(cases)}
            out.good.push(bowtie_continueWatching_parseTime(s))
        end for
        out.bad = []
        for each s in ${bs(bad)}
            out.bad.push(bowtie_continueWatching_parseTime(s) = invalid)
        end for
        out.inv = (bowtie_continueWatching_parseTime(invalid) = invalid)
        out.num = (bowtie_continueWatching_parseTime(5) = invalid)
    `);
    const expected = cases.map((s) => Date.parse(s.replace(' ', 'T').replace(/z$/, 'Z')) / 1000);
    assert.deepEqual(out.good, expected);
    assert.deepEqual(out.bad, bad.map(() => true));
    assert.equal(out.inv, true);
    assert.equal(out.num, true);
});

test('formatTimeLeft: under a minute, minutes, hours and minutes', () => {
    const secs = [0, 59, -5, 60, 37 * 60 + 59, 59 * 60 + 59, 3600, 65 * 60, 2 * 3600 + 30, 2 * 3600 + 59 * 60];
    const out = runBrs(LIB, `
        out.got = []
        for each s in ${bs(secs)}
            out.got.push(bowtie_continueWatching_formatTimeLeft(s))
        end for
        out.inv = bowtie_continueWatching_formatTimeLeft(invalid)
    `);
    assert.deepEqual(out.got, [
        'less than a minute left',
        'less than a minute left',
        'less than a minute left',
        '1 min left',
        '37 min left',
        '59 min left',
        '1 hr left',
        '1 hr 5 min left',
        '2 hr left',
        '2 hr 59 min left',
    ]);
    assert.equal(out.inv, 'less than a minute left');
});

test('remainingText from a recording', () => {
    const rows = [
        [3600, 3541],
        [3600, 3540],
        [1980, 600],
        [3600, 1],
        [3660, 60],
        [7200, 3300],
        [600, 900],
        [3600, 1380],
    ].map(([durationSec, positionSec]) => rec({ durationSec, positionSec }));
    const out = runBrs(LIB, `
        out.got = []
        for each r in bowtie_recordings_parseList(${bs(rows)})
            out.got.push(bowtie_continueWatching_remainingText(r))
        end for
    `);
    assert.deepEqual(out.got, [
        'less than a minute left',
        '1 min left',
        '23 min left',
        '59 min left',
        '1 hr left',
        '1 hr 5 min left',
        'less than a minute left',
        '37 min left',
    ]);
});

test('progress is position / duration, clamped to 0..1', () => {
    const rows = [
        rec({ durationSec: 3600, positionSec: 900 }),
        rec({ durationSec: 0, positionSec: 900 }),
        rec({ durationSec: 600, positionSec: 900 }),
    ];
    const out = runBrs(LIB, `
        out.got = []
        for each r in bowtie_recordings_parseList(${bs(rows)})
            out.got.push(bowtie_continueWatching_progress(r))
        end for
    `);
    assert.ok(Math.abs(out.got[0] - 0.25) < 1e-4);
    assert.equal(out.got[1], 0);
    assert.equal(out.got[2], 1);
});

test('card detail: episode (when there is one) and time left', () => {
    const rows = [
        rec({ title: 'Nova', subtitle: 'Ice', durationSec: 3600, positionSec: 1380 }),
        rec({ title: 'Nova', subtitle: '', durationSec: 3600, positionSec: 1380 }),
    ];
    const out = runBrs(LIB, `
        out.got = []
        for each r in bowtie_recordings_parseList(${bs(rows)})
            out.got.push(bowtie_continueWatching_cardDetail(r))
        end for
    `);
    assert.deepEqual(out.got, ['Ice · 37 min left', '37 min left']);
});

test('* menu: Remove from Continue watching, Cancel', () => {
    const out = runBrs(LIB, `
        menu = bowtie_continueWatching_optionButtons()
        out.labels = bowtie_recordings_buttonLabels(menu)
        out.first = bowtie_recordings_buttonId(menu, 0)
        out.second = bowtie_recordings_buttonId(menu, 1)
    `);
    assert.deepEqual(out.labels, ['Remove from Continue watching', 'Cancel']);
    assert.equal(out.first, 'remove');
    assert.equal(out.second, 'cancel');
});

test('without: drops the removed recording and keeps the order', () => {
    const rows = [rec({ id: 1 }), rec({ id: 2 }), rec({ id: 3 })];
    const out = runBrs(LIB, `
        list = bowtie_recordings_parseList(${bs(rows)})
        out.ids = []
        for each r in bowtie_continueWatching_without(list, 2)
            out.ids.push(r.id)
        end for
        out.missing = bowtie_continueWatching_without(list, 9).count()
        out.orig = list.count()
    `);
    assert.deepEqual(out.ids, [1, 3]);
    assert.equal(out.missing, 3);
    assert.equal(out.orig, 3);
});

test('startPosition: the play response\'s saved position, else the row\'s; near the start plays from 0', () => {
    const out = runBrs(LIB, `
        out.server = bowtie_continueWatching_startPosition(1500, 600, 3600)
        out.fallback = bowtie_continueWatching_startPosition(0, 600, 3600)
        out.missing = bowtie_continueWatching_startPosition(invalid, 600, 3600)
        out.tooearly = bowtie_continueWatching_startPosition(5, 5, 3600)
        out.atend = bowtie_continueWatching_startPosition(3590, 600, 3600)
    `);
    assert.deepEqual(out, { server: 1500, fallback: 600, missing: 600, tooearly: 0, atend: 0 });
});

test('home layout: rows stack header → Continue → Recent → filter chips → rail inside 1080', () => {
    const out = runBrs(LIB, `
        out.none = snapshot(bowtie_continueWatching_homeLayout(false, false))
        out.recent = snapshot(bowtie_continueWatching_homeLayout(false, true))
        out.cont = snapshot(bowtie_continueWatching_homeLayout(true, false))
        out.both = snapshot(bowtie_continueWatching_homeLayout(true, true))
    `);
    // The chips take 72 px (56 + 16) above the rail.
    assert.equal(out.none.filtersY, 136);
    assert.equal(out.none.railY, 208);
    assert.equal(out.none.railRows, 6);
    assert.equal(out.recent.recentY, 172);
    assert.equal(out.recent.filtersY, 280);
    assert.equal(out.recent.railY, 352);
    assert.equal(out.recent.railRows, 5);
    assert.equal(out.both.railRows, 4);
    const HEADER_BOTTOM = 128;
    const CONTINUE_H = 112;
    const RECENT_H = 88;
    const CHIPS_H = 56;
    const RAIL_PITCH = 128; // 120 item + 8 spacing
    for (const [name, l, showC, showR] of [
        ['none', out.none, false, false],
        ['recent', out.recent, false, true],
        ['cont', out.cont, true, false],
        ['both', out.both, true, true],
    ]) {
        let bottom = HEADER_BOTTOM;
        if (showC) {
            assert.ok(l.continueLabelY >= bottom, `${name}: Continue label overlaps the header`);
            assert.ok(l.continueY >= l.continueLabelY + 32, `${name}: Continue row overlaps its label`);
            bottom = l.continueY + CONTINUE_H;
        }
        if (showR) {
            assert.ok(l.recentLabelY >= bottom, `${name}: Recent label overlaps the row above`);
            assert.ok(l.recentY >= l.recentLabelY + 32, `${name}: Recent row overlaps its label`);
            bottom = l.recentY + RECENT_H;
        }
        assert.ok(l.filtersY >= bottom, `${name}: filter chips overlap the row above`);
        bottom = l.filtersY + CHIPS_H;
        assert.ok(l.railY > bottom, `${name}: rail overlaps the filter chips`);
        assert.ok(l.railRows >= (showC && showR ? 4 : 5), `${name}: rail shows too few channels`);
        assert.ok(l.railY + l.railRows * RAIL_PITCH - 8 <= 1080, `${name}: rail runs off the 1080 canvas`);
    }
});
