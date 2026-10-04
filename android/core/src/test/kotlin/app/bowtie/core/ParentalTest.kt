package app.bowtie.core

import app.bowtie.core.BowtieClientRecordingsTest.Companion.TOKEN_PAIR
import app.bowtie.core.RecordingLogic.Action
import kotlinx.coroutines.runBlocking
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Before
import org.junit.Test
import java.time.Instant

/** Parental controls: the 403 `code: "parental"` mapping, locked models and row rules. */
class ParentalTest {

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
        return c
    }

    private val caps = ClientCaps(listOf("h264"), listOf("aac"), 1080, "")

    private val parentalBody =
        """{"error":"Blocked by parental controls (rated TV-MA)","code":"parental"}"""

    @Test
    fun sessionStart403Parental_mapsToParentalWithServerMessage() = runBlocking {
        val c = loggedIn()
        server.enqueue(MockResponse().setResponseCode(403).setBody(parentalBody))
        try {
            c.createSession(5, caps)
            fail("expected Parental")
        } catch (e: BowtieError.Parental) {
            assertEquals("Blocked by parental controls (rated TV-MA)", e.message)
        }
    }

    @Test
    fun plain403_staysServer403() = runBlocking {
        val c = loggedIn()
        server.enqueue(MockResponse().setResponseCode(403).setBody("""{"error":"forbidden"}"""))
        try {
            c.deleteRecording(3)
            fail("expected Server")
        } catch (e: BowtieError.Server) {
            assertEquals(403, e.status)
            assertEquals(
                "Only the person who scheduled it or an admin can change it.",
                RecordingLogic.errorMessage(e),
            )
        }
    }

    @Test
    fun recordingPlay403Parental_errorMessageIsTheServers() = runBlocking {
        val c = loggedIn()
        server.enqueue(
            MockResponse().setResponseCode(403)
                .setBody("""{"error":"Blocked by parental controls (rated R)","code":"parental"}"""),
        )
        try {
            c.playRecording(7)
            fail("expected Parental")
        } catch (e: BowtieError.Parental) {
            assertEquals("Blocked by parental controls (rated R)", RecordingLogic.errorMessage(e))
            assertEquals("Blocked by parental controls (rated R)", RecordingLogic.scheduleErrorMessage(e))
        }
    }

    @Test
    fun heartbeat403Parental_throwsParental() = runBlocking {
        val c = loggedIn()
        server.enqueue(MockResponse().setResponseCode(403).setBody(parentalBody))
        try {
            c.heartbeat("v1", "tok")
            fail("expected Parental")
        } catch (e: BowtieError.Parental) {
            assertEquals("Blocked by parental controls (rated TV-MA)", e.message)
        }
    }

    @Test
    fun heartbeatOther403_isStillSwallowed() = runBlocking {
        val c = loggedIn()
        server.enqueue(MockResponse().setResponseCode(403).setBody("""{"error":"bad token"}"""))
        c.heartbeat("v1", "tok") // must not throw
        server.enqueue(MockResponse().setResponseCode(404).setBody("""{"error":"viewer not found"}"""))
        c.heartbeat("v1", "tok") // must not throw
    }

    @Test
    fun guideProgram_decodesRatingLockAndIds_withOldServerDefaults() {
        val p = BowtieJson.decodeFromString<GuideProgram>(
            """{"start":"2026-10-04T00:00:00Z","stop":"2026-10-04T01:00:00Z","title":"Late Show",
               "subtitle":"","description":"","category":"","rating":"TV-MA","locked":true,
               "programId":"EP1","seriesId":"SH1","isNew":true}""",
        )
        assertEquals("TV-MA", p.rating)
        assertTrue(p.locked)
        assertEquals("EP1", p.programId)
        assertEquals("SH1", p.seriesId)
        assertEquals(true, p.isNew)

        val old = BowtieJson.decodeFromString<GuideProgram>(
            """{"start":"2026-10-04T00:00:00Z","stop":"2026-10-04T01:00:00Z","title":"News",
               "subtitle":"","description":"Local news","category":""}""",
        )
        assertEquals("", old.rating)
        assertFalse(old.locked)
        assertNull(old.seriesId)
    }

    @Test
    fun recording_decodesRatingRuleIdAndLock() {
        val r = BowtieJson.decodeFromString<Recording>(
            """{"id":1,"title":"Movie","channelId":5,"start":"2026-10-04T00:00:00Z",
               "stop":"2026-10-04T02:00:00Z","state":"ready","rating":"R","ruleId":4,"locked":true}""",
        )
        assertEquals("R", r.rating)
        assertEquals(4L, r.ruleId)
        assertTrue(r.locked)
    }

    private fun rec(locked: Boolean, rating: String = "TV-MA", state: String = Recording.READY) = Recording(
        id = 1,
        title = "Movie",
        channelId = 5,
        start = Instant.parse("2026-10-04T00:00:00Z"),
        stop = Instant.parse("2026-10-04T02:00:00Z"),
        state = state,
        canManage = true,
        rating = rating,
        locked = locked,
    )

    @Test
    fun lockedRecording_cannotPlay_butCanStillBeManaged() {
        val actions = RecordingLogic.actions(rec(locked = true))
        assertFalse(Action.Play in actions)
        assertTrue(Action.Delete in actions)
        assertTrue(Action.Play in RecordingLogic.actions(rec(locked = false)))
    }

    @Test
    fun lockLabel_showsRatingOnlyWhenLocked() {
        assertEquals("🔒 TV-MA", RecordingLogic.lockLabel(locked = true, rating = "TV-MA"))
        assertEquals("🔒 Not rated", RecordingLogic.lockLabel(locked = true, rating = ""))
        assertNull(RecordingLogic.lockLabel(locked = false, rating = "TV-MA"))
        assertEquals("🔒 TV-MA", RecordingLogic.lockLabel(rec(locked = true)))
    }
}
