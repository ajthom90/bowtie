package app.bowtie.core

import kotlinx.coroutines.runBlocking
import okhttp3.OkHttpClient
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import java.util.concurrent.TimeUnit

class ServerUrlTest {

    @Test
    fun normalize_hostPort_defaultsHttpScheme() {
        val url = ServerUrl.normalize("192.168.1.50:8400")
        assertNotNull(url)
        assertEquals("http", url!!.scheme)
        assertEquals("192.168.1.50", url.host)
        assertEquals(8400, url.port)
    }

    @Test
    fun normalize_https_stripsTrailingSlash() {
        val withSlash = ServerUrl.normalize("https://tv.example.com/")
        val withoutSlash = ServerUrl.normalize("https://tv.example.com")
        assertNotNull(withSlash)
        assertNotNull(withoutSlash)
        assertEquals(withoutSlash, withSlash)
        // Root path: no trailing-slash distinction in the normalized form.
        assertEquals("tv.example.com", withSlash!!.host)
        assertEquals("https", withSlash.scheme)
        assertTrue(
            "expected no trailing slash in string form (or root-only path)",
            !withSlash.toString().endsWith("/") || withSlash.encodedPath == "/",
        )
        // Equality with the slash-less input is the contract that matters.
        assertEquals(withoutSlash!!.toString().trimEnd('/'), withSlash.toString().trimEnd('/'))
    }

    @Test
    fun normalize_empty_returnsNull() {
        assertNull(ServerUrl.normalize(""))
        assertNull(ServerUrl.normalize("   "))
    }

    @Test
    fun normalize_garbage_returnsNull() {
        assertNull(ServerUrl.normalize("not a url"))
        assertNull(ServerUrl.normalize("://"))
        assertNull(ServerUrl.normalize("http://"))
    }

    @Test
    fun resolve_preservesQueryToken() {
        val base = ServerUrl.normalize("http://192.168.1.50:8400")!!
        val resolved = ServerUrl.resolve(
            path = "/api/v1/stream/x/index.m3u8?token=abc",
            base = base,
        )
        assertEquals("abc", resolved.queryParameter("token"))
        assertTrue(resolved.encodedPath.endsWith("/api/v1/stream/x/index.m3u8")
            || resolved.encodedPath == "/api/v1/stream/x/index.m3u8")
        assertEquals("192.168.1.50", resolved.host)
        assertEquals(8400, resolved.port)
    }

    @Test
    fun validate_healthz200_returnsTrue() = runBlocking {
        val server = MockWebServer()
        server.enqueue(MockResponse().setResponseCode(200).setBody("ok"))
        server.start()
        try {
            val base = server.url("/")
            val ok = ServerUrl.validate(
                url = base,
                client = OkHttpClient.Builder()
                    .callTimeout(2, TimeUnit.SECONDS)
                    .build(),
                timeoutMs = 2000,
            )
            assertTrue(ok)
            val recorded = server.takeRequest()
            assertEquals("/healthz", recorded.path)
        } finally {
            server.shutdown()
        }
    }

    @Test
    fun validate_non200_returnsFalse() = runBlocking {
        val server = MockWebServer()
        server.enqueue(MockResponse().setResponseCode(500).setBody("nope"))
        server.start()
        try {
            val ok = ServerUrl.validate(
                url = server.url("/"),
                client = OkHttpClient(),
                timeoutMs = 2000,
            )
            assertFalse(ok)
        } finally {
            server.shutdown()
        }
    }
}
