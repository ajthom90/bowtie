// Validates roku/manifest against rules the Roku firmware enforces at sideload
// time but bsc does not check (a bad manifest compiles locally, then the device
// rejects the whole package with "Install Failure: Compilation Failed").
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

const manifest = Object.fromEntries(
    readFileSync(new URL('../manifest', import.meta.url), 'utf8')
        .split(/\r?\n/)
        .filter((line) => line.trim() && !line.trim().startsWith('#'))
        .map((line) => {
            const i = line.indexOf('=');
            return [line.slice(0, i).trim(), line.slice(i + 1).trim()];
        })
);

// BrightScript reserved words that firmware refuses as #const names.
const RESERVED = new Set([
    'and', 'box', 'createobject', 'dim', 'each', 'else', 'elseif', 'end', 'endfunction',
    'endif', 'endsub', 'endwhile', 'eval', 'exit', 'exitwhile', 'false', 'for', 'function',
    'getglobalaa', 'getlastruncompileerror', 'getlastrunruntimeerror', 'goto', 'if',
    'invalid', 'let', 'line_num', 'm', 'next', 'not', 'objfun', 'or', 'pos', 'print',
    'rem', 'return', 'run', 'step', 'stop', 'sub', 'tab', 'then', 'to', 'true', 'type', 'while',
]);

test('bs_const entries are NAME=true|false with non-reserved names', () => {
    if (!('bs_const' in manifest)) return;
    for (const entry of manifest.bs_const.split(';')) {
        const m = /^([A-Za-z_][A-Za-z0-9_]*)=(true|false)$/i.exec(entry.trim());
        assert.ok(m, `bs_const entry "${entry}" must be NAME=true|false`);
        assert.ok(!RESERVED.has(m[1].toLowerCase()), `bs_const name "${m[1]}" is a reserved word`);
    }
});

test('required channel keys are present', () => {
    for (const key of ['title', 'major_version', 'minor_version', 'build_version', 'ui_resolutions']) {
        assert.ok(manifest[key], `manifest is missing ${key}`);
    }
});
