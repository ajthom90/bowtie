// Guide category filters (source/lib/GuideFilter.bs), run under brs.
// The program vectors mirror web/src/guide/guideFilterModel.test.ts,
// ios/BowtieKit (GuideFilterTests) and Android :core (GuideFilterTest) so
// every app puts a program in the same buckets. The channel rule is Roku's:
// the rail shows now/next, so a channel stays under a filter when its now or
// next program matches.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { runBrs } from './brs-run.mjs';

const LIB = ['source/lib/GuideLogic.brs', 'source/lib/GuideFilter.brs'];

// A JSON value as a BrightScript expression.
const bs = (v) => `ParseJson("${JSON.stringify(v).replace(/"/g, '""')}")`;

const prog = (over = {}) => ({
    start: '2026-10-04T18:00:00Z',
    stop: '2026-10-04T19:00:00Z',
    title: 'Show',
    subtitle: '',
    description: '',
    category: '',
    ...over,
});

// Sorted bucket names for each [category, overrides] case, in one brs run.
function bucketsFor(cases) {
    const programs = cases.map(([category, over = {}]) => prog({ category, ...over }));
    const got = runBrs(LIB, `
        out.got = []
        for each p in ${bs(programs)}
            out.got.push(bowtie_guideFilter_programBuckets(p))
        end for
    `).got;
    return got.map((aa) => Object.keys(aa).filter((k) => aa[k] === true).sort());
}

function expectAll(categories, expected) {
    const got = bucketsFor(categories.map((c) => [c]));
    categories.forEach((c, i) => assert.deepEqual(got[i], expected, `"${c}"`));
}

test('sports categories', () => {
    expectAll([
        'Sports', 'Sports event', 'Sports non-event', 'Sports talk', 'SPORTS EVENT',
        'Football', 'College football', 'Basketball', 'Baseball', 'Soccer', 'Hockey',
        'Golf', 'Tennis', 'Boxing', 'Pro wrestling', 'Auto racing', 'Motorsports',
        'Figure skating', 'Track/field', 'Olympics', 'Mixed martial arts',
    ], ['sports']);
});

test('movie categories (the whole piece)', () => {
    expectAll(['Movie', 'Movies', 'movie', 'Feature Film', 'Film', 'TV Movie', 'Made-for-TV movie'], ['movies']);
});

test('news categories', () => {
    expectAll(['News', 'Newsmagazine', 'News magazine', 'Weather', 'Local news', 'Newscast'], ['news']);
});

test('kids categories', () => {
    expectAll([
        'Children', "Children's", 'Children-music', 'Children-special', 'Kids',
        'Animated', 'Animation', 'Cartoon', 'Educational',
    ], ['kids']);
});

test('categories in no bucket', () => {
    expectAll([
        '', 'Family', 'Drama', 'Sitcom', 'Comedy', 'Movie review', 'Martial arts',
        'Transportation', 'Talk', 'Series', 'Reality',
    ], []);
});

test('trims and ignores case', () => {
    assert.deepEqual(bucketsFor([['  sPoRtS eVeNt  ']]), [['sports']]);
});

test('a joined category string can land in several buckets', () => {
    assert.deepEqual(bucketsFor([['Sports talk; News'], ['Children, Animated'], ['Movie | Animated']]), [
        ['news', 'sports'],
        ['kids'],
        ['kids', 'movies'],
    ]);
});

test('Schedules Direct program ID prefixes mark movies and sports events', () => {
    assert.deepEqual(
        bucketsFor([
            ['Action', { programId: 'MV000111220000' }],
            ['', { programId: 'SP012345670123' }],
            ['Drama', { programId: 'EP012345670012' }],
            ['', { programId: 'MVP' }],
            ['', { programId: 'mv000111220000' }],
            ['', { programId: 'MV0001112' }],
        ]),
        [['movies'], ['sports'], [], [], [], []],
    );
});

test('a kids movie is in both buckets', () => {
    assert.deepEqual(bucketsFor([['Children', { programId: 'MV000111220000' }]]), [['kids', 'movies']]);
});

test('isNew adds the new bucket', () => {
    assert.deepEqual(
        bucketsFor([
            ['Sitcom', { isNew: true }],
            ['Sports event', { isNew: true }],
            ['Sitcom', { isNew: false }],
            ['Sitcom', { isNew: 'true' }],
        ]),
        [['new'], ['new', 'sports'], [], []],
    );
});

test('mature ratings keep a program out of kids (adult animation)', () => {
    assert.deepEqual(
        bucketsFor([
            ['Animated', { rating: 'TV-14' }],
            ['Animated', { rating: 'TV-MA' }],
            ['Animated', { rating: 'R' }],
            ['Animated', { rating: 'NC-17' }],
            ['Animated', { rating: 'X' }],
            ['Animated', { rating: 'TV-PG' }],
            ['Children', { rating: 'TV-Y7' }],
        ]),
        [[], [], [], [], [], ['kids'], ['kids']],
    );
});

test('missing or non-string fields are ignored', () => {
    const out = runBrs(LIB, `
        out.none = bowtie_guideFilter_programBuckets({})
        out.bad = bowtie_guideFilter_programBuckets({ category: 5, rating: invalid, programId: 12, isNew: invalid })
        out.inv = bowtie_guideFilter_programBuckets(invalid)
    `);
    assert.deepEqual(out, { none: {}, bad: {}, inv: {} });
});

test('programMatches: all matches everything; a bucket only its programs', () => {
    const out = runBrs(LIB, `
        out.all = bowtie_guideFilter_programMatches(${bs(prog())}, "all")
        out.football = bowtie_guideFilter_programMatches(${bs(prog({ category: 'Football' }))}, "sports")
        out.footballmovie = bowtie_guideFilter_programMatches(${bs(prog({ category: 'Football' }))}, "movies")
        out.isnew = bowtie_guideFilter_programMatches(${bs(prog({ isNew: true }))}, "new")
        out.bogus = bowtie_guideFilter_programMatches(${bs(prog({ category: 'Football' }))}, "bogus")
    `);
    assert.deepEqual(out, { all: true, football: true, footballmovie: false, isnew: true, bogus: false });
});

// ── Channels (now/next) ─────────────────────────────────────────────────────

const AT = '2026-10-04T18:30:00Z';

function matches(programs, filter, at = AT) {
    return runBrs(LIB, `
        out.r = bowtie_guideFilter_channelMatches(${bs(programs)}, "${filter}", "${at}")
    `).r;
}

test('all keeps every channel, even without guide data', () => {
    assert.equal(matches([], 'all'), true);
    const out = runBrs(LIB, 'out.r = bowtie_guideFilter_channelMatches(invalid, "all", "' + AT + '")');
    assert.equal(out.r, true);
});

test('a filter hides channels without guide data', () => {
    assert.equal(matches([], 'sports'), false);
    const out = runBrs(LIB, 'out.r = bowtie_guideFilter_channelMatches(invalid, "sports", "' + AT + '")');
    assert.equal(out.r, false);
});

test('keeps a channel whose now or next program matches', () => {
    const programs = [
        prog({ category: 'News' }),
        prog({ start: '2026-10-04T19:00:00Z', stop: '2026-10-04T22:00:00Z', category: 'Football' }),
    ];
    assert.equal(matches(programs, 'news'), true);
    assert.equal(matches(programs, 'sports'), true);
    assert.equal(matches(programs, 'movies'), false);
});

test('ignores a program that has ended and one after next', () => {
    const programs = [
        prog({ start: '2026-10-04T16:00:00Z', stop: '2026-10-04T18:00:00Z', category: 'Golf' }),
        prog({ start: '2026-10-04T18:00:00Z', stop: '2026-10-04T19:00:00Z', category: 'Drama' }),
        prog({ start: '2026-10-04T19:00:00Z', stop: '2026-10-04T20:00:00Z', category: 'Sitcom' }),
        prog({ start: '2026-10-04T20:00:00Z', stop: '2026-10-04T23:00:00Z', category: 'Golf' }),
    ];
    assert.equal(matches(programs, 'sports'), false);
});

test('counts a match that started earlier and is still on (stop is exclusive)', () => {
    const running = prog({ start: '2026-10-04T17:00:00Z', stop: '2026-10-04T18:30:01Z', category: 'Golf' });
    assert.equal(matches([running], 'sports'), true);
    const ended = prog({ start: '2026-10-04T17:00:00Z', stop: '2026-10-04T18:30:00Z', category: 'Golf' });
    assert.equal(matches([ended], 'sports'), false);
});

test('with nothing on now, the next program counts', () => {
    const later = prog({ start: '2026-10-04T19:00:00Z', stop: '2026-10-04T20:00:00Z', category: 'Movie' });
    assert.equal(matches([later], 'movies'), true);
});

test('channelsMatching keeps the rail order and drops non-matching channels', () => {
    const channels = [
        { id: 3, guideNumber: '5.1', name: 'A', favorite: true },
        { id: 7, guideNumber: '9.1', name: 'B', favorite: false },
        { id: 2, guideNumber: '2.1', name: 'C', favorite: false },
        { id: 4, guideNumber: '4.1', name: 'D', favorite: false },
    ];
    const guide = {
        3: [prog({ category: 'Football' })],
        7: [prog({ category: 'News' })],
        2: [prog({ category: 'Sports talk', isNew: true })],
    };
    const out = runBrs(LIB, `
        ch = ${bs(channels)}
        g = ${bs(guide)}
        out.sports = snapshot(bowtie_guideFilter_channelsMatching(ch, g, "sports", "${AT}"))
        out.news = snapshot(bowtie_guideFilter_channelsMatching(ch, g, "news", "${AT}"))
        out.kids = snapshot(bowtie_guideFilter_channelsMatching(ch, g, "kids", "${AT}"))
        out.all = snapshot(bowtie_guideFilter_channelsMatching(ch, g, "all", "${AT}"))
        out.noguide = snapshot(bowtie_guideFilter_channelsMatching(ch, invalid, "new", "${AT}"))
    `);
    const ids = (l) => l.map((c) => c.id);
    assert.deepEqual(ids(out.sports), [3, 2]);
    assert.deepEqual(ids(out.news), [7]);
    assert.deepEqual(ids(out.kids), []);
    assert.deepEqual(ids(out.all), [3, 7, 2, 4]);
    assert.deepEqual(ids(out.noguide), []);
});

// ── Chips, copy, persistence ────────────────────────────────────────────────

test('chips are All, Sports, Movies, News, Kids, New in order', () => {
    const out = runBrs(LIB, `
        out.ids = bowtie_guideFilter_filters()
        out.labels = []
        for each f in out.ids
            out.labels.push(bowtie_guideFilter_label(f))
        end for
        out.idx = [bowtie_guideFilter_indexOf("all"), bowtie_guideFilter_indexOf("kids"), bowtie_guideFilter_indexOf("new"), bowtie_guideFilter_indexOf("bogus")]
    `);
    assert.deepEqual(out.ids, ['all', 'sports', 'movies', 'news', 'kids', 'new']);
    assert.deepEqual(out.labels, ['All', 'Sports', 'Movies', 'News', 'Kids', 'New']);
    assert.deepEqual(out.idx, [0, 4, 5, 0]);
});

test('empty copy names the bucket', () => {
    const out = runBrs(LIB, `
        out.c = []
        for each f in ["sports", "movies", "news", "kids", "new", "all"]
            out.c.push(bowtie_guideFilter_emptyCopy(f))
        end for
    `);
    assert.deepEqual(out.c, [
        'No sports on right now',
        'No movies on right now',
        'No news on right now',
        "No kids' shows on right now",
        'No new episodes on right now',
        '',
    ]);
});

test('parse keeps known values and falls back to all', () => {
    const out = runBrs(LIB, `
        out.p = [bowtie_guideFilter_parse("sports"), bowtie_guideFilter_parse("new"), bowtie_guideFilter_parse("bogus"), bowtie_guideFilter_parse(""), bowtie_guideFilter_parse(invalid), bowtie_guideFilter_parse(3)]
    `);
    assert.deepEqual(out.p, ['sports', 'new', 'all', 'all', 'all', 'all']);
});
