package app.bowtie.core.vm

import app.bowtie.core.BowtieClient
import app.bowtie.core.BowtieClientRecordingsTest.Companion.TOKEN_PAIR
import app.bowtie.core.BowtieClientRecordingsTest.Companion.recordingJson
import app.bowtie.core.BowtieClientRulesTest.Companion.ruleJson
import app.bowtie.core.Channel
import app.bowtie.core.GuideRecordingMark
import app.bowtie.core.InMemoryTokenStore
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
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import java.time.Instant
import java.util.concurrent.CopyOnWriteArrayList

/** Guide search (`GET /guide/search`) and its Watch / Record / Record series actions. */
class SearchViewModelTest {

    private data class Req(val method: String, val path: String, val body: String)

    private lateinit var server: MockWebServer
    private val requests = CopyOnWriteArrayList<Req>()
    private val workScope = CoroutineScope(SupervisorJob() + Dispatchers.IO)

    /** 00:10Z: the 00:00–00:30 airing is on now; the 01:00 one is later. */
    private val clock = Instant.parse("2026-10-04T00:10:00Z")

    private var searchCode = 200
    private var searchBody = """[${result()},${result(start = "2026-10-04T01:00:00Z", stop = "2026-10-04T01:30:00Z", locked = true, rating = "TV-MA")}]"""
    private var createCode = 201
    private var createBody = """{"recording":${recordingJson(id = 9, state = "scheduled")},"warnings":[]}"""

    private fun result(
        start: String = "2026-10-04T00:00:00Z",
        stop: String = "2026-10-04T00:30:00Z",
        locked: Boolean = false,
        rating: String = "TV-PG",
        recording: String? = null,
        watchable: Boolean? = null,
    ) = """
        {"channelId":5,"guideNumber":"5.1","channelName":"KSTP","logoUrl":"/logo.png",
         "start":"$start","stop":"$stop","title":"Jeopardy!","subtitle":"Teen Tournament",
         "description":"","category":"Game","rating":"$rating","locked":$locked
         ${if (recording != null) ""","recording":$recording""" else ""}
         ${if (watchable != null) ""","watchable":$watchable""" else ""}}
    """.trimIndent()

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
                    r.method == "GET" && p.startsWith("/api/v1/guide/search") ->
                        MockResponse().setResponseCode(searchCode)
                            .setBody(if (searchCode == 200) searchBody else """{"error":"q required"}""")
                    r.method == "POST" && p == "/api/v1/recordings" ->
                        MockResponse().setResponseCode(createCode).setBody(createBody)
                    r.method == "POST" && p == "/api/v1/recording-rules" ->
                        MockResponse().setResponseCode(201).setBody("""{"rule":${ruleJson()},"scheduled":1}""")
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

    private suspend fun client(): BowtieClient =
        BowtieClient(server.url("/"), InMemoryTokenStore()).also { it.login("alice", "secret") }

    private suspend fun vm(debounceMs: Long = 0) =
        SearchViewModel(client = client(), now = { clock }, scope = workScope, debounceMs = debounceMs)

    private fun searches() = requests.filter { it.path.startsWith("/api/v1/guide/search") }.map { it.path }

    private fun SearchViewModel.loaded() = state.value.results as SearchViewModel.Results.Loaded

    private fun waitFor(timeoutMs: Long = 5_000, condition: () -> Boolean) {
        val deadline = System.currentTimeMillis() + timeoutMs
        while (!condition()) {
            check(System.currentTimeMillis() < deadline) { "timed out" }
            Thread.sleep(5)
        }
    }

    @Test
    fun client_searchEncodesQuery_andDecodesResults() = runBlocking {
        val c = client()
        val results = c.searchGuide("wheel of fortune & more")

        assertEquals("/api/v1/guide/search?q=wheel%20of%20fortune%20%26%20more&limit=50", searches().single())
        val first = results.first()
        assertEquals(5L, first.channelId)
        assertEquals("5.1", first.guideNumber)
        assertEquals("KSTP", first.channelName)
        assertEquals("Teen Tournament", first.subtitle)
        assertEquals("TV-PG", first.rating)
        assertFalse(first.locked)
        assertTrue(results[1].locked)
        assertNull(first.recording)
    }

    @Test
    fun search_loadsResults_andBlankClears() = runBlocking {
        val m = vm()
        m.search("jeopardy")
        assertEquals(2, m.loaded().items.size)
        assertEquals("jeopardy", m.state.value.query)

        m.onQueryChange("   ")
        assertEquals(SearchViewModel.Results.Idle, m.state.value.results)
    }

    @Test
    fun typing_isDebounced_toOneSearchForTheLastQuery() = runBlocking {
        val m = vm(debounceMs = 150)
        m.onQueryChange("j")
        m.onQueryChange("je")
        m.onQueryChange("jeo")
        waitFor { m.state.value.results is SearchViewModel.Results.Loaded }
        Thread.sleep(200)
        assertEquals(listOf("/api/v1/guide/search?q=jeo&limit=50"), searches())
    }

    @Test
    fun searchFailure_isFailedWithMessage() = runBlocking {
        searchCode = 400
        val m = vm()
        m.search("x")
        assertEquals("q required", (m.state.value.results as SearchViewModel.Results.Failed).message)
    }

    @Test
    fun onNow_andChannelForWatch() = runBlocking {
        val m = vm()
        m.search("jeopardy")
        val (now, later) = m.loaded().items
        assertTrue(m.isOnNow(now))
        assertFalse(m.isOnNow(later))
        assertEquals(Channel(id = 5, guideNumber = "5.1", name = "KSTP", logoUrl = "/logo.png"), m.channelFor(now))
    }

    @Test
    fun canWatch_onlyWhenOnNowAndTheChannelCanStart() = runBlocking {
        // Older servers send no watchable: on-now results stay watchable.
        searchBody = """[${result()},${result(watchable = false)},${result(start = "2026-10-04T01:00:00Z", stop = "2026-10-04T01:30:00Z", watchable = true)}]"""
        val m = vm()
        m.search("jeopardy")
        val (noField, busy, later) = m.loaded().items
        assertTrue(m.canWatch(noField))
        assertFalse("busy tuners: no Watch", m.canWatch(busy))
        assertFalse("not on now: no Watch", m.canWatch(later))
    }

    @Test
    fun record_schedulesAndMarksTheResult() = runBlocking {
        val m = vm()
        m.search("jeopardy")
        val later = m.loaded().items[1]

        val out = m.record(later)

        assertTrue(out is ChannelListViewModel.ScheduleResult.Scheduled)
        assertEquals(
            """{"channelId":5,"programStart":"2026-10-04T01:00:00Z"}""",
            requests.single { it.path == "/api/v1/recordings" }.body,
        )
        assertEquals(GuideRecordingMark(9, "scheduled"), m.loaded().items[1].recording)
        assertNull(m.loaded().items[0].recording)
    }

    @Test
    fun record_409_isConflict() = runBlocking {
        createCode = 409
        createBody = """{"error":"not enough tuners","tunerCount":2,"conflicts":[${recordingJson(state = "scheduled")}]}"""
        val m = vm()
        m.search("jeopardy")
        val out = m.record(m.loaded().items[0])
        assertTrue(out is ChannelListViewModel.ScheduleResult.Conflict)
        assertNull(m.loaded().items[0].recording)
    }

    @Test
    fun recordSeries_postsRule_andRefreshesResults() = runBlocking {
        val m = vm()
        m.search("jeopardy")
        searchBody = "[${result(recording = """{"id":9,"state":"scheduled"}""")}]"

        val out = m.recordSeries(m.loaded().items[0]) as SeriesResult.Scheduled

        assertEquals("Scheduled 1 episode", out.message)
        assertEquals(
            """{"channelId":5,"programStart":"2026-10-04T00:00:00Z","anyChannel":false,"newOnly":true,"keepLatest":0}""",
            requests.single { it.path == "/api/v1/recording-rules" }.body,
        )
        assertEquals(2, searches().size)
        assertEquals(9L, m.loaded().items.single().recording!!.id)
    }
}
