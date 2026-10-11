package app.bowtie.core.pairing

import org.junit.Assert.*
import org.junit.Test

class PairingSessionTest {
    private val t = "0123456789abcdef01234567"
    private fun get(path: String) = HttpRequest.parse("GET $path HTTP/1.1\r\nHost: tv\r\n\r\n".toByteArray())
    private fun post(body: String) = HttpRequest.parse(
        "POST /p HTTP/1.1\r\nContent-Type: application/x-www-form-urlencoded\r\nContent-Length: ${body.toByteArray().size}\r\n\r\n$body".toByteArray())
    private fun text(r: PairingResponse) = r.body.toString(Charsets.UTF_8)
    private fun header(r: PairingResponse, name: String) = r.headers.firstOrNull { it.first.equals(name, true) }?.second

    @Test fun formShowsToken() {
        val (r, sub) = PairingSession(t).handle(get("/p?t=$t"))
        assertEquals(200, r.status); assertNull(sub)
        assertTrue(text(r).contains("<h1>Set up Bowtie on your TV</h1>"))
        assertTrue(text(r).contains("type=\"text\" inputmode=\"url\""))
        assertTrue(text(r).contains("name=\"t\" value=\"$t\""))
        assertEquals("close", header(r, "Connection"))
        assertEquals(r.body.size.toString(), header(r, "Content-Length"))
    }

    @Test fun postNormalizesSubmitsOnceAndRedirects() {
        val s = PairingSession(t)
        val (r, sub) = s.handle(post("t=$t&server=192.168.1.50%3A8400%2F"))
        assertEquals(303, r.status)
        assertEquals("/p/status?t=$t", header(r, "Location"))
        assertEquals("http://192.168.1.50:8400", sub)
        assertNull(s.handle(post("t=$t&server=other")).second)
    }

    @Test fun badAddressReshowsForm() {
        val s = PairingSession(t)
        val (r, sub) = s.handle(post("t=$t&server=ftp%3A%2F%2Fx"))
        assertEquals(200, r.status); assertNull(sub)
        assertTrue(text(r).contains("Enter an address like http://192.168.1.50:8400."))
        assertTrue(text(r).contains("value=\"ftp://x\""))
        assertEquals(PairingSession.State.Waiting, s.state)
    }

    @Test fun statusWaitsThenRedirectsToLatestCode() {
        val s = PairingSession(t)
        s.handle(post("t=$t&server=http%3A%2F%2Fbowtie.lan%3A8400"))
        val waiting = s.handle(get("/p/status?t=$t")).first
        assertTrue(text(waiting).contains("Connecting to your TV…"))
        assertTrue(text(waiting).contains("content=\"1;url=/p/status?t=$t\""))
        s.codeIssued(PairingSession.linkUrl("http://bowtie.lan:8400", "BCDF-2345"))
        assertEquals("http://bowtie.lan:8400/link?code=BCDF2345", header(s.handle(get("/p/status?t=$t")).first, "Location"))
        s.codeIssued(PairingSession.linkUrl("http://bowtie.lan:8400", "GHJK-6789"))
        assertEquals("http://bowtie.lan:8400/link?code=GHJK6789", header(s.handle(get("/p/status?t=$t")).first, "Location"))
        assertEquals("/p/status?t=$t", header(s.handle(get("/p?t=$t")).first, "Location"))
    }

    @Test fun connectFailureAllowsRetry() {
        val s = PairingSession(t)
        val sub = s.handle(post("t=$t&server=bowtie.lan")).second!!
        s.connectFailed(sub)
        val page = text(s.handle(get("/p/status?t=$t")).first)
        assertTrue(page.contains("Couldn&#39;t reach a Bowtie server at http://bowtie.lan. Check the address and try again."))
        assertEquals("http://bowtie2.lan", s.handle(post("t=$t&server=bowtie2.lan")).second)
    }

    @Test fun htmlInAddressIsRejectedAndEscaped() {
        val (r, sub) = PairingSession(t).handle(post("t=$t&server=%22%3E%3Cscript%3E"))
        assertNull(sub)
        assertFalse(text(r).contains("<script>"))
        assertTrue(text(r).contains("value=\"&quot;&gt;&lt;script&gt;\""))
    }

    @Test fun lockoutCountsOnlyWrongTokens() {
        val s = PairingSession(t)
        repeat(30) {
            assertEquals(404, s.handle(get("/favicon.ico")).first.status)
            assertEquals(403, s.handle(get("/p")).first.status)
        }
        repeat(19) { s.handle(get("/p?t=nope")) }
        assertEquals(200, s.handle(get("/p?t=$t")).first.status)
        s.handle(get("/p/status?t=nope"))
        assertEquals(403, s.handle(get("/p?t=$t")).first.status)
        assertEquals(PairingSession.State.Closed, s.state)
    }

    @Test fun parserLimits() {
        assertEquals(Parse.Bad, HttpRequest.parse("hello\r\n\r\n".toByteArray()))
        assertEquals(Parse.Incomplete, HttpRequest.parse("GET /p HTTP/1.1\r\nHost: x".toByteArray()))
        val big = "t=" + "a".repeat(9000)
        assertEquals(Parse.TooLarge, HttpRequest.parse("POST /p HTTP/1.1\r\nContent-Length: ${big.length}\r\n\r\n$big".toByteArray()))
        assertEquals(Parse.Incomplete, HttpRequest.parse("POST /p HTTP/1.1\r\nContent-Length: 10\r\n\r\nt=1".toByteArray()))
        assertEquals(404, PairingSession(t).handle(HttpRequest.parse("PUT /p?t=$t HTTP/1.1\r\n\r\n".toByteArray())).first.status)
    }

    @Test fun decodingAndToken() {
        assertEquals("x y/z:", HttpRequest.urlDecode("x+y%2Fz%3a"))
        val tok = PairingSession.newToken()
        assertEquals(24, tok.length)
        assertTrue(tok.all { it in "0123456789abcdef" })
    }
}
