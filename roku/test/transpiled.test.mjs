// Scans bsc's transpiled output (out/staging) for BrighterScript namespace
// shadowing: a local or parameter named like a function in the enclosing
// namespace gets rewritten to that function's mangled name, so `prog.title`
// becomes `ns_prog.title` and the device raises a runtime Syntax Error.
// The symptom in .brs is a namespaced function name used without a call.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync, readdirSync, statSync } from 'node:fs';
import { join } from 'node:path';

const staging = new URL('../out/staging/', import.meta.url).pathname;

function brsFiles(dir) {
    return readdirSync(dir).flatMap((name) => {
        const p = join(dir, name);
        if (statSync(p).isDirectory()) return brsFiles(p);
        return p.endsWith('.brs') ? [p] : [];
    });
}

test('no namespaced function name is used as a value', () => {
    const files = brsFiles(staging);
    assert.ok(files.length > 0, 'run bsc first: out/staging has no .brs files');

    const sources = files.map((f) => ({ file: f.slice(staging.length), lines: readFileSync(f, 'utf8').split(/\r?\n/) }));
    const declared = new Set();
    for (const { lines } of sources) {
        for (const line of lines) {
            const m = /^\s*(?:function|sub)\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(/i.exec(line);
            if (m && m[1].includes('_')) declared.add(m[1].toLowerCase());
        }
    }

    const offenders = [];
    for (const { file, lines } of sources) {
        lines.forEach((line, i) => {
            const code = line.replace(/"[^"]*"/g, '""').replace(/'.*$/, '');
            if (/^\s*(?:function|sub)\s/i.test(code)) return;
            for (const m of code.matchAll(/[A-Za-z_][A-Za-z0-9_]*/g)) {
                const name = m[0].toLowerCase();
                if (!declared.has(name)) continue;
                const rest = code.slice(m.index + m[0].length);
                if (!/^\s*\(/.test(rest)) offenders.push(`${file}:${i + 1}: ${line.trim()}`);
            }
        });
    }
    assert.deepEqual(offenders, [], 'locals/params shadowed by namespace functions');
});
