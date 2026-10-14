// Live TV rewind pure logic (source/lib/LiveSeek.bs), run under brs. The
// clock is injected: calls take atSec, so these tests just pass numbers.
// Labels and the 10 s live tolerance mirror ios/BowtieKit LiveEdge.swift.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { runBrs } from './brs-run.mjs';

const LIB = ['source/lib/LiveSeek.brs'];
const T0 = 1791100000;

test('a new stream is live', () => {
    const out = runBrs(LIB, `
        s = bowtie_live_newState()
        out.behind = bowtie_live_behindSec(s, ${T0})
        out.label = bowtie_live_label(0)
        out.spoken = bowtie_live_spokenText(0)
    `);
    assert.equal(out.behind, 0);
    assert.equal(out.label, 'Live');
    assert.equal(out.spoken, 'Watching live');
});

test('Rewind goes back 10 s at a time and the label counts it', () => {
    const out = runBrs(LIB, `
        s = bowtie_live_newState()
        out.a = bowtie_live_skip(s, 100, -10, ${T0})
        bowtie_live_seekLanded(s, 90)
        out.b = bowtie_live_skip(s, 90, -10, ${T0} + 1)
        bowtie_live_seekLanded(s, 80)
        out.behind = bowtie_live_behindSec(s, ${T0} + 2)
        out.label = bowtie_live_label(out.behind)
        out.long = bowtie_live_label(90)
        out.spoken = bowtie_live_spokenText(90)
        out.one = bowtie_live_spokenText(61)
    `);
    assert.deepEqual(out.a, { target: 90, live: false });
    assert.deepEqual(out.b, { target: 80, live: false });
    assert.equal(out.behind, 20);
    assert.equal(out.label, 'Live −0:20');
    assert.equal(out.long, 'Live −1:30');
    assert.equal(out.spoken, '1 minute 30 seconds behind live');
    assert.equal(out.one, '1 minute 1 second behind live');
});

test('within 10 s of live still reads as live', () => {
    const out = runBrs(LIB, `
        out.nine = bowtie_live_label(9)
        out.ten = bowtie_live_label(10)
    `);
    assert.equal(out.nine, 'Live');
    assert.equal(out.ten, 'Live −0:10');
});

test('Fast Forward never passes live: the last skip goes to the live point', () => {
    const out = runBrs(LIB, `
        s = bowtie_live_newState()
        bowtie_live_skip(s, 100, -10, ${T0})
        bowtie_live_seekLanded(s, 90)
        bowtie_live_skip(s, 90, -10, ${T0})
        bowtie_live_seekLanded(s, 80)
        bowtie_live_skip(s, 80, -10, ${T0})
        bowtie_live_seekLanded(s, 70)
        out.ff = bowtie_live_skip(s, 70, 10, ${T0})
        bowtie_live_seekLanded(s, 80)
        out.mid = bowtie_live_behindSec(s, ${T0})
        out.ten = bowtie_live_skip(s, 80, 10, ${T0})
        bowtie_live_seekLanded(s, 90)
        out.last = bowtie_live_skip(s, 90, 10, ${T0})
        out.after = bowtie_live_behindSec(s, ${T0})
        fresh = bowtie_live_newState()
        out.atlive = bowtie_live_skip(fresh, 50, 10, ${T0})
    `);
    assert.deepEqual(out.ff, { target: 80, live: false });
    assert.equal(out.mid, 20);
    assert.deepEqual(out.ten, { target: 90, live: false });
    assert.deepEqual(out.last, { target: 100, live: true });
    assert.equal(out.after, 0);
    assert.deepEqual(out.atlive, { target: 50, live: true });
});

test('time paused counts as behind, and Go to live seeks forward by it', () => {
    const out = runBrs(LIB, `
        s = bowtie_live_newState()
        bowtie_live_paused(s, ${T0})
        out.during = bowtie_live_behindSec(s, ${T0} + 45)
        bowtie_live_resumed(s, ${T0} + 45)
        out.after = bowtie_live_behindSec(s, ${T0} + 100)
        out.go = bowtie_live_goLive(s, 200, ${T0} + 100)
        out.live = bowtie_live_behindSec(s, ${T0} + 100)
    `);
    assert.equal(out.during, 45);
    assert.equal(out.after, 45);
    assert.deepEqual(out.go, { target: 245, live: true });
    assert.equal(out.live, 0);
});

test('a skip while paused keeps it paused and keeps counting', () => {
    const out = runBrs(LIB, `
        s = bowtie_live_newState()
        bowtie_live_paused(s, ${T0})
        bowtie_live_skip(s, 100, -10, ${T0} + 30)
        bowtie_live_seekLanded(s, 90)
        out.b = bowtie_live_behindSec(s, ${T0} + 35)
    `);
    assert.equal(out.b, 45);
});

test('a rewind cut short at the start of the window counts only what it moved', () => {
    const out = runBrs(LIB, `
        s = bowtie_live_newState()
        bowtie_live_skip(s, 5, -10, ${T0})
        out.clampedZero = bowtie_live_behindSec(s, ${T0})
        t = bowtie_live_newState()
        bowtie_live_skip(t, 100, -10, ${T0})
        bowtie_live_seekLanded(t, 97)
        out.cut = bowtie_live_behindSec(t, ${T0})
    `);
    assert.equal(out.clampedzero, 5);
    assert.equal(out.cut, 3);
});

test('presses faster than the player seeks add up from the seek under way', () => {
    const out = runBrs(LIB, `
        s = bowtie_live_newState()
        out.a = bowtie_live_skip(s, 100, -10, ${T0})
        out.b = bowtie_live_skip(s, 100, -10, ${T0})
        out.c = bowtie_live_skip(s, 100, -10, ${T0})
        bowtie_live_seekLanded(s, 70)
        out.behind = bowtie_live_behindSec(s, ${T0})
    `);
    assert.equal(out.a.target, 90);
    assert.equal(out.b.target, 80);
    assert.equal(out.c.target, 70);
    assert.equal(out.behind, 30);
});

test('reset is back at live', () => {
    const out = runBrs(LIB, `
        s = bowtie_live_newState()
        bowtie_live_paused(s, ${T0})
        bowtie_live_skip(s, 100, -10, ${T0} + 5)
        bowtie_live_reset(s)
        out.b = bowtie_live_behindSec(s, ${T0} + 60)
    `);
    assert.equal(out.b, 0);
});
