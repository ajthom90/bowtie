package app.bowtie.core

import kotlinx.coroutines.runBlocking
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import okhttp3.mockwebserver.RecordedRequest
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Before
import org.junit.Test
import java.time.Instant
import java.util.concurrent.TimeUnit

/** DVR endpoints on [BowtieClient] (OpenAPI tag `dvr`). */
class BowtieClientRecordingsTest {

    private lateinit var server: MockWebServer
    private lateinit var store: InMemoryTokenStore

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
        store = InMemoryTokenStore()
    }

    @After
    fun tearDown() {
        server.shutdown()
    }

    private suspend fun loggedIn(): BowtieClient {
        server.enqueue(MockResponse().setBody(TOKEN_PAIR))
        val c = BowtieClient(server.url("/"), store)
        c.login("alice", "secret")
        take("/api/v1/auth/login")
        return c
    }

    private fun take(pathContains: String): RecordedRequest {
        val req = server.takeRequest(5, TimeUnit.SECONDS) ?: error("expected $pathContains")
        assertTrue("expected $pathContains, got ${req.path}", req.path!!.contains(pathContains))
        return req
    }

    @Test
    fun recordings_decodesWireShape_andSendsStateFilter() = runBlocking {
        val c = loggedIn()
        server.enqueue(MockResponse().setBody("[${recordingJson()}]"))

        val list = c.recordings(RecordingLogic.Tab.Missed)

        val req = take("/api/v1/recordings")
        assertEquals("GET", req.method)
        assertEquals("/api/v1/recordings?state=failed", req.path)
        assertEquals("Bearer access-1", req.getHeader("Authorization"))

        val r = list.single()
        assertEquals(7L, r.id)
        assertEquals("Jeopardy!", r.title)
        assertEquals(5L, r.channelId)
        assertEquals("5.1 KSTP", r.channelName)
        assertEquals(Instant.parse("2026-10-04T00:00:00Z"), r.start)
        assertEquals(Instant.parse("2026-10-04T00:30:00Z"), r.stop)
        assertEquals("failed", r.state)
        assertEquals("noTuner", r.failure)
        assertTrue(r.partial)
        assertTrue(r.isProtected)
        assertEquals(1800, r.durationSec)
        assertEquals(1_900_000_000L, r.sizeBytes)
        assertEquals(120, r.positionSec)
        assertEquals("bob", r.scheduledBy)
        assertTrue(r.canManage)
    }

    @Test
    fun schedule_sendsChannelAndExactProgramStart_withoutForce() = runBlocking {
        val c = loggedIn()
        server.enqueue(
            MockResponse().setResponseCode(201).setBody(
                """{"recording":${recordingJson(state = "scheduled")},"warnings":[{"code":"usesAllTuners","message":"If Plex is using a tuner then, this may not record."}]}""",
            ),
        )

        val out = c.scheduleRecording(channelId = 5, programStart = Instant.parse("2026-10-04T00:00:00Z"))

        val req = take("/api/v1/recordings")
        assertEquals("POST", req.method)
        assertEquals(
            """{"channelId":5,"programStart":"2026-10-04T00:00:00Z"}""",
            req.body.readUtf8(),
        )
        assertEquals("scheduled", out.recording.state)
        assertEquals("usesAllTuners", out.warnings.single().code)
    }

    @Test
    fun schedule_force_sendsForceTrue() = runBlocking {
        val c = loggedIn()
        server.enqueue(MockResponse().setResponseCode(201).setBody("""{"recording":${recordingJson()},"warnings":[]}"""))

        c.scheduleRecording(5, Instant.parse("2026-10-04T00:00:00Z"), force = true)

        assertEquals(
            """{"channelId":5,"programStart":"2026-10-04T00:00:00Z","force":true}""",
            take("/api/v1/recordings").body.readUtf8(),
        )
    }

    @Test
    fun schedule_409_isRecordingConflict() = runBlocking {
        val c = loggedIn()
        server.enqueue(
            MockResponse().setResponseCode(409).setBody(
                """{"error":"not enough tuners","tunerCount":2,"conflicts":[${recordingJson(state = "scheduled")}]}""",
            ),
        )
        try {
            c.scheduleRecording(5, Instant.parse("2026-10-04T00:00:00Z"))
            fail("expected conflict")
        } catch (e: BowtieError.RecordingConflict) {
            assertEquals("not enough tuners", e.message)
            assertEquals(2, e.tunerCount)
            assertEquals(7L, e.conflicts.single().id)
        }
    }

    @Test
    fun schedule_503_isServerError_notTunersBusy() = runBlocking {
        val c = loggedIn()
        server.enqueue(MockResponse().setResponseCode(503).setBody("""{"error":"recording is not available"}"""))
        try {
            c.scheduleRecording(5, Instant.parse("2026-10-04T00:00:00Z"))
            fail("expected error")
        } catch (e: BowtieError.Server) {
            assertEquals(503, e.status)
        }
    }

    @Test
    fun play_409_notReady_isPlainServerError() = runBlocking {
        val c = loggedIn()
        server.enqueue(MockResponse().setResponseCode(409).setBody("""{"error":"this recording isn't ready to play yet"}"""))
        try {
            c.playRecording(7)
            fail("expected error")
        } catch (e: BowtieError.Server) {
            assertEquals(409, e.status)
            assertEquals("this recording isn't ready to play yet", e.message)
        }
    }

    @Test
    fun play_returnsServerRelativePlaylistAndPosition() = runBlocking {
        val c = loggedIn()
        server.enqueue(
            MockResponse().setBody(
                """{"playlistUrl":"/api/v1/recordings/7/hls/index.m3u8?token=abc","positionSec":95,"durationSec":1800}""",
            ),
        )
        val p = c.playRecording(7)
        val req = take("/api/v1/recordings/7/play")
        assertEquals("POST", req.method)
        assertEquals("/api/v1/recordings/7/hls/index.m3u8?token=abc", p.playlistUrl)
        assertEquals(95, p.positionSec)
        assertEquals(1800, p.durationSec)
    }

    @Test
    fun delete_and_stop() = runBlocking {
        val c = loggedIn()
        server.enqueue(MockResponse().setResponseCode(204))
        server.enqueue(MockResponse().setResponseCode(204))

        c.deleteRecording(7)
        c.stopRecording(8)

        val del = take("/api/v1/recordings/7")
        assertEquals("DELETE", del.method)
        assertEquals("/api/v1/recordings/7", del.path)
        val stop = take("/api/v1/recordings/8/stop")
        assertEquals("POST", stop.method)
        assertEquals("Bearer access-1", stop.getHeader("Authorization"))
    }

    @Test
    fun delete_403_surfacesServerMessage() = runBlocking {
        val c = loggedIn()
        server.enqueue(MockResponse().setResponseCode(403).setBody("""{"error":"forbidden"}"""))
        try {
            c.deleteRecording(7)
            fail("expected error")
        } catch (e: BowtieError.Server) {
            assertEquals(403, e.status)
        }
    }

    @Test
    fun savePosition_putsExactBody_includingZero() = runBlocking {
        val c = loggedIn()
        server.enqueue(MockResponse().setResponseCode(204))
        server.enqueue(MockResponse().setResponseCode(204))

        c.saveRecordingPosition(7, 754)
        c.saveRecordingPosition(7, 0)

        val a = take("/api/v1/recordings/7/position")
        assertEquals("PUT", a.method)
        assertEquals("""{"positionSec":754}""", a.body.readUtf8())
        assertEquals("application/json", a.getHeader("Content-Type")?.substringBefore(';'))
        val b = take("/api/v1/recordings/7/position")
        assertEquals("""{"positionSec":0}""", b.body.readUtf8())
    }

    @Test
    fun setProtected_patchesExactBody_includingFalse() = runBlocking {
        val c = loggedIn()
        server.enqueue(MockResponse().setBody(recordingJson(protected = true)))
        server.enqueue(MockResponse().setBody(recordingJson(protected = false)))

        val kept = c.setRecordingProtected(7, true)
        val unkept = c.setRecordingProtected(7, false)

        val a = take("/api/v1/recordings/7")
        assertEquals("PATCH", a.method)
        assertEquals("""{"protected":true}""", a.body.readUtf8())
        val b = take("/api/v1/recordings/7")
        assertEquals("""{"protected":false}""", b.body.readUtf8())
        assertTrue(kept.isProtected)
        assertFalse(unkept.isProtected)
    }

    @Test
    fun guideProgram_decodesRecordingMark() {
        val json = """
            {"start":"2026-10-04T00:00:00Z","stop":"2026-10-04T00:30:00Z","title":"T","subtitle":"","description":"","category":"",
             "recording":{"id":7,"state":"scheduled"}}
        """.trimIndent()
        val p = BowtieJson.decodeFromString<GuideProgram>(json)
        assertEquals(GuideRecordingMark(id = 7, state = "scheduled"), p.recording)

        val bare = BowtieJson.decodeFromString<GuideProgram>(
            """{"start":"2026-10-04T00:00:00Z","stop":"2026-10-04T00:30:00Z","title":"T","subtitle":"","description":"","category":""}""",
        )
        assertNull(bare.recording)
    }

    companion object {
        const val TOKEN_PAIR = """
            {"accessToken":"access-1","refreshToken":"refresh-1",
             "user":{"id":1,"username":"alice","role":"viewer","maxQuality":"high"}}
        """

        fun recordingJson(
            id: Long = 7,
            state: String = "failed",
            protected: Boolean = true,
        ) = """
            {"id":$id,"title":"Jeopardy!","subtitle":"","description":"Quiz","category":"Game",
             "channelId":5,"channelName":"5.1 KSTP",
             "start":"2026-10-04T00:00:00Z","stop":"2026-10-04T00:30:00Z",
             "state":"$state","partial":true,"failure":"noTuner","failureDetail":"tuners busy",
             "durationSec":1800,"sizeBytes":1900000000,"protected":$protected,"positionSec":120,
             "scheduledBy":"bob","canManage":true}
        """.trimIndent()
    }
}
