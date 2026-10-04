// BowtieClient request builders / parsers for favorites and recents, run
// under brs against the transpiled source/lib/BowtieClient.brs.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { runBrs } from './brs-run.mjs';

const LIB = ['source/lib/BowtieClient.brs'];
const BASE = 'http://bowtie.local:8080/';

test('setFavorite on → PUT /api/v1/me/favorites/{id} with Bearer', () => {
    const out = runBrs(LIB, `
        out.req = bowtie_client_buildRequest("setFavorite", { channelId: 7, on: true }, "tok", "${BASE}")
    `);
    assert.equal(out.req.method, 'PUT');
    assert.equal(out.req.url, 'http://bowtie.local:8080/api/v1/me/favorites/7');
    assert.equal(out.req.headers.Authorization, 'Bearer tok');
    assert.equal(out.req.error, undefined);
});

test('setFavorite off → DELETE /api/v1/me/favorites/{id}', () => {
    const out = runBrs(LIB, `
        out.req = bowtie_client_buildRequest("setFavorite", { channelId: 12, on: false }, "tok", "${BASE}")
    `);
    assert.equal(out.req.method, 'DELETE');
    assert.equal(out.req.url, 'http://bowtie.local:8080/api/v1/me/favorites/12');
    assert.equal(out.req.headers.Authorization, 'Bearer tok');
});

test('recents → GET /api/v1/me/recents?limit=n (default 8)', () => {
    const out = runBrs(LIB, `
        out.given = bowtie_client_buildRequest("recents", { limit: 5 }, "tok", "${BASE}")
        out.dflt = bowtie_client_buildRequest("recents", {}, "tok", "${BASE}")
    `);
    assert.equal(out.given.method, 'GET');
    assert.equal(out.given.url, 'http://bowtie.local:8080/api/v1/me/recents?limit=5');
    assert.equal(out.given.headers.Authorization, 'Bearer tok');
    assert.equal(out.dflt.url, 'http://bowtie.local:8080/api/v1/me/recents?limit=8');
});

test('setFavorite: 204 is success, 404 and 401 are errors with their codes', () => {
    const out = runBrs(LIB, `
        out.ok = bowtie_client_parseResponse("setFavorite", 204, "")
        out.missing = bowtie_client_parseResponse("setFavorite", 404, "{""error"":""channel not found""}")
        out.unauth = bowtie_client_parseResponse("setFavorite", 401, "")
    `);
    assert.equal(out.ok.ok, true);
    assert.equal(out.missing.ok, false);
    assert.equal(out.missing.error.code, 404);
    assert.equal(out.missing.error.message, 'channel not found');
    assert.equal(out.unauth.ok, false);
    assert.equal(out.unauth.error.code, 401);
});

test('recents: 200 array is data, 404 (older server) is notFound', () => {
    const body = JSON.stringify([{ channelId: 7, guideNumber: '9.1', name: 'FOX9', logoUrl: '', watchedAt: '2026-10-03T19:42:10Z' }]);
    const out = runBrs(LIB, `
        out.ok = bowtie_client_parseResponse("recents", 200, "${body.replace(/"/g, '""')}")
        out.old = bowtie_client_parseResponse("recents", 404, "404 page not found")
    `);
    assert.equal(out.ok.ok, true);
    assert.equal(out.ok.data[0].channelId, 7);
    assert.equal(out.old.ok, false);
    assert.equal(out.old.error.code, 404);
    assert.equal(out.old.error.kind, 'notFound');
});
