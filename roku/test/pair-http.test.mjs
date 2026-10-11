// TV pairing page (source/lib/PairHttp.brs): the tiny HTTP page the Roku
// serves so a phone can type the server address. Pure request → response.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { runBrs } from './brs-run.mjs';

const LIB = ['source/lib/PairHttp.brs'];
const lit = (s) => `"${s.replace(/"/g, '""').replace(/\r/g, '" + chr(13) + "').replace(/\n/g, '" + chr(10) + "')}"`;
const T = '0123456789abcdef01234567';
const get = (path) => `GET ${path} HTTP/1.1\r\nHost: tv\r\n\r\n`;
const post = (body) => `POST /p HTTP/1.1\r\nHost: tv\r\nContent-Type: application/x-www-form-urlencoded\r\nContent-Length: ${Buffer.byteLength(body)}\r\n\r\n${body}`;

// brs's FormatJson does not escape quotes, CR or LF, so a response cannot
// travel through `out` as-is: req() percent-encodes those (and "…") in
// BrightScript and run() decodes them back.
const DEC = { '%25': '%', '%22': '"', '%5C': '\\', '%0D': '\r', '%0A': '\n', '%E2': '\u2026' };
const decode = (s) => s.replace(/%(25|22|5C|0D|0A|E2)/g, (m) => DEC[m]);

// Runs a script of steps against one session; each step is BrightScript.
function run(steps) {
    const out = runBrs(LIB, `
        s = bowtie_pair_newSession(${lit(T)})
        out.r = []
        ${steps.join('\n')}
        out.state = s.state
        out.address = s.address
    `);
    for (const r of out.r) r.response = decode(r.response);
    return out;
}
const req = (raw) => `h = bowtie_pair_handle(s, ${lit(raw)}) : e = h.response : e = e.Replace("%", "%25").Replace(chr(34), "%22").Replace(chr(92), "%5C").Replace(chr(13), "%0D").Replace(chr(10), "%0A").Replace(chr(8230), "%E2") : h.response = e : out.r.push(snapshot(h))`;
const status = (r) => r.response.split('\r\n')[0];
const header = (r, name) => r.response.split('\r\n').find((l) => l.toLowerCase().startsWith(name.toLowerCase() + ':'))?.split(': ')[1];
const body = (r) => r.response.split('\r\n\r\n').slice(1).join('\r\n\r\n');

test('GET /p with the token shows the form', () => {
    const { r } = run([req(get(`/p?t=${T}`))]);
    assert.equal(status(r[0]), 'HTTP/1.1 200 OK');
    assert.match(body(r[0]), /<h1>Set up Bowtie on your TV<\/h1>/);
    assert.match(body(r[0]), new RegExp(`name="t" value="${T}"`));
    assert.equal(header(r[0], 'Connection'), 'close');
    assert.equal(header(r[0], 'Content-Length'), String(Buffer.byteLength(body(r[0]))));
});

test('POST normalizes the address, submits it once and redirects to status', () => {
    const out = run([req(post(`t=${T}&server=192.168.1.50%3A8400%2F`)), req(post(`t=${T}&server=other`))]);
    assert.equal(status(out.r[0]), 'HTTP/1.1 303 See Other');
    assert.equal(header(out.r[0], 'Location'), `/p/status?t=${T}`);
    assert.equal(out.r[0].submitted, 'http://192.168.1.50:8400');
    assert.equal(out.r[1].submitted, '');
    assert.equal(out.state, 'connecting');
});

test('a bad address re-shows the form with the error and what was typed', () => {
    const { r, state } = run([req(post(`t=${T}&server=ftp%3A%2F%2Fx`))]);
    assert.equal(status(r[0]), 'HTTP/1.1 200 OK');
    assert.match(body(r[0]), /Enter an address like http:\/\/192\.168\.1\.50:8400\./);
    assert.match(body(r[0]), /value="ftp:\/\/x"/);
    assert.equal(state, 'waiting');
});

test('status: waiting page with refresh while connecting, then redirect to link', () => {
    const { r } = run([
        req(post(`t=${T}&server=http%3A%2F%2Fbowtie.lan%3A8400`)),
        req(get(`/p/status?t=${T}`)),
        'bowtie_pair_codeIssued(s, bowtie_pair_linkUrl("http://bowtie.lan:8400", "BCDF-2345"))',
        req(get(`/p/status?t=${T}`)),
        'bowtie_pair_codeIssued(s, bowtie_pair_linkUrl("http://bowtie.lan:8400", "GHJK-6789"))',
        req(get(`/p/status?t=${T}`)),
        req(get(`/p?t=${T}`)),
    ]);
    assert.match(body(r[1]), /Connecting to your TV…/);
    assert.match(body(r[1]), new RegExp(`content="1;url=/p/status\\?t=${T}"`));
    assert.equal(header(r[2], 'Location'), 'http://bowtie.lan:8400/link?code=BCDF2345');
    assert.equal(header(r[3], 'Location'), 'http://bowtie.lan:8400/link?code=GHJK6789');
    assert.equal(header(r[4], 'Location'), `/p/status?t=${T}`);
});

test('connect failure shows the unreachable error and allows a retry', () => {
    const out = run([
        req(post(`t=${T}&server=bowtie.lan`)),
        'bowtie_pair_connectFailed(s, s.address)',
        req(get(`/p/status?t=${T}`)),
        req(post(`t=${T}&server=bowtie2.lan`)),
    ]);
    assert.match(body(out.r[1]), /Couldn&#39;t reach a Bowtie server at http:\/\/bowtie\.lan\. Check the address and try again\./);
    assert.equal(out.r[2].submitted, 'http://bowtie2.lan');
});

test('HTML in the address is rejected and echoed escaped', () => {
    const { r } = run([req(post(`t=${T}&server=%22%3E%3Cscript%3E`))]);
    assert.equal(r[0].submitted, '');
    assert.doesNotMatch(body(r[0]), /<script>/);
    assert.match(body(r[0]), /value="&quot;&gt;&lt;script&gt;"/);
});

test('wrong tokens lock the session after 20; missing tokens and 404s do not count', () => {
    const steps = [];
    for (let i = 0; i < 30; i++) steps.push(req(get('/favicon.ico')), req(get('/p')));
    for (let i = 0; i < 19; i++) steps.push(req(get('/p?t=nope')));
    steps.push(req(get(`/p?t=${T}`)), req(get('/p/status?t=nope')), req(get(`/p?t=${T}`)));
    const { r, state } = run(steps);
    assert.equal(status(r[0]), 'HTTP/1.1 404 Not Found');
    assert.equal(status(r[1]), 'HTTP/1.1 403 Forbidden');
    assert.equal(status(r[60 + 19]), 'HTTP/1.1 200 OK');
    assert.equal(status(r[60 + 21]), 'HTTP/1.1 403 Forbidden');
    assert.equal(state, 'closed');
});

test('garbage is 400, oversize is 413, other methods 404', () => {
    const { r } = run([req('hello'), req(post('t=' + 'a'.repeat(9000))), req(`PUT /p?t=${T} HTTP/1.1\r\n\r\n`)]);
    assert.equal(status(r[0]), 'HTTP/1.1 400 Bad Request');
    assert.equal(status(r[1]), 'HTTP/1.1 413 Payload Too Large');
    assert.equal(status(r[2]), 'HTTP/1.1 404 Not Found');
});

test('urlDecode handles + and %XX; htmlEscape covers the five characters', () => {
    const got = runBrs(LIB, `
        out.d = bowtie_pair_urlDecode("a+b%2Fc%3a%25")
        out.e = bowtie_pair_htmlEscape("<a href=""x"">'&'</a>")
    `);
    assert.equal(got.d, 'a b/c:%');
    assert.equal(got.e, '&lt;a href=&quot;x&quot;&gt;&#39;&amp;&#39;&lt;/a&gt;');
});

// ---- hardening ----
const raw = (head, tail = '') => `POST /p HTTP/1.1\r\n${head}\r\n\r\n${tail}`;

test('waiting page Content-Length counts UTF-8 bytes', () => {
    const { r } = run([req(post(`t=${T}&server=bowtie.lan`)), req(get(`/p/status?t=${T}`))]);
    assert.equal(header(r[1], 'Content-Length'), String(Buffer.byteLength(body(r[1]))));
});

test('Content-Length must be plain digits; huge or oversize is 413; conflicting duplicates 400', () => {
    const { r } = run([
        req(raw('Content-Length: -5')),
        req(raw('Content-Length: ')),
        req(raw('Content-Length: 1e3')),
        req(raw('Content-Length: +5')),
        req(raw('Content-Length: 99999999999999999999')),
        req(raw('Content-Length: 000005')),
        req(raw('Content-Length: 8193')),
        req(raw('Content-Length: 5\r\nContent-Length: 6', 'abcdef')),
        req(raw('Content-Length: 4\r\ncontent-length: 4', 't=ab')),
    ]);
    assert.deepEqual(r.map(status), [
        'HTTP/1.1 400 Bad Request', 'HTTP/1.1 400 Bad Request', 'HTTP/1.1 400 Bad Request', 'HTTP/1.1 400 Bad Request',
        'HTTP/1.1 413 Payload Too Large', 'HTTP/1.1 413 Payload Too Large', 'HTTP/1.1 413 Payload Too Large',
        'HTTP/1.1 400 Bad Request', 'HTTP/1.1 403 Forbidden',
    ]);
});

test('a body shorter than Content-Length is incomplete; a complete one is not', () => {
    const { r } = run([req(raw('Content-Length: 10', 'abc')), req('POST /p HTTP/1.1\r\nHost: tv'), req(get(`/p?t=${T}`))]);
    assert.equal(r[0].incomplete, true);
    assert.equal(r[1].incomplete, true);
    assert.equal(r[2].incomplete, false);
});

test('connectFailed only applies to the address being connected to', () => {
    const { r, state } = run([
        req(post(`t=${T}&server=bowtie.lan`)),
        'bowtie_pair_connectFailed(s, "http://other.lan")',
        req(get(`/p/status?t=${T}`)),
    ]);
    assert.equal(state, 'connecting');
    assert.match(body(r[1]), /Connecting to your TV/);
});

test('codeIssued is ignored while waiting, failed or closed', () => {
    const a = run(['bowtie_pair_codeIssued(s, "http://x/link?code=AAAA")', req(get(`/p?t=${T}`))]);
    assert.equal(a.state, 'waiting');
    assert.equal(status(a.r[0]), 'HTTP/1.1 200 OK');
    const b = run([req(post(`t=${T}&server=x.lan`)), 'bowtie_pair_connectFailed(s, s.address)', 'bowtie_pair_codeIssued(s, "http://x/link?code=AAAA")']);
    assert.equal(b.state, 'failed');
    const c = run(['bowtie_pair_close(s)', 'bowtie_pair_codeIssued(s, "http://x/link?code=AAAA")']);
    assert.equal(c.state, 'closed');
});

test('once closed every request is 403, even garbage or oversize', () => {
    const { r } = run([
        'bowtie_pair_close(s)',
        req('hello'), req(post('t=' + 'a'.repeat(9000))), req(get('/nope')), req(get(`/p?t=${T}`)),
    ]);
    for (const x of r) assert.equal(status(x), 'HTTP/1.1 403 Forbidden');
});

test('normalizeServer accepts host[:port] and rejects markup, paths and other schemes', () => {
    const cases = ['192.168.1.50:8400/', ' Bowtie.lan ', 'HTTPS://tv.local:443', '"><script>', 'http://a b', 'ftp://x', 'http://', 'http://x:', 'http://x:80a', 'http://x:123456', 'http://x/path', 'http://[::1]:8400', ''];
    const got = runBrs(LIB, `
        out.n = []
        for each c in [${cases.map(lit).join(', ')}]
            out.n.push(bowtie_pair_normalizeServer(c))
        end for
    `);
    assert.deepEqual(got.n, ['http://192.168.1.50:8400', 'http://Bowtie.lan', 'https://tv.local:443', '', '', '', '', '', '', '', '', '', '']);
});

test('linkUrl strips dashes and a trailing slash; duplicate form keys keep the first', () => {
    const got = runBrs(LIB, `out.l = bowtie_pair_linkUrl("http://a.lan:8400/", "BCDF-2345")`);
    assert.equal(got.l, 'http://a.lan:8400/link?code=BCDF2345');
    const out = run([req(post(`t=${T}&server=first.lan&server=second.lan`))]);
    assert.equal(out.r[0].submitted, 'http://first.lan');
});

test('an upper-case T parameter does not alias the token', () => {
    const { r } = run([req(get(`/p?T=${T}`))]);
    assert.equal(status(r[0]), 'HTTP/1.1 403 Forbidden');
});
