// HomeScene wiring for the guide filter chips. The rules run under brs in
// guide-filter.test.mjs and the row stacking in continue-watching.test.mjs;
// this pins the scene-level contract that can't run off-device: the chip
// row, which list the rail indexes, the empty state, keys and the registry.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { runBrs } from './brs-run.mjs';

const read = (p) => readFileSync(new URL(`../${p}`, import.meta.url), 'utf8');
const home = read('components/HomeScene.bs');
const homeXml = read('components/HomeScene.xml');
const chipXml = read('components/FilterChip.xml');
const chipBs = read('components/FilterChip.bs');
const registry = read('source/lib/Registry.bs');

function body(src, signature) {
    const start = src.indexOf(signature);
    assert.ok(start >= 0, `missing ${signature}`);
    const end = src.indexOf(signature.startsWith('function') ? 'end function' : 'end sub', start);
    return src.slice(start, end);
}

function tag(xml, id) {
    const at = xml.indexOf(`id="${id}"`);
    assert.ok(at >= 0, `no element ${id}`);
    return xml.slice(xml.lastIndexOf('<', at), xml.indexOf('>', at));
}

const yOf = (t) => Number(/translation="\[\s*-?\d+\s*,\s*(\d+)\s*\]"/.exec(t)[1]);

// The branch of onKeyEvent guarded by `if m.<node>.hasFocus()`.
function focusBranch(node) {
    const handler = body(home, 'function onKeyEvent');
    const start = handler.indexOf(`if m.${node}.hasFocus()`);
    assert.ok(start >= 0, `onKeyEvent has no ${node} branch`);
    let depth = 0;
    const out = [];
    for (const line of handler.slice(start).split('\n')) {
        out.push(line);
        const t = line.trim();
        if (/^if\b/.test(t) && !/\bthen\b/.test(t)) depth++;
        if (/^end if\b/.test(t) && --depth === 0) break;
    }
    return out.join('\n');
}

test('the chips are a one-row, six-column MarkupGrid of FilterChip between Recent and the rail', () => {
    const grid = tag(homeXml, 'filterList');
    assert.match(grid, /<MarkupGrid/);
    assert.match(grid, /itemComponentName="FilterChip"/);
    assert.match(grid, /numRows="1"/);
    assert.match(grid, /numColumns="6"/);
    assert.match(grid, /visible="false"/);
    assert.match(grid, /focusBitmapUri="pkg:\/images\/focus_amber\.9\.png"/);
    assert.ok(homeXml.indexOf('id="recentList"') < homeXml.indexOf('id="filterList"'));
    assert.ok(homeXml.indexOf('id="filterList"') < homeXml.indexOf('id="channelList"'));
    // Authored where the layout puts them with no Continue / Recent row.
    const layout = runBrs(['source/lib/Recordings.brs', 'source/lib/ContinueWatching.brs'], `
        out.l = snapshot(bowtie_continueWatching_homeLayout(false, false))
    `).l;
    assert.equal(yOf(grid), layout.filtersY);
    const rail = tag(homeXml, 'channelList');
    assert.equal(yOf(rail), layout.railY);
    assert.equal(Number(/numRows="(\d+)"/.exec(rail)[1]), layout.railRows);
    const h = Number(/itemSize="\[\s*\d+\s*,\s*(\d+)\s*\]"/.exec(grid)[1]);
    assert.ok(layout.filtersY + h < layout.railY, 'chips overlap the rail');
    // Six chips fit the 1760 px content width.
    const w = Number(/itemSize="\[\s*(\d+)/.exec(grid)[1]);
    const gap = Number(/itemSpacing="\[\s*(\d+)/.exec(grid)[1]);
    assert.ok(6 * w + 5 * gap <= 1760);
});

test('a chip fills amber when it is the chosen filter', () => {
    for (const field of ['itemContent', 'width', 'height', 'focusPercent', 'gridHasFocus', 'itemHasFocus']) {
        assert.match(chipXml, new RegExp(`<field id="${field}"`));
    }
    assert.match(chipBs, /content\.selected = true/);
    assert.match(chipBs, /m\.chipBg\.color = "0xF0A428FF"/);
    const render = body(home, 'sub renderFilters(');
    assert.match(render, /bowtie\.guideFilter\.filters\(\)/);
    assert.match(render, /bowtie\.guideFilter\.label\(f\)/);
    assert.match(render, /selected: \(f = m\.filter\)/);
    assert.match(render, /m\.filterList\.jumpToItem = bowtie\.guideFilter\.indexOf\(m\.filter\)/);
});

test('the chosen chip is read from and saved to the registry', () => {
    assert.match(body(home, 'sub init('), /m\.filter = bowtie\.guideFilter\.parse\(bowtie\.registry\.loadGuideFilter\(\)\)/);
    const set = body(home, 'sub setFilter(');
    assert.match(set, /bowtie\.registry\.saveGuideFilter\(f\)/);
    assert.match(set, /renderFilters\(\)/);
    assert.match(set, /renderRail\(invalid\)/);
    // Same section as the other prefs; change server / sign-out keep it.
    assert.match(registry, /CreateObject\("roRegistrySection", "bowtie"\)/);
    assert.match(body(registry, 'function saveGuideFilter('), /sec\.Write\("guideFilter", filter\)/);
    assert.doesNotMatch(body(registry, 'function clearAll('), /guideFilter/);
    assert.match(body(home, 'sub onFilterSelected('), /setFilter\(list\[idx\]\)/);
});

test('the rail shows only the channels the filter keeps; selections index that list', () => {
    const render = body(home, 'sub renderRail(');
    assert.match(render, /m\.railChannels = bowtie\.guideFilter\.channelsMatching\(m\.joinable, m\.guideById, m\.filter, atIso\)/);
    assert.match(render, /buildListContent\(m\.railChannels,/);
    assert.match(render, /indexOfChannel\(m\.railChannels, focusChannelId\)/);
    // OK plays from every joinable channel so zapping ignores the filter.
    assert.match(body(home, 'sub onItemSelected('), /indexOfChannel\(m\.joinable, m\.railChannels\[idx\]\.id\)/);
    assert.match(body(home, 'function openChannelOptions('), /ch = m\.railChannels\[idx\]/);
    assert.match(body(home, 'sub handleFavoriteResponse('), /focusedId = m\.railChannels\[idx\]\.id/);
    assert.doesNotMatch(home, /m\.channels\[m\.channelList\./);
});

test('no matching channel: the empty copy and Show all replace the rail', () => {
    const group = tag(homeXml, 'filterEmptyGroup');
    assert.match(group, /visible="false"/);
    assert.match(tag(homeXml, 'showAllButton'), /text="Show all"/);
    const gStart = homeXml.indexOf('id="filterEmptyGroup"');
    const gEnd = homeXml.indexOf('</Group>', gStart);
    assert.ok(homeXml.indexOf('id="showAllButton"') > gStart && homeXml.indexOf('id="showAllButton"') < gEnd);

    const rows = body(home, 'sub updateRows(');
    assert.match(rows, /showRail = \(listShown and m\.railChannels\.count\(\) > 0\)/);
    assert.match(rows, /showFilterEmpty = \(listShown and not showRail\)/);
    assert.match(rows, /bowtie\.guideFilter\.emptyCopy\(m\.filter\)/);
    assert.match(rows, /m\.filterList\.visible = listShown/);
    assert.match(rows, /m\.channelList\.visible = showRail/);
    assert.match(rows, /m\.filterEmptyGroup\.visible = showFilterEmpty/);
    assert.match(rows, /m\.filterList\.translation = \[80, layout\.filtersY\]/);
    // Focus never stays on something hidden.
    assert.match(rows, /not showRail and m\.channelList\.hasFocus\(\)/);
    assert.match(rows, /not showFilterEmpty and m\.showAllButton\.hasFocus\(\)/);
    assert.match(rows, /not listShown and m\.filterList\.hasFocus\(\)/);

    const showAll = body(home, 'sub onShowAll(');
    assert.match(showAll, /setFilter\("all"\)/);
    assert.match(showAll, /m\.filterList\.setFocus\(true\)/);
    assert.match(body(home, 'sub init('), /m\.showAllButton\.observeField\("buttonSelected", "onShowAll"\)/);
});

test('keys: rail up → chips, chips down → rail or Show all, Show all up → chips', () => {
    assert.match(focusBranch('channelList'), /key = "up"[\s\S]*?m\.filterList\.setFocus\(true\)/);
    const chips = focusBranch('filterList');
    assert.match(chips, /key = "down"[\s\S]*?m\.channelList\.visible = true[\s\S]*?m\.channelList\.setFocus\(true\)[\s\S]*?m\.filterEmptyGroup\.visible = true[\s\S]*?m\.showAllButton\.setFocus\(true\)/);
    assert.match(chips, /key = "up"[\s\S]*?focusAboveFilters\(\)/);
    // Left / Right / OK belong to the MarkupGrid.
    assert.doesNotMatch(chips, /key = "(left|right|OK)"/);
    assert.match(focusBranch('showAllButton'), /key = "up"[\s\S]*?m\.filterList\.setFocus\(true\)/);
});

test('default focus is unchanged under All; an emptied rail lands on Show all', () => {
    const show = body(home, 'sub showList(');
    assert.match(show, /m\.listShown = true/);
    assert.match(show, /m\.focusContinueOnShow = true and m\.continueList\.visible = true[\s\S]*?m\.continueList\.setFocus\(true\)[\s\S]*?m\.channelList\.visible = true[\s\S]*?m\.channelList\.setFocus\(true\)[\s\S]*?else[\s\S]*?m\.showAllButton\.setFocus\(true\)/);
    for (const sig of ['sub showLoading(', 'sub showEmpty(', 'sub showError(']) {
        assert.match(body(home, sig), /m\.listShown = false/);
    }
    const restore = body(home, 'sub restoreRailFocus(');
    assert.match(restore, /m\.filterEmptyGroup\.visible = true[\s\S]*?m\.showAllButton\.setFocus\(true\)/);
});
