// Live TV rewind pure logic (source/lib/LiveSeek.bs), run under brs. The
// clock is injected: calls take atSec, so these tests just pass numbers.
// Labels and the 10 s live tolerance mirror ios/BowtieKit LiveEdge.swift.
// Position and duration are on the Video's timeline (seconds); the live point
// is duration less a 20 s margin, moving on with the clock.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { runBrs } from './brs-run.mjs';

const LIB = ['source/lib/LiveSeek.brs'];
const T0 = 1791100000;

test('unknown duration: live, and skips stay relative', () => {
    const out = runBrs(LIB, `
        s = bowtie_live_newState()
        out.behind = bowtie_live_behindSec(s, 50, ${T0})
        out.back = bowtie_live_skip(s, 50, -10, ${T0})
        bowtie_live_seekLanded(s)
        out.go = bowtie_live_goLive(s, 40, ${T0})
        bowtie_live_seekLanded(s)
        out.start = bowtie_live_startTarget(s, 40, ${T0})
        out.label = bowtie_live_label(0)
        out.spoken = bowtie_live_spokenText(0)
    `);
    assert.equal(out.behind, 0);
    assert.deepEqual(out.back, { target: 40, live: false });
    assert.deepEqual(out.go, { target: 40, live: true });
    assert.equal(out.start, -1);
    assert.equal(out.label, 'Live');
    assert.equal(out.spoken, 'Watching live');
});

test('behind is the live point (duration − 20 s) less the position', () => {
    const out = runBrs(LIB, `
        s = bowtie_live_newState()
        bowtie_live_observe(s, 112, ${T0})
        out.atlive = bowtie_live_behindSec(s, 92, ${T0})
        out.thirty = bowtie_live_behindSec(s, 62, ${T0})
        out.label = bowtie_live_label(out.thirty)
        out.long = bowtie_live_label(90)
        out.spoken = bowtie_live_spokenText(90)
        out.one = bowtie_live_spokenText(61)
        out.nine = bowtie_live_label(9)
        out.ten = bowtie_live_label(10)
    `);
    assert.equal(out.atlive, 0);
    assert.equal(out.thirty, 30);
    assert.equal(out.label, 'Live −0:30');
    assert.equal(out.long, 'Live −1:30');
    assert.equal(out.spoken, '1 minute 30 seconds behind live');
    assert.equal(out.one, '1 minute 1 second behind live');
    assert.equal(out.nine, 'Live');
    assert.equal(out.ten, 'Live −0:10');
});

test('paused: duration stops updating, but the live point moves on with the clock', () => {
    const out = runBrs(LIB, `
        s = bowtie_live_newState()
        bowtie_live_observe(s, 112, ${T0})
        ' The Video reports the same duration all through the pause.
        bowtie_live_observe(s, 112, ${T0} + 20)
        out.paused = bowtie_live_behindSec(s, 92, ${T0} + 20)
        ' A refresh after resuming: the new duration is the new reference.
        bowtie_live_observe(s, 136, ${T0} + 24)
        out.after = bowtie_live_behindSec(s, 94, ${T0} + 24)
    `);
    assert.equal(out.paused, 20);
    assert.equal(out.after, 22);
});

test('Rewind goes back 10 s; presses faster than the seek add up', () => {
    const out = runBrs(LIB, `
        s = bowtie_live_newState()
        bowtie_live_observe(s, 112, ${T0})
        out.a = bowtie_live_skip(s, 92, -10, ${T0})
        out.b = bowtie_live_skip(s, 92, -10, ${T0})
        out.behind = bowtie_live_behindSec(s, 92, ${T0})
        bowtie_live_seekLanded(s)
        out.landed = bowtie_live_behindSec(s, 72, ${T0})
        z = bowtie_live_newState()
        out.floor = bowtie_live_skip(z, 4, -10, ${T0})
    `);
    assert.deepEqual(out.a, { target: 82, live: false });
    assert.deepEqual(out.b, { target: 72, live: false });
    assert.equal(out.behind, 20);
    assert.equal(out.landed, 20);
    assert.deepEqual(out.floor, { target: 0, live: false });
});

test('Fast Forward never passes live: a skip that would reach it goes to live', () => {
    const out = runBrs(LIB, `
        s = bowtie_live_newState()
        bowtie_live_observe(s, 112, ${T0})
        out.ff = bowtie_live_skip(s, 62, 10, ${T0})
        bowtie_live_seekLanded(s)
        out.ten = bowtie_live_skip(s, 72, 10, ${T0})
        bowtie_live_seekLanded(s)
        out.edge = bowtie_live_skip(s, 82, 10, ${T0} + 5)
        out.after = bowtie_live_behindSec(s, 82, ${T0} + 5)
    `);
    assert.deepEqual(out.ff, { target: 72, live: false });
    assert.deepEqual(out.ten, { target: 82, live: false });
    assert.deepEqual(out.edge, { target: 97, live: true });
    assert.equal(out.after, 0);
});

test('a stream joined mid-window starts at live; one already there stays', () => {
    const out = runBrs(LIB, `
        s = bowtie_live_newState()
        bowtie_live_observe(s, 136, ${T0})
        out.joined = bowtie_live_startTarget(s, 7, ${T0})
        f = bowtie_live_newState()
        bowtie_live_observe(f, 20, ${T0})
        out.fresh = bowtie_live_startTarget(f, 0.5, ${T0})
    `);
    assert.equal(out.joined, 116);
    assert.equal(out.fresh, -1);
});

test('reset forgets the old stream', () => {
    const out = runBrs(LIB, `
        s = bowtie_live_newState()
        bowtie_live_observe(s, 500, ${T0})
        bowtie_live_skip(s, 400, -10, ${T0})
        bowtie_live_reset(s)
        out.b = bowtie_live_behindSec(s, 3, ${T0} + 60)
        out.k = bowtie_live_known(s)
    `);
    assert.equal(out.b, 0);
    assert.equal(out.k, false);
});
