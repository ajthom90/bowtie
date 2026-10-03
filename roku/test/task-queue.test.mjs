// ApiTask's message loop is fed by a port observer on its `request` field.
// Reading m.top.request when the event arrives returns the field's CURRENT
// value, not the one that fired the event: two scenes writing back to back
// (Player's DELETE on Back, then Home's channel reload on show) lost the
// DELETE and processed the reload twice, stranding the viewer on the server.
// Each event must be read from msg.getData().
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

const src = readFileSync(new URL('../components/tasks/ApiTask.bs', import.meta.url), 'utf8');

test('ApiTask reads each request from its own field event', () => {
    const loop = src.slice(src.indexOf('sub taskRun()'), src.indexOf('end sub', src.indexOf('sub taskRun()')));
    assert.match(loop, /observeField\("request", m\.port\)/);
    assert.doesNotMatch(loop, /=\s*m\.top\.request\b/, 'reads the live field instead of the event payload');
    assert.match(loop, /msg\.getData\(\)/);
});
