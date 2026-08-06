package app.bowtie.core.vm

import app.bowtie.core.BowtieClient
import app.bowtie.core.Channel
import app.bowtie.core.ClientCaps
import app.bowtie.core.InMemoryTokenStore
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.test.setMain
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
import java.util.concurrent.CopyOnWriteArrayList
import java.util.concurrent.atomic.AtomicInteger

/**
 * PlayerViewModel tests: real [BowtieClient] over MockWebServer.
 *
 * Debounce uses virtual time (`advanceTimeBy`) — never wall-clock sleeps for
 * the 400ms window. After virtual debounce, a short pump loop drains
 * `Dispatchers.IO` completions from OkHttp/MockWebServer back onto the test
 * scheduler (advanceUntilIdle alone returns while IO is still in flight).
 */
@OptIn(ExperimentalCoroutinesApi::class)
class PlayerViewModelTest {

    private lateinit var server: MockWebServer
    private lateinit var store: InMemoryTokenStore

    private val sampleCaps = ClientCaps(
        videoCodecs = listOf("h264", "hevc"),
        audioCodecs = listOf("aac", "ac3", "eac3"),
        maxHeight = 1080,
        profile = "",
    )

    private val ch1 = Channel(id = 1, guideNumber = "1", name = "One", logoUrl = "")
    private val ch2 = Channel(id = 2, guideNumber = "2", name = "Two", logoUrl = "")
    private val ch3 = Channel(id = 3, guideNumber = "3", name = "Three", logoUrl = "")

    private val recorded = CopyOnWriteArrayList<BodyRequest>()
    private val createSeq = AtomicInteger(0)

    /** Scripted session POST status codes (empty → always 200). */
    private var createStatusCodes: List<Int> = emptyList()
    private var createBodies: List<String> = emptyList()

    @Before
    fun setUp() {
        Dispatchers.setMain(StandardTestDispatcher())
        server = MockWebServer()
        store = InMemoryTokenStore()
        recorded.clear()
        createSeq.set(0)
        createStatusCodes = emptyList()
        createBodies = emptyList()

        server.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse {
                // Snapshot body now — RecordedRequest body can only be read once.
                val bodyUtf8 = try {
                    request.body.readUtf8()
                } catch (_: Exception) {
                    ""
                }
                recorded.add(BodyRequest(request, bodyUtf8))
                val path = request.path.orEmpty()
                val method = request.method.orEmpty()

                if (method == "POST" && path == "/api/v1/auth/login") {
                    return MockResponse().setBody(tokenPairJson())
                }
                if (method == "POST" && path == "/api/v1/sessions") {
                    val n = createSeq.incrementAndGet()
                    val code = createStatusCodes.getOrElse(n - 1) { 200 }
                    if (code != 200) {
                        val errBody = when (code) {
                            422 -> """{"error":"profile not available"}"""
                            404 -> """{"error":"channel not found"}"""
                            503 -> """{"error":"all tuners in use","sessions":[]}"""
                            else -> """{"error":"fail"}"""
                        }
                        return MockResponse().setResponseCode(code).setBody(errBody)
                    }
                    val body = createBodies.getOrElse(n - 1) {
                        createdSessionJson(viewerId = "viewer-$n")
                    }
                    return MockResponse().setBody(body)
                }
                if (method == "DELETE" && path.startsWith("/api/v1/sessions/")) {
                    return MockResponse().setResponseCode(204)
                }
                return MockResponse().setResponseCode(500).setBody("""{"error":"unhandled $method $path"}""")
            }
        }
        server.start()
    }

    @After
    fun tearDown() {
        server.shutdown()
        Dispatchers.resetMain()
    }

    /** Captures method/path/body because MockWebServer bodies are one-shot. */
    private data class BodyRequest(
        val method: String?,
        val path: String?,
        val body: String,
    ) {
        constructor(req: RecordedRequest, body: String) : this(req.method, req.path, body)
    }

    private fun allRecorded(): List<BodyRequest> = recorded.toList()

    private fun tokenPairJson(
        access: String = "access-1",
        refresh: String = "refresh-1",
    ) = """
        {
          "accessToken":"$access",
          "refreshToken":"$refresh",
          "user":{"id":1,"username":"alice","role":"viewer","maxQuality":"high"}
        }
    """.trimIndent()

    private fun createdSessionJson(viewerId: String) =
        """{"viewerId":"$viewerId","playlistUrl":"/api/v1/stream/$viewerId/index.m3u8?token=t"}"""

    private suspend fun authedClient(): BowtieClient {
        val c = BowtieClient(server.url("/"), store, OkHttpClient())
        c.login("alice", "secret")
        return c
    }

    private fun sessionCreates(): List<BodyRequest> =
        allRecorded().filter { it.method == "POST" && it.path == "/api/v1/sessions" }

    private fun sessionDeletes(): List<BodyRequest> =
        allRecorded().filter {
            it.method == "DELETE" && it.path.orEmpty().startsWith("/api/v1/sessions/")
        }

    private fun makeVm(
        client: BowtieClient,
        scope: kotlinx.coroutines.CoroutineScope,
        debounceMs: Long = 400,
    ): PlayerViewModel = PlayerViewModel(
        client = client,
        caps = sampleCaps,
        debounceMs = debounceMs,
        scope = scope,
    )

    /**
     * Advance virtual debounce, then pump until [condition] (or timeout).
     * Pumping is required because [BowtieClient] uses [Dispatchers.IO];
     * [advanceUntilIdle] returns while real IO is still in flight.
     */
    private fun TestScope.advanceDebounceAndPump(
        debounceMs: Long = 400,
        timeoutMs: Long = 5_000,
        condition: () -> Boolean = { true },
    ) {
        advanceTimeBy(debounceMs)
        val deadline = System.nanoTime() + timeoutMs * 1_000_000L
        while (!condition()) {
            advanceUntilIdle()
            if (condition()) break
            if (System.nanoTime() > deadline) {
                error("timeout waiting for condition after debounce")
            }
            // Brief park so Dispatchers.IO / MockWebServer can finish (not debounce).
            Thread.sleep(2)
        }
        advanceUntilIdle()
    }

    private fun TestScope.playThroughDebounce(
        vm: PlayerViewModel,
        channel: Channel = ch1,
        debounceMs: Long = 400,
    ) {
        vm.play(channel)
        advanceDebounceAndPump(debounceMs) {
            vm.state.value !is PlayerViewModel.State.Starting
        }
    }

    private fun TestScope.installMain() {
        Dispatchers.setMain(StandardTestDispatcher(testScheduler))
    }

    // ── Debounced triple-play → one create ──────────────────────────────────

    @Test
    fun rapidTriplePlayCreatesExactlyOneSession() = runTest {
        installMain()
        val client = authedClient()
        val vm = makeVm(client, this)

        vm.play(ch1)
        vm.play(ch2)
        vm.play(ch3)

        advanceDebounceAndPump {
            vm.state.value is PlayerViewModel.State.Playing
        }

        assertEquals("exactly one create after triple-play debounce", 1, sessionCreates().count())
        assertEquals(3L, vm.currentChannel.value?.id)
        val state = vm.state.value
        assertTrue("expected Playing, got $state", state is PlayerViewModel.State.Playing)
        assertEquals("viewer-1", (state as PlayerViewModel.State.Playing).s.viewerId)

        val body = sessionCreates().single().body
        assertTrue("body=$body", body.contains("\"channelId\":3"))
    }

    // ── Zap: delete old then create new ─────────────────────────────────────

    @Test
    fun zapDeletesOldThenCreatesNew() = runTest {
        installMain()
        val client = authedClient()
        val vm = makeVm(client, this)

        playThroughDebounce(vm, ch1)
        val first = vm.state.value
        assertTrue("expected Playing, got $first", first is PlayerViewModel.State.Playing)
        assertEquals("viewer-1", (first as PlayerViewModel.State.Playing).s.viewerId)
        assertEquals(1, sessionCreates().count())
        assertEquals(0, sessionDeletes().count())

        vm.play(ch2)
        advanceDebounceAndPump {
            val s = vm.state.value
            s is PlayerViewModel.State.Playing &&
                (s as PlayerViewModel.State.Playing).s.viewerId == "viewer-2"
        }

        val second = vm.state.value
        assertTrue("expected Playing, got $second", second is PlayerViewModel.State.Playing)
        assertEquals("viewer-2", (second as PlayerViewModel.State.Playing).s.viewerId)
        assertEquals(2, sessionCreates().count())

        val deletes = sessionDeletes()
        assertEquals(1, deletes.count())
        assertTrue(
            "zap must DELETE the old viewer: ${deletes[0].path}",
            deletes[0].path!!.endsWith("/viewer-1"),
        )

        val snapshot = allRecorded()
        val deleteIdx = snapshot.indexOfFirst {
            it.method == "DELETE" && it.path.orEmpty().contains("viewer-1")
        }
        val createIndices = snapshot.indices.filter {
            snapshot[it].method == "POST" && snapshot[it].path == "/api/v1/sessions"
        }
        assertEquals(2, createIndices.size)
        assertTrue("DELETE old before POST new", deleteIdx < createIndices[1])
    }

    // ── setProfile sends effectiveCaps ──────────────────────────────────────

    @Test
    fun setProfileSendsEffectiveCapsInRequestBody() = runTest {
        installMain()
        val client = authedClient()
        val vm = makeVm(client, this)

        playThroughDebounce(vm, ch1)
        assertEquals(1, sessionCreates().count())

        vm.setProfile("medium")
        advanceDebounceAndPump {
            sessionCreates().count() >= 2 &&
                vm.state.value is PlayerViewModel.State.Playing
        }

        assertEquals(ch1.id, vm.currentChannel.value?.id)
        assertEquals("medium", vm.selectedProfile)

        val creates = sessionCreates()
        assertEquals(2, creates.count())
        val body = creates[1].body
        assertTrue("body=$body", body.contains("\"channelId\":1"))
        assertTrue("body=$body", body.contains("\"profile\":\"medium\""))
        assertTrue("body=$body", body.contains("\"maxHeight\":1080"))
        assertTrue("body=$body", body.contains("\"h264\""))
        assertTrue("body=$body", body.contains("\"hevc\""))
    }

    // ── 422 fallback ────────────────────────────────────────────────────────

    @Test
    fun negotiation422RetriesOnceWithAutoProfile() = runTest {
        installMain()
        createStatusCodes = listOf(422, 200)
        createBodies = listOf(
            "",
            createdSessionJson("auto-ok"),
        )
        val client = authedClient()
        val vm = makeVm(client, this)
        vm.selectedProfile = "high"

        vm.play(ch1)
        advanceDebounceAndPump {
            vm.state.value is PlayerViewModel.State.Playing
        }

        val creates = sessionCreates()
        assertEquals(2, creates.count())
        val first = creates[0].body
        val second = creates[1].body
        assertTrue("first=$first", first.contains("\"profile\":\"high\""))
        assertTrue("second=$second", second.contains("\"profile\":\"\""))
        assertEquals("", vm.selectedProfile)

        val state = vm.state.value
        assertTrue("expected Playing, got $state", state is PlayerViewModel.State.Playing)
        assertEquals("auto-ok", (state as PlayerViewModel.State.Playing).s.viewerId)
    }

    @Test
    fun negotiation422TwiceFailsWithDeviceCantPlay() = runTest {
        installMain()
        createStatusCodes = listOf(422, 422)
        val client = authedClient()
        val vm = makeVm(client, this)
        vm.selectedProfile = "original"

        vm.play(ch1)
        advanceDebounceAndPump {
            vm.state.value is PlayerViewModel.State.Failed
        }

        val creates = sessionCreates()
        assertEquals(2, creates.count())
        val first = creates[0].body
        val second = creates[1].body
        assertTrue("first=$first", first.contains("\"profile\":\"original\""))
        assertTrue("second=$second", second.contains("\"profile\":\"\""))

        val state = vm.state.value
        assertTrue("expected Failed, got $state", state is PlayerViewModel.State.Failed)
        assertEquals(
            PlayerViewModel.DEVICE_CANT_PLAY_MESSAGE,
            (state as PlayerViewModel.State.Failed).message,
        )
        assertEquals("", vm.selectedProfile)
    }

    // ── stop ────────────────────────────────────────────────────────────────

    @Test
    fun stopDeletesSessionAndReturnsToIdle() = runTest {
        installMain()
        createBodies = listOf(createdSessionJson("to-stop"))
        val client = authedClient()
        val vm = makeVm(client, this)

        playThroughDebounce(vm, ch1)
        assertTrue(vm.state.value is PlayerViewModel.State.Playing)

        vm.stop()
        advanceDebounceAndPump(debounceMs = 0) {
            sessionDeletes().isNotEmpty() && vm.state.value is PlayerViewModel.State.Idle
        }

        assertEquals(PlayerViewModel.State.Idle, vm.state.value)
        assertNull(vm.currentChannel.value)
        val deletes = sessionDeletes()
        assertEquals(1, deletes.count())
        assertTrue(deletes[0].path!!.endsWith("/to-stop"))
    }

    // ── onPlaybackAuthError ─────────────────────────────────────────────────

    @Test
    fun authErrorOnceSilentlyReplaces() = runTest {
        installMain()
        val client = authedClient()
        val vm = makeVm(client, this)

        playThroughDebounce(vm, ch1)
        val first = vm.state.value
        assertTrue(first is PlayerViewModel.State.Playing)
        assertEquals("viewer-1", (first as PlayerViewModel.State.Playing).s.viewerId)

        vm.onPlaybackAuthError()
        advanceDebounceAndPump {
            val s = vm.state.value
            s is PlayerViewModel.State.Playing &&
                (s as PlayerViewModel.State.Playing).s.viewerId == "viewer-2"
        }

        val second = vm.state.value
        assertTrue("expected Playing after silent replace, got $second", second is PlayerViewModel.State.Playing)
        assertEquals("viewer-2", (second as PlayerViewModel.State.Playing).s.viewerId)
        assertEquals(2, sessionCreates().count())
        assertEquals(1, sessionDeletes().count())
    }

    @Test
    fun authErrorTwiceGoesToFailed() = runTest {
        installMain()
        val client = authedClient()
        val vm = makeVm(client, this)

        playThroughDebounce(vm, ch1)

        vm.onPlaybackAuthError()
        advanceDebounceAndPump {
            vm.state.value is PlayerViewModel.State.Playing &&
                sessionCreates().count() >= 2
        }
        assertTrue(
            "expected Playing after first auth failure, got ${vm.state.value}",
            vm.state.value is PlayerViewModel.State.Playing,
        )

        vm.onPlaybackAuthError()
        advanceUntilIdle()

        val state = vm.state.value
        assertTrue("expected Failed, got $state", state is PlayerViewModel.State.Failed)
        assertEquals(
            PlayerViewModel.PLAYBACK_AUTH_FAILED_MESSAGE,
            (state as PlayerViewModel.State.Failed).message,
        )
        assertEquals(2, sessionCreates().count())
    }

    // ── 404 → Failed + channelsStale ────────────────────────────────────────

    @Test
    fun create404FailsAndSignalsChannelsStale() = runTest {
        installMain()
        createStatusCodes = listOf(404)
        val client = authedClient()
        val vm = makeVm(client, this)

        assertFalse(vm.channelsStale.value)

        vm.play(ch1)
        advanceDebounceAndPump {
            vm.state.value is PlayerViewModel.State.Failed
        }

        val state = vm.state.value
        assertTrue("expected Failed, got $state", state is PlayerViewModel.State.Failed)
        assertEquals(
            PlayerViewModel.CHANNEL_NOT_FOUND_MESSAGE,
            (state as PlayerViewModel.State.Failed).message,
        )
        assertTrue("channelsStale should be true after 404", vm.channelsStale.value)
        assertEquals(1, sessionCreates().count())
    }
}
