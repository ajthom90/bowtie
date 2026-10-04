package app.bowtie.core

import app.bowtie.core.BowtieClientRecordingsTest.Companion.TOKEN_PAIR
import app.bowtie.core.RecordingLogic.Badge
import app.bowtie.core.RecordingLogic.Tone
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

/** Series recording rules (`/recording-rules`) and their row wording. */
class BowtieClientRulesTest {

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
        take("/api/v1/auth/login")
        return c
    }

    private fun take(pathContains: String): RecordedRequest {
        val req = server.takeRequest(5, TimeUnit.SECONDS) ?: error("expected $pathContains")
        assertTrue("expected $pathContains, got ${req.path}", req.path!!.contains(pathContains))
        return req
    }

    @Test
    fun rules_decodeWireShape() = runBlocking {
        val c = loggedIn()
        server.enqueue(MockResponse().setBody("[${ruleJson()}]"))

        val rule = c.recordingRules().single()

        val req = take("/api/v1/recording-rules")
        assertEquals("GET", req.method)
        assertEquals("Bearer access-1", req.getHeader("Authorization"))
        assertEquals(4L, rule.id)
        assertEquals("Jeopardy!", rule.title)
        assertEquals("SH123", rule.seriesId)
        assertEquals(5L, rule.channelId)
        assertEquals("5.1 KSTP", rule.channelName)
        assertTrue(rule.newOnly)
        assertEquals(3, rule.keepLatest)
        assertEquals("bob", rule.scheduledBy)
        assertTrue(rule.canManage)
        assertEquals(Instant.parse("2026-10-01T12:00:00Z"), rule.createdAt)
    }

    @Test
    fun createRule_sendsAllFields_andDecodesScheduledCount() = runBlocking {
        val c = loggedIn()
        server.enqueue(MockResponse().setResponseCode(201).setBody("""{"rule":${ruleJson()},"scheduled":6}"""))

        val out = c.createRecordingRule(channelId = 5, programStart = Instant.parse("2026-10-04T00:00:00Z"))

        val req = take("/api/v1/recording-rules")
        assertEquals("POST", req.method)
        assertEquals(
            """{"channelId":5,"programStart":"2026-10-04T00:00:00Z","anyChannel":false,"newOnly":true,"keepLatest":0}""",
            req.body.readUtf8(),
        )
        assertEquals(6, out.scheduled)
        assertEquals(4L, out.rule.id)
    }

    @Test
    fun createRule_customOptions() = runBlocking {
        val c = loggedIn()
        server.enqueue(MockResponse().setResponseCode(201).setBody("""{"rule":${ruleJson()},"scheduled":0}"""))

        c.createRecordingRule(
            channelId = 5,
            programStart = Instant.parse("2026-10-04T00:00:00Z"),
            anyChannel = true,
            newOnly = false,
            keepLatest = 5,
        )

        assertEquals(
            """{"channelId":5,"programStart":"2026-10-04T00:00:00Z","anyChannel":true,"newOnly":false,"keepLatest":5}""",
            take("/api/v1/recording-rules").body.readUtf8(),
        )
    }

    @Test
    fun deleteRule_sendsDelete() = runBlocking {
        val c = loggedIn()
        server.enqueue(MockResponse().setResponseCode(204))
        c.deleteRecordingRule(4)
        val req = take("/api/v1/recording-rules/4")
        assertEquals("DELETE", req.method)
    }

    @Test
    fun rules503_isDvrOff_notTunersBusy() = runBlocking {
        val c = loggedIn()
        server.enqueue(MockResponse().setResponseCode(503).setBody("""{"error":"recording not available"}"""))
        try {
            c.recordingRules()
            fail("expected Server")
        } catch (e: BowtieError.Server) {
            assertEquals(503, e.status)
        }
    }

    @Test
    fun deleteRule403_readsOnlyTheScheduler() = runBlocking {
        val c = loggedIn()
        server.enqueue(MockResponse().setResponseCode(403).setBody("""{"error":"forbidden"}"""))
        try {
            c.deleteRecordingRule(4)
            fail("expected Server")
        } catch (e: BowtieError.Server) {
            assertEquals(
                "Only the person who scheduled it or an admin can change it.",
                RecordingLogic.errorMessage(e),
            )
        }
    }

    // ── Wording ─────────────────────────────────────────────────────────────

    private fun rec(state: String, failure: String = "", ruleId: Long = 0) = Recording(
        id = 1,
        title = "Jeopardy!",
        channelId = 5,
        start = Instant.parse("2026-10-04T00:00:00Z"),
        stop = Instant.parse("2026-10-04T00:30:00Z"),
        state = state,
        failure = failure,
        canManage = true,
        ruleId = ruleId,
    )

    @Test
    fun seriesRecording_hasSeriesBadge() {
        assertEquals(
            listOf(Badge("Scheduled", Tone.Neutral), Badge("Series", Tone.Neutral)),
            RecordingLogic.badges(rec(Recording.SCHEDULED, ruleId = 4)),
        )
        assertFalse(RecordingLogic.badges(rec(Recording.SCHEDULED)).any { it.text == "Series" })
    }

    @Test
    fun skippedEpisode_readsSkipped_notMissed() {
        val r = rec(Recording.FAILED, failure = "skipped", ruleId = 4)
        assertEquals("Skipped", RecordingLogic.failureLabel("skipped"))
        assertEquals("Skipped", RecordingLogic.statusLine(r))
        assertEquals(
            listOf(Badge("Skipped", Tone.Neutral), Badge("Series", Tone.Neutral)),
            RecordingLogic.badges(r),
        )
        // Other failures are unchanged.
        assertEquals("Missed: No tuner was free", RecordingLogic.statusLine(rec(Recording.FAILED, "noTuner")))
    }

    @Test
    fun seriesScheduledMessage_singularAndPlural() {
        assertEquals("Scheduled 1 episode", RecordingLogic.seriesScheduledMessage(1))
        assertEquals("Scheduled 6 episodes", RecordingLogic.seriesScheduledMessage(6))
        assertEquals("Scheduled 0 episodes", RecordingLogic.seriesScheduledMessage(0))
    }

    @Test
    fun ruleDetail_describesChannelEpisodesAndKeep() {
        val rule = BowtieJson.decodeFromString<RecordingRule>(ruleJson())
        assertEquals("5.1 KSTP · New episodes only · Keeps the latest 3", RecordingLogic.ruleDetail(rule))

        val any = rule.copy(channelId = 0, channelName = "", newOnly = false, keepLatest = 0, canManage = false)
        assertEquals("Any channel · All episodes · by bob", RecordingLogic.ruleDetail(any))
        assertNull(RecordingLogic.lockLabel(locked = false, rating = ""))
    }

    companion object {
        fun ruleJson(id: Long = 4, canManage: Boolean = true) = """
            {"id":$id,"title":"Jeopardy!","seriesId":"SH123","channelId":5,"channelName":"5.1 KSTP",
             "newOnly":true,"keepLatest":3,"scheduledBy":"bob","canManage":$canManage,
             "createdAt":"2026-10-01T12:00:00Z"}
        """.trimIndent()
    }
}
