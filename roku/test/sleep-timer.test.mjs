// Sleep timer pure logic (source/lib/SleepTimer.bs), run under brs. The clock
// is injected: every call takes nowSec, so these tests just pass numbers.
// Options, labels and rules mirror android/core SleepTimer.kt.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { runBrs } from './brs-run.mjs';

const LIB = ['source/lib/SleepTimer.brs'];
const T0 = 1791100000; // any epoch second

test('options: Off, 15/30/45/60/90 minutes, 2 hours; End of this program only with a future end', () => {
    const out = runBrs(LIB, `
        out.none = bowtie_sleep_options(0, ${T0})
        out.invalid = bowtie_sleep_options(invalid, ${T0})
        out.past = bowtie_sleep_options(${T0 - 5}, ${T0})
        out.ahead = bowtie_sleep_options(${T0 + 600}, ${T0})
    `);
    const labels = (l) => l.map((o) => o.label);
    const base = ['Off', '15 minutes', '30 minutes', '45 minutes', '60 minutes', '90 minutes', '2 hours'];
    assert.deepEqual(labels(out.none), base);
    assert.deepEqual(labels(out.invalid), base);
    assert.deepEqual(labels(out.past), base);
    assert.deepEqual(labels(out.ahead), [...base, 'End of this program']);
});

test('remaining time counts down with the injected clock', () => {
    const out = runBrs(LIB, `
        s = bowtie_sleep_newState()
        out.off = bowtie_sleep_secondsLeft(s, ${T0})
        out.offText = bowtie_sleep_statusText(s, ${T0})
        out.ok = bowtie_sleep_start(s, "30", ${T0}, 0)
        out.at0 = bowtie_sleep_secondsLeft(s, ${T0})
        out.later = bowtie_sleep_secondsLeft(s, ${T0 + 19})
        out.text = bowtie_sleep_statusText(s, ${T0 + 19})
        s2 = bowtie_sleep_newState()
        bowtie_sleep_start(s2, "120", ${T0}, 0)
        out.two = bowtie_sleep_secondsLeft(s2, ${T0})
        out.twoText = bowtie_sleep_format(bowtie_sleep_secondsLeft(s2, ${T0 + 3300}))
    `);
    assert.equal(out.off, -1);
    assert.equal(out.offtext, 'Sleep timer: Off');
    assert.equal(out.ok, true);
    assert.equal(out.at0, 1800);
    assert.equal(out.later, 1781);
    assert.equal(out.text, 'Sleep timer: sleeping in 29:41');
    assert.equal(out.two, 7200);
    assert.equal(out.twotext, '1:05:00');
});

test('warning shows in the last minute (T-60 s), not before', () => {
    const out = runBrs(LIB, `
        s = bowtie_sleep_newState()
        bowtie_sleep_start(s, "15", ${T0}, 0)
        out.t = []
        for each dt in [0, 839, 840, 899]
            out.t.push(bowtie_sleep_tick(s, ${T0} + dt))
        end for
        out.prompt = bowtie_sleep_promptText(60)
    `);
    assert.deepEqual(out.t.map((r) => r.warning), [false, false, true, true]);
    assert.deepEqual(out.t.map((r) => r.remaining), [900, 61, 60, 1]);
    assert.ok(out.t.every((r) => r.fired === false));
    assert.equal(out.prompt, 'Still watching? Sleeping in 1:00 — OK to keep watching');
});

test('extend adds the chosen duration to the deadline and clears the warning', () => {
    const out = runBrs(LIB, `
        s = bowtie_sleep_newState()
        bowtie_sleep_start(s, "45", ${T0}, 0)
        out.before = bowtie_sleep_tick(s, ${T0 + 2690})
        bowtie_sleep_extend(s)
        out.after = bowtie_sleep_tick(s, ${T0 + 2690})
        e = bowtie_sleep_newState()
        bowtie_sleep_start(e, "end", ${T0}, ${T0 + 600})
        bowtie_sleep_extend(e)
        out.endLeft = bowtie_sleep_secondsLeft(e, ${T0})
        o = bowtie_sleep_newState()
        bowtie_sleep_extend(o)
        out.offLeft = bowtie_sleep_secondsLeft(o, ${T0})
    `);
    assert.equal(out.before.warning, true);
    assert.equal(out.before.remaining, 10);
    assert.equal(out.after.warning, false);
    assert.equal(out.after.remaining, 10 + 45 * 60);
    // End of this program: 30 more minutes.
    assert.equal(out.endleft, 600 + 1800);
    // Nothing to extend when off.
    assert.equal(out.offleft, -1);
});

test('cancel (and choosing Off) turns it off', () => {
    const out = runBrs(LIB, `
        s = bowtie_sleep_newState()
        bowtie_sleep_start(s, "60", ${T0}, 0)
        bowtie_sleep_cancel(s)
        out.a = bowtie_sleep_tick(s, ${T0 + 3600})
        bowtie_sleep_start(s, "60", ${T0}, 0)
        out.ok = bowtie_sleep_start(s, "off", ${T0}, 0)
        out.b = bowtie_sleep_tick(s, ${T0 + 3600})
        out.option = s.option
    `);
    assert.deepEqual(out.a, { fired: false, warning: false, remaining: -1 });
    assert.equal(out.ok, true);
    assert.deepEqual(out.b, { fired: false, warning: false, remaining: -1 });
    assert.equal(out.option, 'off');
});

test('end of program: deadline is the program end', () => {
    const out = runBrs(LIB, `
        s = bowtie_sleep_newState()
        out.ok = bowtie_sleep_start(s, "end", ${T0}, ${T0 + 1234})
        out.left = bowtie_sleep_secondsLeft(s, ${T0})
        out.warn = bowtie_sleep_tick(s, ${T0 + 1200})
        out.fire = bowtie_sleep_tick(s, ${T0 + 1234})
        out.labels = bowtie_sleep_buttonLabels(bowtie_sleep_options(${T0 + 1234 + 99}, ${T0}), { option: "end" })
    `);
    assert.equal(out.ok, true);
    assert.equal(out.left, 1234);
    assert.equal(out.warn.warning, true);
    assert.equal(out.fire.fired, true);
    assert.equal(out.labels[7], '✓ End of this program');
    assert.equal(out.labels[0], 'Off');
});

test('end of program is unavailable without an end time (start refuses, stays off)', () => {
    const out = runBrs(LIB, `
        out.r = []
        for each endSec in [0, invalid, ${T0}, ${T0 - 60}, "${T0 + 600}"]
            s = bowtie_sleep_newState()
            bowtie_sleep_start(s, "30", ${T0}, 0)
            ok = bowtie_sleep_start(s, "end", ${T0}, endSec)
            out.r.push({ ok: ok, left: bowtie_sleep_secondsLeft(s, ${T0}) })
        end for
    `);
    for (const r of out.r) {
        assert.equal(r.ok, false);
        assert.equal(r.left, -1);
    }
});

test('fires exactly once, then stays off', () => {
    const out = runBrs(LIB, `
        s = bowtie_sleep_newState()
        bowtie_sleep_start(s, "15", ${T0}, 0)
        out.ticks = []
        for each dt in [899, 900, 901, 960]
            out.ticks.push(bowtie_sleep_tick(s, ${T0} + dt))
        end for
        s2 = bowtie_sleep_newState()
        bowtie_sleep_start(s2, "15", ${T0}, 0)
        ' A late tick (the box was busy) still fires once.
        out.late = bowtie_sleep_tick(s2, ${T0 + 5000})
        out.again = bowtie_sleep_tick(s2, ${T0 + 5001})
    `);
    assert.deepEqual(out.ticks.map((t) => t.fired), [false, true, false, false]);
    assert.equal(out.late.fired, true);
    assert.equal(out.again.fired, false);
});

test('format: m:ss under an hour, h:mm:ss above, never negative', () => {
    const out = runBrs(LIB, `
        f = bowtie_sleep_format
        out.t = [f(0), f(1), f(60), f(599), f(1781), f(3600), f(3900), f(-5)]
    `);
    assert.deepEqual(out.t, ['0:00', '0:01', '1:00', '9:59', '29:41', '1:00:00', '1:05:00', '0:00']);
});
