// BrightScript lowercases unquoted AA-literal keys, so FormatJson({ refreshToken: x })
// sends "refreshtoken". Any camelCase key in a JSON body literal must be quoted.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync, readdirSync, statSync } from 'node:fs';
import { join } from 'node:path';

const root = new URL('../', import.meta.url).pathname;

function bsFiles(dir) {
    return readdirSync(dir).flatMap((name) => {
        const p = join(dir, name);
        if (statSync(p).isDirectory()) return name === 'tests' ? [] : bsFiles(p);
        return p.endsWith('.bs') ? [p] : [];
    });
}

test('FormatJson literals quote camelCase keys', () => {
    const offenders = [];
    for (const file of [...bsFiles(join(root, 'source')), ...bsFiles(join(root, 'components'))]) {
        const src = readFileSync(file, 'utf8');
        for (const m of src.matchAll(/FormatJson\(\{/g)) {
            let depth = 0, i = m.index + 'FormatJson('.length;
            for (; i < src.length; i++) {
                if (src[i] === '{') depth++;
                else if (src[i] === '}' && --depth === 0) break;
            }
            const literal = src.slice(m.index, i + 1);
            for (const k of literal.matchAll(/(?:^|[{\s,])([a-z]+[A-Z][A-Za-z0-9]*)\s*:/gm)) {
                offenders.push(`${file.slice(root.length)}: ${k[1]}`);
            }
        }
    }
    assert.deepEqual(offenders, []);
});
