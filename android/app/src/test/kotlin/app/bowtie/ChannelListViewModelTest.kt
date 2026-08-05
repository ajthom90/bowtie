package app.bowtie

import app.bowtie.core.BowtieClient
import app.bowtie.core.InMemoryTokenStore
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.test.runTest
import okhttp3.OkHttpClient
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
import java.util.concurrent.atomic.AtomicInteger

/**
 * ChannelListViewModel tests: real [BowtieClient] over MockWebServer.
 * Mirrors iOS ChannelListModelTests (join, empty, failed, guide window, stale math).
 */
@OptIn(ExperimentalCoroutinesApi::class)
class ChannelListViewModelTest {

    private lateinit var server: MockWebServer
    private lateinit var store: InMemoryTokenStore

    private var clock: Instant = Instant.parse("2024-06-15T20:30:00Z")

    private val channelsHits = AtomicInteger(0)
    private val guideHits = AtomicInteger(0)

    private var channelsBody: String = "[]"
    private var guideBody: String = "[]"
    private var channelsCode: Int = 200
    private var guideCode: Int = 200

    @Before
    fun setUp() {
        server = MockWebServer()
        store = InMemoryTokenStore()
        clock = Instant.parse("2024-06-15T20:30:00Z")
        channelsHits.set(0)
        guideHits.set(0)
        channelsBody = "[]"
        guideBody = "[]"
        channelsCode = 200
        guideCode = 200

        server.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse {
                val path = request.path.orEmpty()
                val method = request.method.orEmpty()

                if (method == "POST" && path == "/api/v1/auth/login") {
                    return MockResponse().setBody(tokenPairJson())
                }
                if (method == "GET" && path == "/api/v1/channels") {
                    channelsHits.incrementAndGet()
                    return MockResponse().setResponseCode(channelsCode).setBody(channelsBody)
                }
                if (method == "GET" && path.startsWith("/api/v1/guide")) {
                    guideHits.incrementAndGet()
                    // Capture raw query for window assertions.
                    lastGuidePath = path
                    return MockResponse().setResponseCode(guideCode).setBody(guideBody)
                }
                return MockResponse()
                    .setResponseCode(500)
                    .setBody("""{"error":"unhandled $method $path"}""")
            }
        }
        server.start()
    }

    private var lastGuidePath: String? = null

    @After
    fun tearDown() {
        server.shutdown()
    }

    private fun tokenPairJson() = """
        {
          "accessToken":"access-1",
          "refreshToken":"refresh-1",
          "user":{"id":1,"username":"alice","role":"viewer","maxQuality":"high"}
        }
    """.trimIndent()

    private suspend fun authedClient(): BowtieClient {
        val c = BowtieClient(server.url("/"), store, OkHttpClient())
        c.login("alice", "secret")
        return c
    }

    private fun makeVm(client: BowtieClient): ChannelListViewModel =
        ChannelListViewModel(client = client, now = { clock })

    // ── Join logic ──────────────────────────────────────────────────────────

    @Test
    fun loadJoinsChannelsWithGuideNowNext() = runTest {
        channelsBody = """
            [
              {"id":1,"guideNumber":"4.1","name":"WABC","logoUrl":""},
              {"id":2,"guideNumber":"7.1","name":"WXYZ","logoUrl":""}
            ]
        """.trimIndent()
        guideBody = """
            [
              {
                "channelId":1,
                "guideNumber":"4.1",
                "name":"WABC",
                "logoUrl":"",
                "programs":[
                  {
                    "start":"2024-06-15T20:00:00Z",
                    "stop":"2024-06-15T21:00:00Z",
                    "title":"News",
                    "subtitle":"",
                    "description":"",
                    "category":""
                  },
                  {
                    "start":"2024-06-15T21:00:00Z",
                    "stop":"2024-06-15T22:00:00Z",
                    "title":"Drama",
                    "subtitle":"",
                    "description":"",
                    "category":""
                  }
                ]
              }
            ]
        """.trimIndent()

        val client = authedClient()
        val vm = makeVm(client)
        vm.refresh()

        val state = vm.state.value
        assertTrue("expected Loaded, got $state", state is ChannelListViewModel.LoadState.Loaded)
        val rows = (state as ChannelListViewModel.LoadState.Loaded).rows
        assertEquals(2, rows.size)

        assertEquals(1L, rows[0].channel.id)
        assertEquals("WABC", rows[0].channel.name)
        assertEquals("News", rows[0].nowNext.now?.title)
        assertEquals("Drama", rows[0].nowNext.next?.title)
        assertEquals(1L, rows[0].id)

        // Channel without guide data → empty NowNext
        assertEquals(2L, rows[1].channel.id)
        assertNull(rows[1].nowNext.now)
        assertNull(rows[1].nowNext.next)
    }

    @Test
    fun loadRequestsGuideWindowNowToNowPlus4h() = runTest {
        channelsBody = """
            [{"id":1,"guideNumber":"4.1","name":"WABC","logoUrl":""}]
        """.trimIndent()
        guideBody = "[]"

        val client = authedClient()
        val vm = makeVm(client)
        vm.refresh()

        assertTrue(vm.state.value is ChannelListViewModel.LoadState.Loaded)
        // 20:30 → 00:30 next day
        val path = lastGuidePath.orEmpty()
        assertTrue("path=$path", path.contains("start=2024-06-15T20:30:00Z"))
        assertTrue("path=$path", path.contains("stop=2024-06-16T00:30:00Z"))
        assertEquals(1, guideHits.get())
    }

    // ── Empty / failure ─────────────────────────────────────────────────────

    @Test
    fun loadEmptyChannels() = runTest {
        channelsBody = "[]"
        guideBody = "[]"

        val client = authedClient()
        val vm = makeVm(client)
        vm.refresh()
        assertEquals(ChannelListViewModel.LoadState.Empty, vm.state.value)
    }

    @Test
    fun loadFailure() = runTest {
        channelsCode = 500
        channelsBody = """{"error":"boom"}"""

        val client = authedClient()
        val vm = makeVm(client)
        vm.refresh()

        val state = vm.state.value
        assertTrue("expected Failed, got $state", state is ChannelListViewModel.LoadState.Failed)
        val message = (state as ChannelListViewModel.LoadState.Failed).message
        assertFalse(message.isEmpty())
    }

    @Test
    fun initialStateIsLoading() = runBlocking {
        val client = authedClient()
        val vm = makeVm(client)
        assertEquals(ChannelListViewModel.LoadState.Loading, vm.state.value)
    }

    // ── refreshIfStale window math ──────────────────────────────────────────

    @Test
    fun refreshIfStaleSkipsWhenFresh() = runTest {
        channelsBody = "[]"
        guideBody = "[]"

        val client = authedClient()
        val vm = makeVm(client)

        vm.refresh()
        val afterLoad = channelsHits.get()
        assertTrue(afterLoad > 0)

        // Advance 1 minute — still fresh (5 min window).
        clock = clock.plusSeconds(60)
        vm.refreshIfStale()
        assertEquals("should not re-fetch within 5 minutes", afterLoad, channelsHits.get())
    }

    @Test
    fun refreshIfStaleReloadsAfter5Minutes() = runTest {
        channelsBody = "[]"
        guideBody = "[]"

        val client = authedClient()
        val vm = makeVm(client)

        vm.refresh()
        val afterLoad = channelsHits.get()

        // Exactly 5 minutes later → stale.
        clock = clock.plusSeconds(5 * 60)
        vm.refreshIfStale()
        assertTrue(
            "should re-fetch at 5-minute boundary (hits=$afterLoad → ${channelsHits.get()})",
            channelsHits.get() > afterLoad,
        )
    }

    @Test
    fun refreshIfStaleLoadsWhenNeverLoaded() = runTest {
        channelsBody = "[]"
        guideBody = "[]"

        val client = authedClient()
        val vm = makeVm(client)
        assertEquals(ChannelListViewModel.LoadState.Loading, vm.state.value)

        vm.refreshIfStale()
        assertEquals(ChannelListViewModel.LoadState.Empty, vm.state.value)
    }

    // ── progress helper ─────────────────────────────────────────────────────

    @Test
    fun programProgressMidway() {
        val start = Instant.parse("2024-06-15T20:00:00Z")
        val stop = Instant.parse("2024-06-15T21:00:00Z")
        val program = app.bowtie.core.GuideProgram(
            start = start,
            stop = stop,
            title = "News",
            subtitle = "",
            description = "",
            category = "",
        )
        val mid = Instant.parse("2024-06-15T20:30:00Z")
        assertEquals(0.5f, ChannelListViewModel.programProgress(program, mid), 0.001f)
        assertEquals(0f, ChannelListViewModel.programProgress(null, mid), 0f)
    }
}
