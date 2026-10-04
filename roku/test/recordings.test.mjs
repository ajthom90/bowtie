// Recordings pure logic (source/lib/Recordings.bs), run under brs. Strings
// mirror web/src/recordings/recordingsModel.ts so every app says the same.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { runBrs } from './brs-run.mjs';

const LIB = ['source/lib/Recordings.brs'];

// A JSON value as a BrightScript expression.
const bs = (v) => `ParseJson("${JSON.stringify(v).replace(/"/g, '""')}")`;

const rec = (over = {}) => ({
    id: 7,
    title: 'Jeopardy!',
    subtitle: '',
    description: '',
    category: '',
    channelId: 3,
    channelName: '5.1 KSTP',
    start: '2026-10-05T00:00:00Z',
    stop: '2026-10-05T00:30:00Z',
    state: 'ready',
    partial: false,
    failure: '',
    failureDetail: '',
    durationSec: 1980,
    sizeBytes: 3800000000,
    protected: false,
    positionSec: 0,
    scheduledBy: 'andrew',
    canManage: true,
    ...over,
});

test('tabs: Upcoming / Recorded / Missed / Shows, Missed queries failed', () => {
    const out = runBrs(LIB, `
        out.ids = bowtie_recordings_tabIds()
        out.labels = [bowtie_recordings_tabLabel("upcoming"), bowtie_recordings_tabLabel("recorded"), bowtie_recordings_tabLabel("missed"), bowtie_recordings_tabLabel("shows")]
        out.queries = [bowtie_recordings_tabQuery("upcoming"), bowtie_recordings_tabQuery("recorded"), bowtie_recordings_tabQuery("missed")]
        out.empty = [bowtie_recordings_emptyText("upcoming"), bowtie_recordings_emptyText("recorded"), bowtie_recordings_emptyText("missed"), bowtie_recordings_emptyText("shows")]
        out.rules = [bowtie_recordings_isRulesTab("shows"), bowtie_recordings_isRulesTab("upcoming")]
    `);
    assert.deepEqual(out.ids, ['upcoming', 'recorded', 'missed', 'shows']);
    assert.deepEqual(out.labels, ['Upcoming', 'Recorded', 'Missed', 'Shows']);
    assert.deepEqual(out.queries, ['upcoming', 'recorded', 'failed']);
    assert.equal(out.empty[0], 'Nothing scheduled. Press * on a channel and choose Record.');
    assert.equal(out.empty[1], 'No recordings yet.');
    assert.equal(out.empty[2], 'No missed recordings.');
    assert.equal(out.empty[3], 'No shows yet. Press * on a channel and choose Record series.');
    assert.deepEqual(out.rules, [true, false]);
});

test('tabOf maps every state; filterForTab keeps only that tab, in order', () => {
    const list = [
        rec({ id: 1, state: 'scheduled' }),
        rec({ id: 2, state: 'ready' }),
        rec({ id: 3, state: 'waiting' }),
        rec({ id: 4, state: 'failed' }),
        rec({ id: 5, state: 'recording' }),
        rec({ id: 6, state: 'converting' }),
        rec({ id: 8, state: 'cancelled' }),
    ];
    const out = runBrs(LIB, `
        out.states = []
        for each s in ["scheduled", "waiting", "recording", "converting", "ready", "failed", "cancelled", ""]
            out.states.push(bowtie_recordings_tabOf(s))
        end for
        l = ${bs(list)}
        out.up = bowtie_recordings_filterForTab(l, "upcoming")
        out.rec = bowtie_recordings_filterForTab(l, "recorded")
        out.miss = bowtie_recordings_filterForTab(l, "missed")
        out.none = bowtie_recordings_filterForTab(invalid, "missed")
    `);
    assert.deepEqual(out.states, ['upcoming', 'upcoming', 'upcoming', 'recorded', 'recorded', 'missed', '', '']);
    assert.deepEqual(out.up.map((r) => r.id), [1, 3, 5]);
    assert.deepEqual(out.rec.map((r) => r.id), [2, 6]);
    assert.deepEqual(out.miss.map((r) => r.id), [4]);
    assert.deepEqual(out.none, []);
});

test('parseList fills missing fields with safe defaults and drops junk', () => {
    const out = runBrs(LIB, `
        out.list = bowtie_recordings_parseList(${bs([{ id: 7, title: 'X', state: 'ready' }, 'junk', { title: 'no id' }])})
        out.bad = bowtie_recordings_parseList(${bs({ error: 'nope' })})
    `);
    assert.equal(out.list.length, 1);
    const r = out.list[0];
    assert.equal(r.id, 7);
    assert.equal(r.title, 'X');
    assert.equal(r.channelName, '');
    assert.equal(r.canManage, false);
    assert.equal(r.protected, false);
    assert.equal(r.durationSec, 0);
    assert.equal(r.sizeBytes, 0);
    assert.equal(r.positionSec, 0);
    assert.equal(r.failure, '');
    assert.deepEqual(out.bad, []);
});

test('failureText: plain words; error falls back to the detail', () => {
    const out = runBrs(LIB, `
        f = bowtie_recordings_failureText
        out.t = [f("noTuner", ""), f("noSignal", ""), f("diskFull", ""), f("error", "  ffmpeg exited 1 "), f("error", ""), f("", "x"), f("other", "")]
    `);
    assert.deepEqual(out.t, ['No tuner was free', 'No signal', 'Disk full', 'ffmpeg exited 1', 'Something went wrong', '', '']);
});

test('statusText: state badges, Partial, and failure reasons', () => {
    const out = runBrs(LIB, `
        s = bowtie_recordings_statusText
        out.t = [
            s(${bs(rec({ state: 'scheduled' }))}),
            s(${bs(rec({ state: 'waiting' }))}),
            s(${bs(rec({ state: 'recording' }))}),
            s(${bs(rec({ state: 'converting', partial: true }))}),
            s(${bs(rec({ state: 'ready' }))}),
            s(${bs(rec({ state: 'ready', partial: true }))}),
            s(${bs(rec({ state: 'failed', failure: 'noTuner' }))}),
        ]
    `);
    assert.deepEqual(out.t, [
        'Scheduled',
        'Waiting for a tuner',
        'Recording now',
        'Converting · Partial',
        '',
        'Partial',
        'Missed: No tuner was free',
    ]);
});

test('resumeDecision: resume only past 10 s and before the last 30 s', () => {
    const out = runBrs(LIB, `
        d = bowtie_recordings_resumeDecision
        out.t = [d(0, 1980), d(10, 1980), d(11, 1980), d(1949, 1980), d(1950, 1980), d(2000, 1980), d(400, 0)]
    `);
    const kinds = out.t.map((x) => x.kind);
    assert.deepEqual(kinds, ['start', 'start', 'ask', 'ask', 'start', 'start', 'ask']);
    assert.equal(out.t[2].positionSec, 11);
    assert.equal(out.t[0].positionSec, 0);
    // Unknown duration: any position past 10 s is offered.
    assert.equal(out.t[6].positionSec, 400);
});

test('formatDuration / formatSize / formatClock', () => {
    const out = runBrs(LIB, `
        out.d = [bowtie_recordings_formatDuration(0), bowtie_recordings_formatDuration(45), bowtie_recordings_formatDuration(1980), bowtie_recordings_formatDuration(3600), bowtie_recordings_formatDuration(5400), bowtie_recordings_formatDuration(-3)]
        out.s = [bowtie_recordings_formatSize(0), bowtie_recordings_formatSize(512), bowtie_recordings_formatSize(1500), bowtie_recordings_formatSize(3800000000), bowtie_recordings_formatSize(12400000000), bowtie_recordings_formatSize(2000000)]
        out.c = [bowtie_recordings_formatClock(5), bowtie_recordings_formatClock(754), bowtie_recordings_formatClock(3723), bowtie_recordings_formatClock(-1)]
    `);
    assert.deepEqual(out.d, ['—', '<1 min', '33 min', '1 h', '1 h 30 min', '—']);
    assert.deepEqual(out.s, ['—', '512 B', '1.5 KB', '3.8 GB', '12 GB', '2 MB']);
    assert.deepEqual(out.c, ['0:05', '12:34', '1:02:03', '0:00']);
});

test('formatSize handles sizes past 2^31 (LongInteger from ParseJson)', () => {
    const out = runBrs(LIB, `
        out.s = bowtie_recordings_formatSize(ParseJson("7400000000"))
    `);
    assert.equal(out.s, '7.4 GB');
});

test('formatTimeRange / formatWhen use the given UTC offset', () => {
    // 00:00Z = 7:00 PM CDT the previous day (offset -5 h).
    const cdt = -5 * 3600;
    const out = runBrs(LIB, `
        out.r1 = bowtie_recordings_formatTimeRange("2026-10-05T00:00:00Z", "2026-10-05T00:30:00Z", ${cdt})
        out.r2 = bowtie_recordings_formatTimeRange("2026-10-04T16:30:00Z", "2026-10-04T17:30:00Z", ${cdt})
        out.r3 = bowtie_recordings_formatTimeRange("2026-10-04T05:00:00Z", "2026-10-04T06:00:00Z", 0)
        now = "2026-10-04T15:00:00Z"
        out.today = bowtie_recordings_formatWhen("2026-10-05T00:00:00Z", "2026-10-05T00:30:00Z", now, ${cdt})
        out.tomorrow = bowtie_recordings_formatWhen("2026-10-05T14:00:00Z", "2026-10-05T15:00:00Z", now, ${cdt})
        out.yesterday = bowtie_recordings_formatWhen("2026-10-03T14:00:00Z", "2026-10-03T15:00:00Z", now, ${cdt})
        out.later = bowtie_recordings_formatWhen("2026-10-08T00:00:00Z", "2026-10-08T00:30:00Z", now, ${cdt})
    `);
    assert.equal(out.r1, '7:00–7:30 PM');
    assert.equal(out.r2, '11:30 AM–12:30 PM');
    assert.equal(out.r3, '5:00–6:00 AM');
    assert.equal(out.today, 'Today 7:00–7:30 PM');
    assert.equal(out.tomorrow, 'Tomorrow 9:00–10:00 AM');
    assert.equal(out.yesterday, 'Yesterday 9:00–10:00 AM');
    assert.equal(out.later, 'Wed Oct 7 7:00–7:30 PM');
});

test('rowActions: play when ready; stop/cancel/delete/keep only with canManage', () => {
    const out = runBrs(LIB, `
        a = bowtie_recordings_rowActions
        out.ready = a(${bs(rec({ state: 'ready' }))})
        out.sched = a(${bs(rec({ state: 'scheduled' }))})
        out.live = a(${bs(rec({ state: 'recording' }))})
        out.conv = a(${bs(rec({ state: 'converting' }))})
        out.failed = a(${bs(rec({ state: 'failed' }))})
        out.other = a(${bs(rec({ state: 'ready', canManage: false }))})
    `);
    assert.deepEqual(out.ready, { play: true, stop: false, remove: 'delete', keep: true });
    assert.deepEqual(out.sched, { play: false, stop: false, remove: 'cancel', keep: false });
    assert.deepEqual(out.live, { play: false, stop: true, remove: 'delete', keep: false });
    assert.deepEqual(out.conv, { play: false, stop: false, remove: 'delete', keep: true });
    assert.deepEqual(out.failed, { play: false, stop: false, remove: 'delete', keep: false });
    assert.deepEqual(out.other, { play: true, stop: false, remove: '', keep: false });
});

test('optionButtons: the * menu for a recording', () => {
    const out = runBrs(LIB, `
        b = bowtie_recordings_optionButtons
        out.ready = b(${bs(rec({ state: 'ready' }))})
        out.kept = b(${bs(rec({ state: 'ready', protected: true }))})
        out.sched = b(${bs(rec({ state: 'waiting' }))})
        out.live = b(${bs(rec({ state: 'recording' }))})
        out.other = b(${bs(rec({ state: 'ready', canManage: false }))})
    `);
    const labels = (bs) => bs.map((b) => b.label);
    const ids = (bs) => bs.map((b) => b.id);
    assert.deepEqual(labels(out.ready), ['Keep', 'Delete', 'Close']);
    assert.deepEqual(ids(out.ready), ['keep', 'delete', 'close']);
    assert.deepEqual(labels(out.kept), ["Don't keep", 'Delete', 'Close']);
    assert.deepEqual(ids(out.kept), ['unkeep', 'delete', 'close']);
    assert.deepEqual(labels(out.sched), ['Cancel recording', 'Close']);
    assert.deepEqual(ids(out.sched), ['cancel', 'close']);
    assert.deepEqual(labels(out.live), ['Stop recording', 'Delete', 'Close']);
    assert.deepEqual(ids(out.live), ['stop', 'delete', 'close']);
    // Not the scheduler and not an admin: nothing to offer.
    assert.deepEqual(out.other, []);
});

test('buttonLabels and buttonId turn a menu into dialog buttons and back', () => {
    const out = runBrs(LIB, `
        menu = bowtie_recordings_optionButtons(${bs(rec({ state: 'ready' }))})
        out.labels = bowtie_recordings_buttonLabels(menu)
        out.ids = [bowtie_recordings_buttonId(menu, 0), bowtie_recordings_buttonId(menu, 2), bowtie_recordings_buttonId(menu, 3), bowtie_recordings_buttonId(menu, -1), bowtie_recordings_buttonId(menu, invalid)]
    `);
    assert.deepEqual(out.labels, ['Keep', 'Delete', 'Close']);
    assert.deepEqual(out.ids, ['keep', 'close', '', '', '']);
});

test('deleteConfirmText warns it removes the recording for everyone', () => {
    const out = runBrs(LIB, `
        out.t = bowtie_recordings_deleteConfirmText("Jeopardy!")
    `);
    assert.equal(out.t, 'Delete “Jeopardy!”? This removes the recording for everyone.');
});

test('homeOptions: favorite toggle, record when there is a current program, cancel', () => {
    const now = { start: '2026-10-05T00:00:00Z', stop: '2026-10-05T00:30:00Z', title: 'News' };
    const out = runBrs(LIB, `
        h = bowtie_recordings_homeOptions
        out.full = h(true, false, ${bs(now)})
        out.starred = h(true, true, ${bs(now)})
        out.noguide = h(true, false, invalid)
        out.oldserver = h(false, false, ${bs(now)})
        out.scheduled = h(true, false, ${bs({ ...now, recording: { id: 9, state: 'scheduled' } })})
        out.recnow = h(true, false, ${bs({ ...now, recording: { id: 9, state: 'recording' } })})
    `);
    const labels = (o) => o.buttons.map((b) => b.label);
    assert.deepEqual(labels(out.full), ['Favorite', 'Record this program', 'Record series', 'Cancel']);
    assert.deepEqual(out.full.buttons.map((b) => b.id), ['favorite', 'record', 'series', 'cancel']);
    assert.equal(out.full.note, 'Now: News');
    assert.deepEqual(labels(out.starred), ['Unfavorite', 'Record this program', 'Record series', 'Cancel']);
    assert.deepEqual(labels(out.noguide), ['Favorite', 'Cancel']);
    assert.equal(out.noguide.note, '');
    // A server without favorites has no star to toggle.
    assert.deepEqual(labels(out.oldserver), ['Record this program', 'Record series', 'Cancel']);
    // Already scheduled or recording: no second Record, say so instead.
    assert.deepEqual(labels(out.scheduled), ['Favorite', 'Record series', 'Cancel']);
    assert.equal(out.scheduled.note, 'Now: News · Recording scheduled');
    assert.equal(out.recnow.note, 'Now: News · Recording now');
});

test('scheduledMessage: confirmation plus any warnings', () => {
    const out = runBrs(LIB, `
        out.plain = bowtie_recordings_scheduledMessage(${bs({ recording: { title: 'News' }, warnings: [] })})
        out.warn = bowtie_recordings_scheduledMessage(${bs({ recording: { title: 'News' }, warnings: [{ code: 'usesAllTuners', message: 'If Plex is using a tuner then, this may not record.' }] })})
        out.bare = bowtie_recordings_scheduledMessage(invalid)
    `);
    assert.deepEqual(out.plain, ['Recording “News”.']);
    assert.deepEqual(out.warn, ['Recording “News”.', 'If Plex is using a tuner then, this may not record.']);
    assert.deepEqual(out.bare, ['Recording scheduled.']);
});

test('conflictMessage: server text (or a heading) then one line per conflict', () => {
    const conflicts = [
        rec({ id: 1, title: 'A', channelName: '5.1 KSTP', start: '2026-10-05T00:00:00Z', stop: '2026-10-05T00:30:00Z' }),
        rec({ id: 2, title: 'B', channelName: '9.1 FOX9', start: '2026-10-05T00:00:00Z', stop: '2026-10-05T01:00:00Z' }),
    ];
    const out = runBrs(LIB, `
        out.server = bowtie_recordings_conflictMessage(${bs({ message: 'Only 2 tuners: other recordings already need them then.', tunerCount: 2, conflicts })}, ${-5 * 3600})
        out.heading = bowtie_recordings_conflictMessage(${bs({ message: '', tunerCount: 1, conflicts: [] })}, 0)
        out.two = bowtie_recordings_conflictHeading(2)
    `);
    assert.deepEqual(out.server, [
        'Only 2 tuners: other recordings already need them then.',
        'A · 5.1 KSTP · 7:00–7:30 PM',
        'B · 9.1 FOX9 · 7:00–8:00 PM',
    ]);
    assert.deepEqual(out.heading, ['Only 1 tuner — these recordings already need it:']);
    assert.equal(out.two, 'Only 2 tuners — these recordings already need them:');
});

test('row text: title line and detail line for each tab', () => {
    const now = '2026-10-04T15:00:00Z';
    const off = -5 * 3600;
    const out = runBrs(LIB, `
        d = bowtie_recordings_detailLine
        out.ready = d(${bs(rec({ subtitle: 'Teen Tournament' }))}, "${now}", ${off})
        out.sched = d(${bs(rec({ state: 'scheduled', durationSec: 0, sizeBytes: 0 }))}, "${now}", ${off})
        out.missed = d(${bs(rec({ state: 'failed', failure: 'noSignal', durationSec: 0, sizeBytes: 0 }))}, "${now}", ${off})
        out.title = bowtie_recordings_titleLine(${bs(rec({ subtitle: 'Teen Tournament' }))})
        out.kept = bowtie_recordings_titleLine(${bs(rec({ protected: true }))})
    `);
    assert.equal(out.title, 'Jeopardy! — Teen Tournament');
    assert.equal(out.kept, 'Jeopardy! · Kept');
    assert.equal(out.ready, 'Today 7:00–7:30 PM · 5.1 KSTP · 33 min · 3.8 GB');
    assert.equal(out.sched, 'Today 7:00–7:30 PM · 5.1 KSTP · Scheduled');
    assert.equal(out.missed, 'Today 7:00–7:30 PM · 5.1 KSTP · Missed: No signal');
});

// ── Series ──────────────────────────────────────────────────────────────────

const rule = (over = {}) => ({
    id: 5,
    title: 'Jeopardy!',
    seriesId: 'SH01',
    channelId: 3,
    channelName: '5.1 KSTP',
    newOnly: true,
    keepLatest: 0,
    scheduledBy: 'andrew',
    canManage: true,
    createdAt: '2026-10-04T00:00:00Z',
    ...over,
});

test('series rows: ruleId > 0 says Series; a skipped episode says Skipped, not Missed', () => {
    const now = '2026-10-04T15:00:00Z';
    const off = -5 * 3600;
    const out = runBrs(LIB, `
        d = bowtie_recordings_detailLine
        s = bowtie_recordings_statusText
        out.series = d(${bs(rec({ state: 'scheduled', ruleId: 5, durationSec: 0, sizeBytes: 0 }))}, "${now}", ${off})
        out.oneoff = d(${bs(rec({ state: 'scheduled', ruleId: 0, durationSec: 0, sizeBytes: 0 }))}, "${now}", ${off})
        out.skipped = s(${bs(rec({ state: 'failed', failure: 'skipped', failureDetail: 'Skipped', ruleId: 5 }))})
        out.skipline = d(${bs(rec({ state: 'failed', failure: 'skipped', failureDetail: 'Skipped', ruleId: 5, durationSec: 0, sizeBytes: 0 }))}, "${now}", ${off})
        out.text = bowtie_recordings_failureText("skipped", "Skipped")
        out.tint = [bowtie_recordings_missedTint(${bs(rec({ state: 'failed', failure: 'noTuner' }))}), bowtie_recordings_missedTint(${bs(rec({ state: 'failed', failure: 'skipped' }))}), bowtie_recordings_missedTint(${bs(rec())})]
        l = bowtie_recordings_parseList(${bs([rec({ ruleId: 5 }), rec({ id: 8 })])})
        out.ruleids = [l[0].ruleId, l[1].ruleId]
    `);
    assert.equal(out.series, 'Today 7:00–7:30 PM · 5.1 KSTP · Series · Scheduled');
    assert.equal(out.oneoff, 'Today 7:00–7:30 PM · 5.1 KSTP · Scheduled');
    assert.equal(out.skipped, 'Skipped');
    assert.equal(out.skipline, 'Today 7:00–7:30 PM · 5.1 KSTP · Series · Skipped');
    assert.equal(out.text, 'Skipped');
    // Red is for real misses; a skip was the user's own choice.
    assert.deepEqual(out.tint, [true, false, false]);
    assert.deepEqual(out.ruleids, [5, 0]);
});

test('homeOptions: Record series next to Record this program, even once this airing is marked', () => {
    const now = { start: '2026-10-05T00:00:00Z', stop: '2026-10-05T00:30:00Z', title: 'News' };
    const out = runBrs(LIB, `
        h = bowtie_recordings_homeOptions
        out.full = h(true, false, ${bs(now)})
        out.marked = h(true, false, ${bs({ ...now, recording: { id: 9, state: 'scheduled' } })})
        out.noguide = h(true, false, invalid)
    `);
    assert.deepEqual(out.full.buttons.map((b) => b.label), ['Favorite', 'Record this program', 'Record series', 'Cancel']);
    assert.deepEqual(out.full.buttons.map((b) => b.id), ['favorite', 'record', 'series', 'cancel']);
    assert.deepEqual(out.marked.buttons.map((b) => b.id), ['favorite', 'series', 'cancel']);
    assert.deepEqual(out.noguide.buttons.map((b) => b.id), ['favorite', 'cancel']);
});

test('seriesScheduledMessage: what is recorded, then "Scheduled N episodes"', () => {
    const out = runBrs(LIB, `
        f = bowtie_recordings_seriesScheduledMessage
        out.three = f(${bs({ rule: rule(), scheduled: 3 })})
        out.one = f(${bs({ rule: rule(), scheduled: 1 })})
        out.none = f(${bs({ rule: rule({ channelId: 0, channelName: '' }), scheduled: 0 })})
        out.bare = f(invalid)
    `);
    assert.deepEqual(out.three, ['Recording new episodes of “Jeopardy!” on 5.1 KSTP.', 'Scheduled 3 episodes.']);
    assert.deepEqual(out.one, ['Recording new episodes of “Jeopardy!” on 5.1 KSTP.', 'Scheduled 1 episode.']);
    assert.deepEqual(out.none, ['Recording new episodes of “Jeopardy!”.', 'Scheduled 0 episodes. New ones are added as the guide fills in.']);
    assert.deepEqual(out.bare, ['Scheduled 0 episodes. New ones are added as the guide fills in.']);
});

test('seriesErrorText: plain words for the ways Record series fails', () => {
    const out = runBrs(LIB, `
        e = bowtie_recordings_seriesErrorText
        out.t = [
            e({ code: 404, message: "program not found in the guide" }),
            e({ code: 404, message: "404 page not found" }),
            e({ code: 503, message: "recording is not available" }),
            e({ code: 500, message: "failed to create rule" }),
            e({ code: 0, message: "" }),
            e(invalid),
        ]
    `);
    assert.deepEqual(out.t, [
        "That program isn't in the guide anymore.",
        "Series recording isn't available on this server.",
        "Recording isn't available on this server.",
        'failed to create rule',
        "Couldn't set up the series recording.",
        "Couldn't set up the series recording.",
    ]);
});

test('rules: parseRules, title and detail lines for the Shows tab', () => {
    const out = runBrs(LIB, `
        l = bowtie_recordings_parseRules(${bs([rule(), 'junk', { title: 'no id' }, rule({ id: 6, title: 'News', channelId: 0, channelName: '', newOnly: false, keepLatest: 5, scheduledBy: '', canManage: false })])})
        out.count = l.count()
        out.first = snapshot(l[0])
        out.title = bowtie_recordings_ruleTitleLine(l[0])
        out.d1 = bowtie_recordings_ruleDetailLine(l[0])
        out.d2 = bowtie_recordings_ruleDetailLine(l[1])
        out.bad = bowtie_recordings_parseRules(${bs({ error: 'nope' })})
    `);
    assert.equal(out.count, 2);
    assert.equal(out.first.id, 5);
    assert.equal(out.first.canManage, true);
    assert.equal(out.title, 'Jeopardy!');
    assert.equal(out.d1, '5.1 KSTP · New episodes only · Set up by andrew');
    assert.equal(out.d2, 'Any channel · All episodes · Keeps the latest 5');
    assert.deepEqual(out.bad, []);
});

test('ruleOptionButtons: Stop recording this show only with canManage', () => {
    const out = runBrs(LIB, `
        out.mine = bowtie_recordings_ruleOptionButtons(${bs(rule())})
        out.theirs = bowtie_recordings_ruleOptionButtons(${bs(rule({ canManage: false }))})
        out.note = bowtie_recordings_stopRuleNote()
    `);
    assert.deepEqual(out.mine.map((b) => b.label), ['Stop recording this show', 'Close']);
    assert.deepEqual(out.mine.map((b) => b.id), ['stopRule', 'close']);
    assert.deepEqual(out.theirs, []);
    assert.equal(out.note, 'Upcoming episodes are cancelled; recorded ones stay.');
});
