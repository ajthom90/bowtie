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
    'http://10.0.0.5:8080',
    'http://10.0.0.5:8080/p?t=0123456789abcdef',
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

// Every mask candidate for text, built from the encoder's own steps: the
// matrix with mask k applied and its format bits written, plus the score
// the encoder's penalty() gives it, and the mask encode() chose.
function candidates(text) {
    return runBrs(LIB, `
        text = ${lit(text)}
        out.mask = bowtie_qr_encode(text).mask
        ba = CreateObject("roByteArray")
        ba.FromAsciiString(text)
        bytes = []
        for each b in ba
            bytes.push(b)
        end for
        table = bowtie_qr_versions()
        info = table[bowtie_qr_capacityVersion(bytes.Count(), table) - 1]
        gf = bowtie_qr_gfTables()
        base = bowtie_qr_newMatrix(info)
        bowtie_qr_placeData(base, bowtie_qr_interleave(bowtie_qr_dataCodewords(bytes, info), info, gf))
        out.cands = []
        out.scores = []
        for k = 0 to 7
            c = bowtie_qr_applyMask(base, k)
            bowtie_qr_writeFormat(c, k)
            out.cands.push({ size: c.size, rows: c.dark })
            out.scores.push(bowtie_qr_penalty(c))
        end for
    `);
}

// Reference mask penalty (ISO/IEC 18004 7.8.3), written independently of
// the BrightScript: N1 = 3 + (run - 5) per same-colour run of 5+ in a row or
// column; N2 = 3 per 2x2 same-colour block; N3 = 40 per dark-light-dark-
// dark-dark-light-dark with 4 light modules (outside the symbol is light)
// before or after it; N4 = 10 per full 5% the dark share is off 50%.
function refPenalty(rows) {
    const n = rows.length;
    const lines = [];
    for (let i = 0; i < n; i++) {
        lines.push(rows[i]);
        lines.push(rows.map((row) => row[i]));
    }
    let score = 0;
    for (const line of lines) {
        let run = 1;
        for (let i = 1; i <= n; i++) {
            if (i < n && line[i] === line[i - 1]) { run++; continue; }
            if (run >= 5) score += 3 + run - 5;
            run = 1;
        }
        const dark = (i) => i >= 0 && i < n && line[i];
        const light4 = (from) => [0, 1, 2, 3].every((d) => !dark(from + d));
        const finder = [true, false, true, true, true, false, true];
        for (let i = 0; i + 7 <= n; i++) {
            if (finder.every((v, d) => line[i + d] === v) && (light4(i - 4) || light4(i + 7))) score += 40;
        }
    }
    for (let r = 0; r + 1 < n; r++) for (let c = 0; c + 1 < n; c++) {
        const v = rows[r][c];
        if (rows[r][c + 1] === v && rows[r + 1][c] === v && rows[r + 1][c + 1] === v) score += 3;
    }
    const darkCount = rows.flat().filter(Boolean).length;
    const percent = (darkCount * 100) / (n * n);
    score += 10 * Math.floor(Math.abs(percent - 50) / 5);
    return score;
}

const MASK_CASES = [
    'A',
    'http://10.0.0.5:8080',
    'http://255.255.255.255:65535/p?t=0123456789abcdef01234567',
    'x'.repeat(84),
    // Masks 0 and 3 tie for the lowest score, and some candidates score N4.
    ' '.repeat(62),
];

for (const text of MASK_CASES) {
    test(`picks the lowest-penalty mask (lowest number on a tie) for ${text.length} bytes`, () => {
        const got = candidates(text);
        const want = got.cands.map((m) => refPenalty(m.rows));
        assert.deepEqual(got.scores, want);
        assert.equal(got.mask, want.indexOf(Math.min(...want)));
    });
}

for (const text of ['A', 'http://255.255.255.255:65535/p?t=0123456789abcdef01234567']) {
    test(`every mask scans back for ${text.length} bytes`, () => {
        const got = candidates(text);
        got.cands.forEach((m, k) => assert.equal(decode(m), text, `mask ${k}`));
    });
}
