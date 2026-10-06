package app.bowtie.core

import app.bowtie.core.BowtieClientRecordingsTest.Companion.TOKEN_PAIR
import kotlinx.coroutines.runBlocking
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import java.util.concurrent.TimeUnit

/** Heartbeat `?signal=1`: antenna reception on the 15 s beat (null = unknown). */
class HeartbeatSignalTest {

    private lateinit var server: MockWebServer

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
    }

    @After
    fun tearDown() {
        server.shutdown()
    }

    private suspend fun loggedIn(): BowtieClient {
        server.enqueue(MockResponse().setBody(TOKEN_PAIR))
        val c = BowtieClient(server.url("/"), InMemoryTokenStore())
        c.login("alice", "secret")
        server.takeRequest(1, TimeUnit.SECONDS)
        return c
    }

    private fun beat(code: Int, body: String = "") =
        MockResponse().setResponseCode(code).setBody(body)

    @Test
    fun sendsSignalOneAlongsideToken() = runBlocking {
        val c = loggedIn()
        server.enqueue(beat(204))
        c.heartbeat("v1", "tok")
        val path = server.takeRequest(1, TimeUnit.SECONDS)!!.path!!
        assertTrue("path: $path", path.startsWith("/api/v1/sessions/v1/heartbeat?"))
        assertTrue("path: $path", path.contains("signal=1"))
        assertTrue("path: $path", path.contains("token=tok"))
    }

    @Test
    fun weakReadingIsParsed() = runBlocking {
        val c = loggedIn()
        server.enqueue(
            beat(200, """{"signal":{"strength":96,"quality":46,"symbolQuality":0,"weak":true}}"""),
        )
        assertEquals(
            SignalReading(strength = 96, quality = 46, symbolQuality = 0, weak = true),
            c.heartbeat("v1", "tok"),
        )
    }

    @Test
    fun goodReadingIsNotWeak() = runBlocking {
        val c = loggedIn()
        server.enqueue(
            beat(200, """{"signal":{"strength":100,"quality":100,"symbolQuality":100,"weak":false}}"""),
        )
        assertEquals(false, c.heartbeat("v1", "tok")?.weak)
    }

    @Test
    fun nullSignalIsUnknown() = runBlocking {
        val c = loggedIn()
        server.enqueue(beat(200, """{"signal":null}"""))
        assertNull(c.heartbeat("v1", "tok"))
    }

    @Test
    fun olderServer204IsUnknown() = runBlocking {
        val c = loggedIn()
        server.enqueue(beat(204))
        assertNull(c.heartbeat("v1", "tok"))
    }

    @Test
    fun serverErrorIsUnknownAndSwallowed() = runBlocking {
        val c = loggedIn()
        server.enqueue(beat(500, """{"error":"boom"}"""))
        assertNull(c.heartbeat("v1", "tok"))
    }

    @Test
    fun malformedBodyIsUnknown() = runBlocking {
        val c = loggedIn()
        server.enqueue(beat(200, "<html>nope</html>"))
        assertNull(c.heartbeat("v1", "tok"))
    }

    @Test
    fun parental403StillThrows() = runBlocking {
        val c = loggedIn()
        server.enqueue(
            beat(403, """{"error":"Blocked by parental controls (rated TV-MA)","code":"parental"}"""),
        )
        try {
            c.heartbeat("v1", "tok")
            throw AssertionError("expected Parental")
        } catch (e: BowtieError.Parental) {
            assertEquals("Blocked by parental controls (rated TV-MA)", e.message)
        }
    }
}
