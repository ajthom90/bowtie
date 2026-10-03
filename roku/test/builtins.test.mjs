// bsc cannot type-check methods on CreateObject() results, so a wrong name
// (roDateTime.MarkTime / .ToUTC) compiles and only crashes on device —
// "Member function not found" took down the channel rail. Check calls on
// variables created from these components against their documented interfaces.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync, readdirSync, statSync } from 'node:fs';
import { join } from 'node:path';

const METHODS = {
    roDateTime: [
        'Mark', 'ToLocalTime', 'GetTimeZoneOffset', 'AsSeconds', 'AsSecondsLong', 'FromSeconds',
        'FromSecondsLong', 'ToISOString', 'FromISO8601String', 'AsDateString', 'AsDateStringNoParam',
        'AsTimeToken', 'GetWeekday', 'GetYear', 'GetMonth', 'GetDayOfMonth', 'GetHours', 'GetMinutes',
        'GetSeconds', 'GetMilliseconds', 'GetLastDayOfMonth', 'GetDayOfWeek',
    ],
    roRegistrySection: ['Read', 'ReadMulti', 'Write', 'WriteMulti', 'Delete', 'Exists', 'Flush', 'GetKeyList'],
    roMessagePort: ['WaitMessage', 'GetMessage', 'PeekMessage'],
};

const root = new URL('../', import.meta.url).pathname;
function bsFiles(dir) {
    return readdirSync(dir).flatMap((name) => {
        const p = join(dir, name);
        if (statSync(p).isDirectory()) return bsFiles(p);
        return p.endsWith('.bs') ? [p] : [];
    });
}

test('methods called on built-in components exist', () => {
    const offenders = [];
    for (const file of [...bsFiles(join(root, 'source')), ...bsFiles(join(root, 'components'))]) {
        // Track per function: variable name -> component type.
        let vars = {};
        readFileSync(file, 'utf8').split(/\r?\n/).forEach((line, i) => {
            if (/^\s*(?:function|sub)\s/i.test(line)) vars = {};
            const code = line.replace(/'.*$/, '');
            const created = /([A-Za-z_][\w.]*)\s*=\s*CreateObject\("(\w+)"/.exec(code);
            if (created && METHODS[created[2]]) vars[created[1].toLowerCase()] = created[2];
            for (const call of code.matchAll(/([A-Za-z_][\w.]*)\.(\w+)\s*\(/g)) {
                const type = vars[call[1].toLowerCase()];
                if (!type) continue;
                const ok = METHODS[type].some((m) => m.toLowerCase() === call[2].toLowerCase());
                if (!ok) offenders.push(`${file.slice(root.length)}:${i + 1}: ${type}.${call[2]}()`);
            }
        });
    }
    assert.deepEqual(offenders, []);
});
