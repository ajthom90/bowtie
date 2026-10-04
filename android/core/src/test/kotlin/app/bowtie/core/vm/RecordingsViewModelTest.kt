package app.bowtie.core.vm

import app.bowtie.core.BowtieClient
import app.bowtie.core.BowtieClientRecordingsTest.Companion.TOKEN_PAIR
import app.bowtie.core.BowtieClientRecordingsTest.Companion.recordingJson
import app.bowtie.core.InMemoryTokenStore
import app.bowtie.core.RecordingLogic.Tab
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.runBlocking
import okhttp3.mockwebserver.Dispatcher
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import okhttp3.mockwebserver.RecordedRequest
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import java.util.concurrent.CopyOnWriteArrayList
import java.util.concurrent.LinkedBlockingQueue
import java.util.concurrent.TimeUnit

/** RecordingsViewModel over a real [BowtieClient] and MockWebServer. */
class RecordingsViewModelTest {

    private data class Req(val method: String, val path: String, val body: String)

    private lateinit var server: MockWebServer
    private val requests = CopyOnWriteArrayList<Req>()
    private val positionPuts = LinkedBlockingQueue<Req>()
    private val workScope = CoroutineScope(SupervisorJob() + Dispatchers.IO)

    /** Response bodies by list filter. */
    private val lists = mutableMapOf(
        "upcoming" to "[${recordingJson(id = 1, state = "scheduled")}]",
        "recorded" to "[${recordingJson(id = 2, state = "ready")}]",
        "failed" to "[${recordingJson(id = 3, state = "failed")}]",
    )
    private var listCode = 200
    private var actionCode = 204
    private var playBody = """{"playlistUrl":"/api/v1/recordings/2/hls/index.m3u8?token=t","positionSec":95,"durationSec":1800}"""

    @Before
    fun setUp() {
        server = MockWebServer()
        server.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse {
                val r = Req(request.method.orEmpty(), request.path.orEmpty(), request.body.readUtf8())
                requests += r
                val p = r.path
                return when {
                    r.method == "POST" && p == "/api/v1/auth/login" -> MockResponse().setBody(TOKEN_PAIR)
                    r.method == "GET" && p.startsWith("/api/v1/recordings?state=") ->
                        MockResponse().setResponseCode(listCode).setBody(
                            if (listCode == 200) lists[p.substringAfter("state=")]!! else """{"error":"boom"}""",
                        )
                    r.method == "PUT" && p.endsWith("/position") -> {
                        positionPuts += r
                        MockResponse().setResponseCode(204)
                    }
                    r.method == "POST" && p.endsWith("/play") -> MockResponse().setBody(playBody)
                    r.method == "PATCH" -> MockResponse().setResponseCode(if (actionCode == 204) 200 else actionCode)
                        .setBody(if (actionCode == 204) recordingJson(id = 2, state = "ready") else """{"error":"forbidden"}""")
                    r.method == "DELETE" || p.endsWith("/stop") ->
                        MockResponse().setResponseCode(actionCode)
                            .setBody(if (actionCode == 204) "" else """{"error":"forbidden"}""")
                    else -> MockResponse().setResponseCode(500).setBody("""{"error":"unhandled"}""")
                }
            }
        }
        server.start()
    }

    @After
    fun tearDown() {
        workScope.cancel()
        server.shutdown()
    }

    private suspend fun vm(): RecordingsViewModel {
        val client = BowtieClient(server.url("/"), InMemoryTokenStore())
        client.login("alice", "secret")
        return RecordingsViewModel(client = client, scope = workScope)
    }

    private fun lastList(): String = requests.last { it.method == "GET" }.path

    @Test
    fun startsOnUpcoming_andRefreshLoadsIt() = runBlocking {
        val m = vm()
        assertEquals(Tab.Upcoming, m.state.value.tab)
        assertTrue(m.state.value.load is RecordingsViewModel.Load.Loading)

        m.refresh()

        assertEquals("/api/v1/recordings?state=upcoming", lastList())
        val load = m.state.value.load as RecordingsViewModel.Load.Loaded
        assertEquals(listOf(1L), load.items.map { it.id })
    }

    @Test
    fun selectTab_loadsThatTabsFilter() = runBlocking {
        val m = vm()
        m.selectTab(Tab.Missed)
        assertEquals(Tab.Missed, m.state.value.tab)
        assertEquals("/api/v1/recordings?state=failed", lastList())
        assertEquals(3L, (m.state.value.load as RecordingsViewModel.Load.Loaded).items.single().id)

        m.selectTab(Tab.Recorded)
        assertEquals("/api/v1/recordings?state=recorded", lastList())
    }

    @Test
    fun refreshFailure_isFailedWithMessage() = runBlocking {
        val m = vm()
        listCode = 500
        m.refresh()
        val load = m.state.value.load as RecordingsViewModel.Load.Failed
        assertEquals("boom", load.message)
    }

    @Test
    fun delete_callsServer_thenReloadsCurrentTab() = runBlocking {
        val m = vm()
        m.refresh()
        lists["upcoming"] = "[]"
        val rec = (m.state.value.load as RecordingsViewModel.Load.Loaded).items.single()

        assertTrue(m.delete(rec))

        assertTrue(requests.any { it.method == "DELETE" && it.path == "/api/v1/recordings/1" })
        assertEquals("/api/v1/recordings?state=upcoming", lastList())
        assertEquals(emptyList<Long>(), (m.state.value.load as RecordingsViewModel.Load.Loaded).items.map { it.id })
        assertNull(m.state.value.message)
    }

    @Test
    fun stop_postsStop_thenReloads() = runBlocking {
        val m = vm()
        m.refresh()
        val rec = (m.state.value.load as RecordingsViewModel.Load.Loaded).items.single()
        assertTrue(m.stop(rec))
        assertTrue(requests.any { it.method == "POST" && it.path == "/api/v1/recordings/1/stop" })
        assertEquals(2, requests.count { it.method == "GET" })
    }

    @Test
    fun setKept_patchesProtected() = runBlocking {
        val m = vm()
        m.selectTab(Tab.Recorded)
        val rec = (m.state.value.load as RecordingsViewModel.Load.Loaded).items.single()
        assertTrue(m.setKept(rec, keep = false))
        val patch = requests.single { it.method == "PATCH" }
        assertEquals("/api/v1/recordings/2", patch.path)
        assertEquals("""{"protected":false}""", patch.body)
    }

    @Test
    fun actionFailure_setsMessage_keepsList() = runBlocking {
        val m = vm()
        m.refresh()
        actionCode = 403
        val rec = (m.state.value.load as RecordingsViewModel.Load.Loaded).items.single()

        assertFalse(m.delete(rec))

        assertEquals("Only the person who scheduled it or an admin can change it.", m.state.value.message)
        assertEquals(1L, (m.state.value.load as RecordingsViewModel.Load.Loaded).items.single().id)
        m.clearMessage()
        assertNull(m.state.value.message)
    }

    @Test
    fun play_offersResume_fromPlayResponse() = runBlocking {
        val m = vm()
        m.selectTab(Tab.Recorded)
        val rec = (m.state.value.load as RecordingsViewModel.Load.Loaded).items.single()

        val start = m.play(rec)

        assertNotNull(start)
        start!!
        assertEquals("/api/v1/recordings/2/hls/index.m3u8?token=t", start.playlistUrl)
        assertTrue(start.offerResume)
        assertEquals(95, start.resumeAtSec)
        assertEquals(1800, start.durationSec)
        assertEquals(2L, start.recording.id)
    }

    @Test
    fun play_nearEnd_startsOver_noOffer() = runBlocking {
        playBody = """{"playlistUrl":"/x.m3u8?token=t","positionSec":1790,"durationSec":1800}"""
        val m = vm()
        m.selectTab(Tab.Recorded)
        val rec = (m.state.value.load as RecordingsViewModel.Load.Loaded).items.single()
        val start = m.play(rec)!!
        assertFalse(start.offerResume)
    }

    @Test
    fun play_notReady_returnsNull_withMessage() = runBlocking {
        playBody = "unused"
        val m = vm()
        m.selectTab(Tab.Recorded)
        val rec = (m.state.value.load as RecordingsViewModel.Load.Loaded).items.single()
        server.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest) =
                MockResponse().setResponseCode(409).setBody("""{"error":"this recording isn't ready to play yet"}""")
        }
        assertNull(m.play(rec))
        assertEquals("this recording isn't ready to play yet", m.state.value.message)
    }

    @Test
    fun savePosition_fireAndForget_inOrder_wholeSeconds_clampedAtZero() = runBlocking {
        val m = vm()
        // Last save wins on the server, so saves must arrive in call order.
        m.savePosition(recordingId = 2, positionMs = 754_900)
        m.savePosition(recordingId = 2, positionMs = -50)

        val a = positionPuts.poll(5, TimeUnit.SECONDS)!!
        val b = positionPuts.poll(5, TimeUnit.SECONDS)!!
        assertEquals("""{"positionSec":754}""", a.body)
        assertEquals("""{"positionSec":0}""", b.body)
        assertEquals("/api/v1/recordings/2/position", a.path)
    }
}
