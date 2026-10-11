// QR encoder (source/lib/Qr.brs), run under brs and decoded with jsqr: the
// pairing QR is drawn on the Roku itself, so it must actually scan.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import jsQR from 'jsqr';
import { runBrs } from './brs-run.mjs';

const LIB = ['source/lib/Qr.brs'];
const lit = (s) => `"${s.replace(/"/g, '""')}"`;

function encode(text) {
    return runBrs(LIB, `out.m = bowtie_qr_encode(${lit(text)})`).m;
}

// Matrix → RGBA image (4 px per module, 4-module quiet zone) → jsqr.
function decode(m) {
    const scale = 4, quiet = 4, n = (m.size + quiet * 2) * scale;
    const data = new Uint8ClampedArray(n * n * 4).fill(255);
    for (let r = 0; r < m.size; r++) for (let c = 0; c < m.size; c++) {
        if (!m.rows[r][c]) continue;
        for (let dy = 0; dy < scale; dy++) for (let dx = 0; dx < scale; dx++) {
            const i = (((r + quiet) * scale + dy) * n + (c + quiet) * scale + dx) * 4;
            data[i] = data[i + 1] = data[i + 2] = 0;
        }
    }
    return jsQR(data, n, n)?.data;
}

const CASES = [
    'A',
    'http://10.0.0.5:8080/p?t=0123456789abcdef01234567',
    'http://192.168.1.123:54321/p?t=0123456789abcdef01234567',
    'http://255.255.255.255:65535/p?t=0123456789abcdef01234567',
    'x'.repeat(84),
    'y'.repeat(106),
];

for (const text of CASES) {
    test(`encodes ${text.length} bytes so it scans back`, () => {
        const m = encode(text);
        assert.equal(m.rows.length, m.size);
        assert.equal(decode(m), text);
    });
}

test('picks the smallest version (v4 = 33 modules for the longest pairing URL)', () => {
    assert.equal(encode('http://255.255.255.255:65535/p?t=0123456789abcdef01234567').size, 33);
    assert.equal(encode('A').size, 21);
});

test('too long returns invalid', () => {
    const got = runBrs(LIB, `out.isinvalid = bowtie_qr_encode(${lit('z'.repeat(107))}) = invalid`);
    assert.equal(got.isinvalid, true);
});

test('runs cover exactly the dark modules', () => {
    const got = runBrs(LIB, `
        qr = bowtie_qr_encode("http://192.168.1.2:5000/p?t=abc")
        out.m = snapshot(qr)
        out.runs = bowtie_qr_runs(qr)
    `);
    let dark = 0;
    for (const row of got.m.rows) for (const v of row) if (v) dark++;
    const covered = got.runs.reduce((s, r) => s + r.w, 0);
    assert.equal(covered, dark);
    for (const r of got.runs) for (let c = r.x; c < r.x + r.w; c++) assert.equal(got.m.rows[r.y][c], true);
});
