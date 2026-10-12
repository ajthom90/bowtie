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
        val got = java.util.concurrent.atomic.AtomicReference<String?>()
        val server = PairingServer(PairingSession(t)) { got.set(it) }
        val port = server.start(scope, "127.0.0.1")!!
        try {
            val body = "t=$t&server=bowtie.lan%3A8400"
            val reply = exchange(port, "POST /p HTTP/1.1\r\nContent-Length: ${body.length}\r\n\r\n", body)
            assertTrue(reply.startsWith("HTTP/1.1 303 See Other\r\n"))
            assertEquals("http://bowtie.lan:8400", got.get())
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

    private fun withServer(block: (port: Int, token: String) -> Unit) {
        val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
        val session = PairingSession()
        val server = PairingServer(session) { }
        val port = server.start(scope, "127.0.0.1")!!
        try { block(port, session.token) } finally { server.stop(); scope.cancel() }
    }

    private fun assertServes(port: Int, token: String) =
        assertTrue(exchange(port, "GET /p?t=$token HTTP/1.1\r\n\r\n").startsWith("HTTP/1.1 200 OK"))

    @Test fun slowDripIsDroppedByTheWholeRequestDeadline() = withServer { port, token ->
        val start = System.nanoTime()
        Socket("127.0.0.1", port).use { sock ->
            sock.soTimeout = 15000
            val out = sock.getOutputStream()
            var closed = false
            try {
                for (c in "GET /p?t=$token HTTP/1.1\r\n") { out.write(c.code); out.flush(); Thread.sleep(1000) }
            } catch (_: java.io.IOException) { closed = true }
            if (!closed) closed = runCatching { sock.getInputStream().read() }.getOrDefault(-1) == -1
            assertTrue(closed)
        }
        assertTrue((System.nanoTime() - start) / 1_000_000 < 7000)
        assertServes(port, token)
    }

    @Test fun oversizeRequestGets413AndServerSurvives() = withServer { port, token ->
        assertTrue(exchange(port, "POST /p HTTP/1.1\r\nContent-Length: 9000\r\n\r\n").startsWith("HTTP/1.1 413"))
        assertServes(port, token)
        val big = "GET /p?t=" + "a".repeat(9000) + " HTTP/1.1\r\n\r\n"
        assertTrue(exchange(port, big).startsWith("HTTP/1.1 413"))
        assertServes(port, token)
    }

    @Test fun garbageGets400AndServerSurvives() = withServer { port, token ->
        assertTrue(exchange(port, "\u0001\u0002garbage\r\n\r\n").startsWith("HTTP/1.1 400"))
        assertServes(port, token)
    }

    @Test fun silentClientDoesNotBlockTheNextRequestForever() = withServer { port, token ->
        Socket("127.0.0.1", port).use {
            assertServes(port, token) // queued behind the silent client until it times out
        }
    }

    @Test fun silentConnectionDoesNotStallAnotherRequest() = withServer { port, token ->
        Socket("127.0.0.1", port).use {
            val start = System.nanoTime()
            assertServes(port, token)
            assertTrue((System.nanoTime() - start) / 1_000_000 < 1000)
        }
    }

    @Test fun fifthConcurrentConnectionIsClosedImmediately() = withServer { port, token ->
        val silent = List(4) { Socket("127.0.0.1", port) }
        try {
            Thread.sleep(200) // let the server accept all four
            Socket("127.0.0.1", port).use { fifth ->
                fifth.soTimeout = 2000
                assertEquals(-1, fifth.getInputStream().read())
            }
        } finally { silent.forEach { it.close() } }
        Thread.sleep(200)
        assertServes(port, token)
    }
}
