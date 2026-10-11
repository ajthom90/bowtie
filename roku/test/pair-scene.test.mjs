// Wiring checks for TV pairing (sockets can't run under brs; the device run
// in docs/deploy/roku-testing.md covers the listener itself).
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

const read = (p) => readFileSync(new URL(`../${p}`, import.meta.url), 'utf8');

function body(src, signature) {
    const start = src.indexOf(signature);
    assert.ok(start >= 0, `missing ${signature}`);
    const end = src.indexOf(signature.startsWith('function') ? 'end function' : 'end sub', start);
    return src.slice(start, end);
}

test('PairTask exposes the fields the scenes use', () => {
    const xml = read('components/tasks/PairTask.xml');
    for (const f of ['pairUrl', 'submitted', 'connectFailed', 'linkUrl', 'stop']) assert.match(xml, new RegExp(`id="${f}"`));
    assert.match(xml, /functionName="pairRun"|<script/);
});

test('PairTask listens on the LAN address only, serves at most 4 connections, 5 s each', () => {
    const bs = read('components/tasks/PairTask.bs');
    assert.match(bs, /setHostName\(ip\)/);
    assert.match(bs, /MAX_CONNECTIONS = 4\b/);
    assert.match(bs, /REQUEST_MS = 5000\b/);
    assert.match(bs, /notifyReadable\(true\)/);
    // Every way out hides the QR.
    assert.match(body(bs, 'sub pairRun()'), /m\.top\.pairUrl = ""/);
});

test('Connect no longer opens the keyboard by itself and offers typing as a button', () => {
    const bs = read('components/ConnectScene.bs');
    assert.doesNotMatch(bs, /onOpenKeyboardTimer/);
    assert.match(read('components/ConnectScene.xml'), /text="Type the address instead"/);
    assert.match(bs, /bowtie\.pair\.normalizeServer/);
});

test('Connect shows the pairing copy', () => {
    const xml = read('components/ConnectScene.xml');
    assert.match(xml, /Scan with your phone to set up this TV/);
    assert.match(xml, /<QrCode id="pairQr"/);
});

test('Connect reports phone addresses it will not probe, and stops pairing only on a typed success', () => {
    const bs = read('components/ConnectScene.bs');
    const response = body(bs, 'sub onApiResponse()');
    assert.match(response, /if m\.fromPair <> true then stopPairing\(\)/);
    assert.match(body(bs, 'sub stopPairing()'), /\.stop = true/);
    assert.match(response, /reportPairFailed/);
    assert.match(body(bs, 'sub reportPairFailed('), /\.connectFailed = address/);
    assert.match(body(bs, 'sub onPairSubmitted()'), /m\.top\.visible <> true or m\.busy = true\s+reportPairFailed/);
    assert.doesNotMatch(body(bs, 'sub submitUrl('), /stop = true/);
});

test('Login hands the link URL to the pairing task', () => {
    assert.match(read('components/LoginScene.bs'), /pairTask\.linkUrl = bowtie\.pair\.linkUrl\(/);
});

test('AppScene stops pairing once signed in', () => {
    const bs = read('components/AppScene.bs');
    assert.match(bs, /pairTask\.stop = true/);
    assert.match(body(bs, 'sub showHome()'), /stopPairing\(\)/);
    assert.doesNotMatch(body(bs, 'sub onConnected()'), /stopPairing/);
});

test('QrCode encodes once per text change', () => {
    const bs = read('components/QrCode.bs');
    assert.match(bs, /observeField\("text"/);
    assert.equal(bs.match(/bowtie\.qr\.encode\(/g).length, 1);
});

test('deploy script sideloads with digest auth', () => {
    assert.match(read('package.json'), /"deploy": "npm run package && \.\/scripts\/deploy\.sh"/);
    assert.match(read('scripts/deploy.sh'), /--digest/);
});
