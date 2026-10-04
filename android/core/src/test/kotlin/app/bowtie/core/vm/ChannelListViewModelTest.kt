package app.bowtie.core.vm

import app.bowtie.core.BowtieClient
import app.bowtie.core.InMemoryTokenStore
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
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
import java.util.concurrent.CopyOnWriteArrayList
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

    private val recentsHits = AtomicInteger(0)
    private var recentsBody: String = "[]"
    private var recentsCode: Int = 200
    private var favoriteCode: Int = 204
    private val favoriteRequests = CopyOnWriteArrayList<String>()

    /** Runs on the MockWebServer thread when a favorite PUT/DELETE arrives. */
    @Volatile
    private var onFavoriteRequest: (() -> Unit)? = null

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
        recentsHits.set(0)
        recentsBody = "[]"
        recentsCode = 200
        favoriteCode = 204
        favoriteRequests.clear()
        onFavoriteRequest = null

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
                if (method == "GET" && path.startsWith("/api/v1/me/recents")) {
                    recentsHits.incrementAndGet()
                    lastRecentsPath = path
                    return MockResponse().setResponseCode(recentsCode).setBody(recentsBody)
                }
                if ((method == "PUT" || method == "DELETE") && path.startsWith("/api/v1/me/favorites/")) {
                    favoriteRequests.add("$method $path")
                    onFavoriteRequest?.invoke()
                    val body = if (favoriteCode == 204) "" else "{\"error\":\"boom\"}"
                    return MockResponse().setResponseCode(favoriteCode).setBody(body)
                }
                return MockResponse()
                    .setResponseCode(500)
                    .setBody("""{"error":"unhandled $method $path"}""")
            }
        }
        server.start()
    }

    private var lastGuidePath: String? = null
    private var lastRecentsPath: String? = null

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

        // Sleep timer "End of this program": when the now program stops.
        assertEquals(
            Instant.parse("2024-06-15T21:00:00Z").toEpochMilli(),
            vm.programEndMs(rows[0].channel),
        )
        assertNull(vm.programEndMs(rows[1].channel))
    }

    @Test
    fun rowsKeepProgramsAndCategoryFilterIsRemembered() = runTest {
        channelsBody = """
            [
              {"id":1,"guideNumber":"4.1","name":"WABC","logoUrl":""},
              {"id":2,"guideNumber":"7.1","name":"WXYZ","logoUrl":""}
            ]
        """.trimIndent()
        guideBody = """
            [
              {
                "channelId":1, "guideNumber":"4.1", "name":"WABC", "logoUrl":"",
                "programs":[
                  {"start":"2024-06-15T20:00:00Z","stop":"2024-06-15T21:00:00Z","title":"News",
                   "subtitle":"","description":"","category":"News"},
                  {"start":"2024-06-15T22:00:00Z","stop":"2024-06-15T23:30:00Z","title":"Game",
                   "subtitle":"","description":"","category":"Sports event"}
                ]
              },
              {
                "channelId":2, "guideNumber":"7.1", "name":"WXYZ", "logoUrl":"",
                "programs":[
                  {"start":"2024-06-15T20:00:00Z","stop":"2024-06-15T22:00:00Z","title":"Movie",
                   "subtitle":"","description":"","category":"Movie"}
                ]
              }
            ]
        """.trimIndent()
        val prefs = app.bowtie.core.GuideFilterPrefs.InMemory(app.bowtie.core.GuideFilter.SPORTS)
        val vm = ChannelListViewModel(client = authedClient(), now = { clock }, filterPrefs = prefs)
        assertEquals(app.bowtie.core.GuideFilter.SPORTS, vm.filter.value)
        vm.refresh()

        val rows = (vm.state.value as ChannelListViewModel.LoadState.Loaded).rows
        assertEquals(listOf("News", "Game"), rows[0].programs.map { it.title })
        assertEquals(listOf(1L), vm.visibleRows(rows, vm.filter.value, clock).map { it.id })
        // Now (News) is dimmed; next (Game) matches, so no later line.
        val h = vm.highlight(rows[0], app.bowtie.core.GuideFilter.SPORTS, clock)
        assertEquals(false, h.nowMatches)
        assertEquals(true, h.nextMatches)

        vm.setFilter(app.bowtie.core.GuideFilter.MOVIES)
        assertEquals(app.bowtie.core.GuideFilter.MOVIES, prefs.filter)
        assertEquals(listOf(2L), vm.visibleRows(rows, vm.filter.value, clock).map { it.id })
        assertEquals(emptyList<Long>(), vm.visibleRows(rows, app.bowtie.core.GuideFilter.KIDS, clock).map { it.id })
        assertEquals(listOf(1L, 2L), vm.visibleRows(rows, app.bowtie.core.GuideFilter.ALL, clock).map { it.id })
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

    // ── Favorites / Recents ─────────────────────────────────────────────────

    /** A /channels item; [favorite] null omits the field like a pre-favorites server. */
    private fun ch(id: Int, number: String, favorite: Boolean?): String {
        val fav = if (favorite == null) "" else ",\"favorite\":$favorite"
        return "{\"id\":$id,\"guideNumber\":\"$number\",\"name\":\"C$id\",\"logoUrl\":\"\"$fav}"
    }

    private fun channelsJson(vararg items: String) = items.joinToString(",", "[", "]")

    private fun recent(id: Int, number: String, name: String, at: String) =
        "{\"channelId\":$id,\"guideNumber\":\"$number\",\"name\":\"$name\",\"logoUrl\":\"\",\"watchedAt\":\"$at\"}"

    private fun loaded(vm: ChannelListViewModel): ChannelListViewModel.LoadState.Loaded {
        val state = vm.state.value
        assertTrue("expected Loaded, got $state", state is ChannelListViewModel.LoadState.Loaded)
        return state as ChannelListViewModel.LoadState.Loaded
    }

    @Test
    fun favoritesSortFirstInGuideNumberOrderThenRestInServerOrder() = runTest {
        channelsBody = channelsJson(
            ch(1, "2.1", false),
            ch(2, "4.1", true),
            ch(3, "11.1", true),
            ch(4, "9.1", true),
            ch(5, "5.1", false),
            ch(6, "3.1", false),
        )

        val vm = makeVm(authedClient())
        vm.refresh()

        val l = loaded(vm)
        assertTrue(l.favoritesSupported)
        // This order is also the TV zap order (TvNav builds the zap list from rows).
        assertEquals(
            listOf("4.1", "9.1", "11.1", "2.1", "5.1", "3.1"),
            l.rows.map { it.channel.guideNumber },
        )
        assertEquals(listOf(2L, 4L, 3L), l.favorites.map { it.id })
        assertEquals(listOf(1L, 5L, 6L), l.others.map { it.id })
        assertTrue(l.rows[0].isFavorite)
        assertFalse(l.rows[3].isFavorite)
    }

    @Test
    fun guideNumberOrderIsNumeric() {
        val sorted = listOf("11.1", "9.1", "4.10", "4.2", "7", "7.1", "4.1")
            .sortedWith(ChannelListViewModel.GuideNumberOrder)
        assertEquals(listOf("4.1", "4.2", "4.10", "7", "7.1", "9.1", "11.1"), sorted)
    }

    @Test
    fun olderServerWithoutFavoriteFieldHidesFavoritesAndSkipsRecents() = runTest {
        channelsBody = channelsJson(ch(1, "4.1", null), ch(2, "2.1", null))
        recentsBody = "[" + recent(1, "4.1", "C1", "2026-10-03T19:42:10Z") + "]"

        val vm = makeVm(authedClient())
        vm.refresh()

        val l = loaded(vm)
        assertFalse(l.favoritesSupported)
        assertEquals("server order kept", listOf(1L, 2L), l.rows.map { it.id })
        assertEquals(0, recentsHits.get())
        assertTrue(vm.recents.value.isEmpty())
    }

    @Test
    fun recentsLoadedOnRefresh() = runTest {
        channelsBody = channelsJson(ch(1, "4.1", false), ch(7, "9.1", true))
        recentsBody = "[" + recent(7, "9.1", "C7", "2026-10-03T19:42:10Z") + "," +
            recent(1, "4.1", "C1", "2026-10-03T18:00:00Z") + "]"

        val vm = makeVm(authedClient())
        vm.refresh()

        assertEquals(1, recentsHits.get())
        assertEquals("/api/v1/me/recents?limit=8", lastRecentsPath)
        assertEquals(listOf(7L, 1L), vm.recents.value.map { it.channelId })
    }

    @Test
    fun recents404MeansUnsupportedAndLoadStillSucceeds() = runTest {
        channelsBody = channelsJson(ch(1, "4.1", false))
        recentsCode = 404
        recentsBody = "404 page not found"

        val vm = makeVm(authedClient())
        vm.refresh()

        loaded(vm)
        assertTrue(vm.recents.value.isEmpty())
    }

    @Test
    fun recentsServerErrorDoesNotFailLoad() = runTest {
        channelsBody = channelsJson(ch(1, "4.1", false))
        recentsCode = 500
        recentsBody = "{\"error\":\"boom\"}"

        val vm = makeVm(authedClient())
        vm.refresh()

        loaded(vm)
        assertTrue(vm.recents.value.isEmpty())
    }

    @Test
    fun refreshRecentsUpdatesRowWithoutReloadingChannels() = runTest {
        channelsBody = channelsJson(ch(1, "4.1", false))
        val vm = makeVm(authedClient())
        vm.refresh()
        assertTrue(vm.recents.value.isEmpty())
        val channelLoads = channelsHits.get()

        recentsBody = "[" + recent(1, "4.1", "C1", "2026-10-03T19:42:10Z") + "]"
        vm.refreshRecents()

        assertEquals(listOf(1L), vm.recents.value.map { it.channelId })
        assertEquals(channelLoads, channelsHits.get())
        loaded(vm)
    }

    @Test
    fun channelForRecentPrefersTheListedChannel() = runTest {
        channelsBody = channelsJson(ch(7, "9.1", true))
        recentsBody = "[" + recent(7, "9.1", "C7", "2026-10-03T19:42:10Z") + "," +
            recent(8, "9.2", "Gone", "2026-10-03T18:00:00Z") + "]"
        val vm = makeVm(authedClient())
        vm.refresh()

        val listed = vm.channelFor(vm.recents.value[0])
        assertEquals(true, listed.favorite)
        val built = vm.channelFor(vm.recents.value[1])
        assertEquals(8L, built.id)
        assertEquals("9.2", built.guideNumber)
        assertEquals("Gone", built.name)
    }

    @Test
    fun setFavoriteIsOptimisticAndResorts() = runTest {
        channelsBody = channelsJson(ch(1, "2.1", false), ch(2, "4.1", false), ch(3, "9.1", false))
        val vm = makeVm(authedClient())
        vm.refresh()

        var duringRequest: List<Long>? = null
        onFavoriteRequest = { duringRequest = loaded(vm).rows.map { it.id } }
        vm.setFavorite(3, true)

        assertEquals(listOf("PUT /api/v1/me/favorites/3"), favoriteRequests.toList())
        assertEquals("flipped before the server answered", listOf(3L, 1L, 2L), duringRequest)
        val l = loaded(vm)
        assertEquals(listOf(3L, 1L, 2L), l.rows.map { it.id })
        assertTrue(l.rows[0].isFavorite)
        assertNull(vm.message.value)
    }

    @Test
    fun unsetFavoriteSendsDeleteAndReturnsChannelToItsPlace() = runTest {
        channelsBody = channelsJson(ch(1, "2.1", false), ch(2, "4.1", true), ch(3, "9.1", false))
        val vm = makeVm(authedClient())
        vm.refresh()
        assertEquals(listOf(2L, 1L, 3L), loaded(vm).rows.map { it.id })

        vm.setFavorite(2, false)

        assertEquals(listOf("DELETE /api/v1/me/favorites/2"), favoriteRequests.toList())
        assertEquals(listOf(1L, 2L, 3L), loaded(vm).rows.map { it.id })
        assertTrue(loaded(vm).favorites.isEmpty())
    }

    @Test
    fun setFavoriteRevertsAndReportsOnError() = runTest {
        channelsBody = channelsJson(ch(1, "2.1", false), ch(2, "4.1", false))
        favoriteCode = 500
        val vm = makeVm(authedClient())
        vm.refresh()

        var duringRequest: Boolean? = null
        onFavoriteRequest = {
            duringRequest = loaded(vm).rows.first { it.id == 2L }.isFavorite
        }
        vm.setFavorite(2, true)

        assertEquals("optimistic while in flight", true, duringRequest)
        val l = loaded(vm)
        assertEquals("reverted", listOf(1L, 2L), l.rows.map { it.id })
        assertFalse(l.rows.any { it.isFavorite })
        val message = vm.message.value
        assertTrue("message=$message", !message.isNullOrEmpty())

        vm.consumeMessage()
        assertNull(vm.message.value)
    }

    @Test
    fun toggleFavoriteFlipsCurrentValueInViewModelScope() = runBlocking {
        channelsBody = channelsJson(ch(1, "2.1", false), ch(2, "4.1", true))
        val vm = ChannelListViewModel(
            client = authedClient(),
            now = { clock },
            scope = CoroutineScope(Dispatchers.Unconfined),
        )
        vm.refresh()

        vm.toggleFavorite(1).join()
        vm.toggleFavorite(2).join()

        assertEquals(
            listOf("PUT /api/v1/me/favorites/1", "DELETE /api/v1/me/favorites/2"),
            favoriteRequests.toList(),
        )
        assertEquals(listOf(1L), loaded(vm).favorites.map { it.id })
        assertEquals(listOf(2L), loaded(vm).others.map { it.id })
    }
}
