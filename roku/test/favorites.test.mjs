// Favorites/Recents pure logic (source/lib/Favorites.bs), run under brs.
// Rail order: favorites first in guide-number order, then the rest in server
// order. Toggles are optimistic and revert to the last state the server
// confirmed (not merely the opposite of the request), so two queued toggles
// that both fail still leave the star matching the server.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { runBrs } from './brs-run.mjs';

const LIB = ['source/lib/Favorites.brs'];

// A JSON value as a BrightScript expression.
const bs = (v) => `ParseJson("${JSON.stringify(v).replace(/"/g, '""')}")`;

// Server order is (device, guide_number as text), so "10.1" precedes "9.1".
const SERVER = [
    { id: 4, guideNumber: '10.1', name: 'TEN', logoUrl: '', reception: 'ok', favorite: false },
    { id: 7, guideNumber: '9.1', name: 'FOX9', logoUrl: '', reception: 'ok', favorite: true },
    { id: 2, guideNumber: '2.1', name: 'WCCO', logoUrl: '', reception: 'ok', favorite: false },
    { id: 5, guideNumber: '11.1', name: 'KARE', logoUrl: '', reception: 'ok', favorite: true },
    { id: 3, guideNumber: '5.10', name: 'KSTP-D', logoUrl: '', reception: 'ok', favorite: true },
];

const ids = (channels) => channels.map((c) => c.id);

test('compareGuideNumbers orders major then minor numerically', () => {
    const out = runBrs(LIB, `
        c = bowtie_favorites_compareGuideNumbers
        out.a = c("9.1", "10.1")
        out.b = c("10.1", "9.1")
        out.c = c("5.2", "5.10")
        out.d = c("5.1", "5.1")
        out.e = c("7", "7.1")
    `);
    assert.deepEqual(out, { a: -1, b: 1, c: -1, d: 0, e: -1 });
});

test('normalizeChannels keeps server fields and reports favorite support', () => {
    const out = runBrs(LIB, `
        out.arr = bowtie_favorites_normalizeChannels(${bs(SERVER)})
        out.wrapped = bowtie_favorites_normalizeChannels(${bs({ channels: [{ id: 9, guideNumber: 4.1, name: 'X' }] })})
        out.none = bowtie_favorites_normalizeChannels(invalid)
    `);
    assert.equal(out.arr.supported, true);
    assert.deepEqual(out.arr.channels[1], { id: 7, guideNumber: '9.1', name: 'FOX9', favorite: true, watchable: true, order: 1 });
    // Older server: no "favorite" key → unsupported, every channel unstarred.
    assert.equal(out.wrapped.supported, false);
    assert.deepEqual(out.wrapped.channels, [{ id: 9, guideNumber: '4.1', name: 'X', favorite: false, watchable: true, order: 0 }]);
    assert.deepEqual(out.none, { channels: [], supported: false });
});

test('sortChannels puts favorites first by guide number, rest in server order', () => {
    const out = runBrs(LIB, `
        n = bowtie_favorites_normalizeChannels(${bs(SERVER)})
        out.sorted = bowtie_favorites_sortChannels(n.channels)
    `);
    // favorites 5.10, 9.1, 11.1; then 10.1, 2.1 as the server sent them
    assert.deepEqual(ids(out.sorted), [3, 7, 5, 4, 2]);
});

test('indexOfChannel finds a channel in the rail, -1 when absent', () => {
    const out = runBrs(LIB, `
        n = bowtie_favorites_normalizeChannels(${bs(SERVER)})
        rail = bowtie_favorites_sortChannels(n.channels)
        out.fox = bowtie_favorites_indexOfChannel(rail, 7)
        out.ten = bowtie_favorites_indexOfChannel(rail, 4)
        out.missing = bowtie_favorites_indexOfChannel(rail, 99)
    `);
    assert.deepEqual(out, { fox: 1, ten: 3, missing: -1 });
});

test('toggle flips optimistically and re-sorts the rail', () => {
    const out = runBrs(LIB, `
        st = bowtie_favorites_newState(${bs(SERVER)})
        out.initial = snapshot(st.channels)
        out.on = bowtie_favorites_toggle(st, 2).on
        out.afterstar = snapshot(st.channels)
        out.off = bowtie_favorites_toggle(st, 2).on
        out.afterunstar = snapshot(st.channels)
        out.unknown = bowtie_favorites_toggle(st, 99)
    `);
    assert.deepEqual(ids(out.initial), [3, 7, 5, 4, 2]);
    assert.equal(out.on, true);
    assert.deepEqual(ids(out.afterstar), [2, 3, 7, 5, 4]);
    assert.equal(out.afterstar[0].favorite, true);
    assert.equal(out.off, false);
    // Unstarred 2.1 goes back to its server position, not the front of the rest.
    assert.deepEqual(ids(out.afterunstar), [3, 7, 5, 4, 2]);
    assert.equal(out.unknown, null);
});

test('toggle is a no-op against a server without favorites', () => {
    const old = SERVER.map(({ favorite, ...c }) => c);
    const out = runBrs(LIB, `
        st = bowtie_favorites_newState(${bs(old)})
        out.supported = st.supported
        out.result = bowtie_favorites_toggle(st, 2)
        out.channels = st.channels
    `);
    assert.equal(out.supported, false);
    assert.equal(out.result, null);
    assert.deepEqual(ids(out.channels), [4, 7, 2, 5, 3]);
});

test('a failed toggle reverts to the confirmed state', () => {
    const out = runBrs(LIB, `
        st = bowtie_favorites_newState(${bs(SERVER)})
        on = bowtie_favorites_toggle(st, 2).on
        out.changed = bowtie_favorites_resolve(st, 2, on, false)
        out.channels = st.channels
    `);
    assert.equal(out.changed, true);
    assert.deepEqual(ids(out.channels), [3, 7, 5, 4, 2]);
    assert.equal(out.channels[4].favorite, false);
});

test('a successful toggle is kept and becomes the confirmed state', () => {
    const out = runBrs(LIB, `
        st = bowtie_favorites_newState(${bs(SERVER)})
        on = bowtie_favorites_toggle(st, 7).on
        out.okchanged = bowtie_favorites_resolve(st, 7, on, true)
        ' A later toggle that fails must fall back to "off", the confirmed state.
        on2 = bowtie_favorites_toggle(st, 7).on
        out.failchanged = bowtie_favorites_resolve(st, 7, on2, false)
        out.channels = st.channels
    `);
    assert.equal(out.okchanged, false);
    assert.equal(out.failchanged, true);
    assert.deepEqual(ids(out.channels), [3, 5, 4, 7, 2]);
    assert.equal(out.channels.find((c) => c.id === 7).favorite, false);
});

test('two queued toggles that both fail leave the server state', () => {
    const out = runBrs(LIB, `
        st = bowtie_favorites_newState(${bs(SERVER)})
        a = bowtie_favorites_toggle(st, 2).on ' PUT
        b = bowtie_favorites_toggle(st, 2).on ' DELETE
        bowtie_favorites_resolve(st, 2, a, false)
        bowtie_favorites_resolve(st, 2, b, false)
        out.channels = st.channels
    `);
    assert.equal(out.channels.find((c) => c.id === 2).favorite, false);
});

test('parseRecents keeps well-formed entries newest first', () => {
    const body = [
        { channelId: 7, guideNumber: '9.1', name: 'FOX9', logoUrl: '', watchedAt: '2026-10-03T19:42:10Z' },
        { guideNumber: '1.1', name: 'no id' },
        { channelId: 2, guideNumber: 2.1, name: 'WCCO', logoUrl: '', watchedAt: '2026-10-03T18:00:00Z' },
    ];
    const out = runBrs(LIB, `
        out.list = bowtie_favorites_parseRecents(${bs(body)})
        out.empty = bowtie_favorites_parseRecents(${bs([])})
        out.bad = bowtie_favorites_parseRecents(${bs({ error: 'nope' })})
        out.none = bowtie_favorites_parseRecents(invalid)
    `);
    assert.deepEqual(out.list, [
        { channelId: 7, guideNumber: '9.1', name: 'FOX9' },
        { channelId: 2, guideNumber: '2.1', name: 'WCCO' },
    ]);
    assert.deepEqual(out.empty, []);
    assert.deepEqual(out.bad, []);
    assert.deepEqual(out.none, []);
});
