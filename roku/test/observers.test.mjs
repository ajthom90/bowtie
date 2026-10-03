// ApiTask is one node shared by every scene. Plain unobserveField(field) removes
// ALL observers of that field — including other scenes' — so a hidden scene
// tearing down its observer silently deafened the visible one (Connect never
// saw its /healthz response). Shared-task observers must use the *Scoped API,
// which only adds/removes the calling component's observers.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync, readdirSync } from 'node:fs';

const dir = new URL('../components/', import.meta.url).pathname;

test('shared ApiTask fields are observed with the scoped API', () => {
    const offenders = [];
    for (const name of readdirSync(dir).filter((f) => f.endsWith('.bs'))) {
        readFileSync(dir + name, 'utf8').split(/\r?\n/).forEach((line, i) => {
            if (/\b(?:task|apiTask)\.(?:un)?observeField\(/i.test(line)) {
                offenders.push(`components/${name}:${i + 1}: ${line.trim()}`);
            }
        });
    }
    assert.deepEqual(offenders, []);
});
