package app.bowtie.core.pairing

import kotlinx.coroutines.*
import org.junit.Assert.*
import org.junit.Test
import java.net.Socket

class PairingServerTest {
    private fun exchange(port: Int, vararg parts: String, gapMs: Long = 100): String =
        Socket("127.0.0.1", port).use { sock ->
            parts.forEachIndexed { i, p -> if (i > 0) Thread.sleep(gapMs); sock.getOutputStream().write(p.toByteArray()); sock.getOutputStream().flush() }
            sock.getInputStream().readBytes().toString(Charsets.UTF_8)
        }

    @Test fun bodyInASecondWriteStillSubmits() {
        val t = "0123456789abcdef01234567"
        val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
        var got: String? = null
        val server = PairingServer(PairingSession(t)) { got = it }
        val port = server.start(scope)!!
        try {
            val body = "t=$t&server=bowtie.lan%3A8400"
            val reply = exchange(port, "POST /p HTTP/1.1\r\nContent-Length: ${body.length}\r\n\r\n", body)
            assertTrue(reply.startsWith("HTTP/1.1 303 See Other\r\n"))
            assertEquals("http://bowtie.lan:8400", got)
        } finally { server.stop(); scope.cancel() }
    }

    @Test fun coordinatorRedirectsOnlyForTheSubmittedServer() = runBlocking {
        val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
        val connected = mutableListOf<String>()
        val c = PairingCoordinator(scope, connect = { connected += it; true }, host = { "127.0.0.1" })
        c.start()
        val pair = withTimeout(2000) { while (c.pairUrl.value == null) delay(10); c.pairUrl.value!! }
        val port = pair.substringAfterLast(':').substringBefore('/').toInt()
        val token = pair.substringAfter("t=")
        val body = "t=$token&server=http%3A%2F%2Fbowtie.lan%3A8400"
        exchange(port, "POST /p HTTP/1.1\r\nContent-Length: ${body.length}\r\n\r\n$body")
        withTimeout(2000) { while (connected.isEmpty()) delay(10) }
        assertEquals(listOf("http://bowtie.lan:8400"), connected)
        c.codeIssued("http://elsewhere:1", "AAAA-BBBB")
        assertTrue(exchange(port, "GET /p/status?t=$token HTTP/1.1\r\n\r\n").contains("Connecting to your TV"))
        c.codeIssued("http://bowtie.lan:8400", "BCDF-2345")
        assertTrue(exchange(port, "GET /p/status?t=$token HTTP/1.1\r\n\r\n").contains("Location: http://bowtie.lan:8400/link?code=BCDF2345\r\n"))
        c.stop()
        assertNull(c.pairUrl.value)
        scope.cancel()
    }

    @Test fun lockoutThenStartIssuesAFreshSessionAndToken() = runBlocking {
        val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
        val c = PairingCoordinator(scope, connect = { true }, host = { "127.0.0.1" })
        try {
            c.start()
            val first = withTimeout(2000) { while (c.pairUrl.value == null) delay(10); c.pairUrl.value!! }
            val port = first.substringAfterLast(':').substringBefore('/').toInt()
            repeat(20) { exchange(port, "GET /p?t=wrong HTTP/1.1\r\n\r\n") }
            withTimeout(2000) { while (c.pairUrl.value != null) delay(10) }
            c.start()
            val second = withTimeout(2000) { while (c.pairUrl.value == null) delay(10); c.pairUrl.value!! }
            assertNotEquals(first, second)
            val port2 = second.substringAfterLast(':').substringBefore('/').toInt()
            assertTrue(exchange(port2, "GET /p?t=${second.substringAfter("t=")} HTTP/1.1\r\n\r\n").startsWith("HTTP/1.1 200 OK"))
            c.start()
            assertEquals(second, c.pairUrl.value)
        } finally { c.stop(); scope.cancel() }
    }
}
