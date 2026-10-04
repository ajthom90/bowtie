package app.bowtie.core.vm

import app.bowtie.core.BowtieClient
import app.bowtie.core.BowtieClientRecordingsTest.Companion.TOKEN_PAIR
import app.bowtie.core.BowtieClientRecordingsTest.Companion.recordingJson
import app.bowtie.core.BowtieClientRulesTest.Companion.ruleJson
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
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import java.time.Instant
import java.util.concurrent.CopyOnWriteArrayList

/** "Record series" from the channel list and the Recordings "Shows" tab. */
class SeriesRulesTest {

    private data class Req(val method: String, val path: String, val body: String)

    private lateinit var server: MockWebServer
    private val requests = CopyOnWriteArrayList<Req>()
    private val workScope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    private val clock = Instant.parse("2026-10-04T00:10:00Z")

    private var rulesBody = "[${ruleJson(id = 4)},${ruleJson(id = 5, canManage = false)}]"
    private var createRuleCode = 201
    private var createRuleBody = """{"rule":${ruleJson(id = 4)},"scheduled":6}"""
    private var deleteRuleCode = 204

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
                    r.method == "GET" && p == "/api/v1/channels" ->
                        MockResponse().setBody("""[{"id":5,"guideNumber":"5.1","name":"KSTP","logoUrl":""}]""")
                    r.method == "GET" && p.startsWith("/api/v1/guide") -> MockResponse().setBody(
                        """[{"channelId":5,"guideNumber":"5.1","name":"KSTP","logoUrl":"","programs":[
                            {"start":"2026-10-04T00:00:00Z","stop":"2026-10-04T00:30:00Z","title":"Jeopardy!","subtitle":"","description":"","category":""}
                        ]}]""",
                    )
                    r.method == "GET" && p == "/api/v1/recording-rules" -> MockResponse().setBody(rulesBody)
                    r.method == "POST" && p == "/api/v1/recording-rules" ->
                        MockResponse().setResponseCode(createRuleCode).setBody(createRuleBody)
                    r.method == "DELETE" && p.startsWith("/api/v1/recording-rules/") ->
                        MockResponse().setResponseCode(deleteRuleCode)
                            .setBody(if (deleteRuleCode == 204) "" else """{"error":"forbidden"}""")
                    r.method == "GET" && p.startsWith("/api/v1/recordings?state=") ->
                        MockResponse().setBody("[${recordingJson(id = 1, state = "scheduled")}]")
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

    private fun paths(method: String) = requests.filter { it.method == method }.map { it.path }

    // ── Channel list: "Record series" ───────────────────────────────────────

    @Test
    fun recordSeries_postsRule_andReportsScheduledCount() = runBlocking {
        val vm = ChannelListViewModel(client = client(), now = { clock })
        vm.refresh()
        val program = (vm.state.value as ChannelListViewModel.LoadState.Loaded).rows.single().nowNext.now!!
        val guideLoads = paths("GET").count { it.startsWith("/api/v1/guide") }

        val result = vm.recordSeries(channelId = 5, program = program) as SeriesResult.Scheduled

        assertEquals(6, result.scheduled)
        assertEquals("Scheduled 6 episodes", result.message)
        assertEquals(
            """{"channelId":5,"programStart":"2026-10-04T00:00:00Z","anyChannel":false,"newOnly":true,"keepLatest":0}""",
            requests.single { it.method == "POST" && it.path == "/api/v1/recording-rules" }.body,
        )
        // The guide reloads quietly so the new episodes show as set to record.
        assertEquals(guideLoads + 1, paths("GET").count { it.startsWith("/api/v1/guide") })
        assertTrue(vm.state.value is ChannelListViewModel.LoadState.Loaded)
    }

    @Test
    fun recordSeries_failure_isPlainWords() = runBlocking {
        createRuleCode = 404
        createRuleBody = """{"error":"no such program"}"""
        val vm = ChannelListViewModel(client = client(), now = { clock })
        vm.refresh()
        val program = (vm.state.value as ChannelListViewModel.LoadState.Loaded).rows.single().nowNext.now!!

        val result = vm.recordSeries(5, program) as SeriesResult.Failed
        assertEquals("That program isn't in the guide anymore.", result.message)
    }

    // ── Recordings: "Shows" tab ─────────────────────────────────────────────

    @Test
    fun showsTab_listsRules_withoutCallingRecordings() = runBlocking {
        val vm = RecordingsViewModel(client = client(), scope = workScope)
        vm.selectTab(Tab.Shows)

        assertEquals(Tab.Shows, vm.state.value.tab)
        val load = vm.state.value.load as RecordingsViewModel.Load.Shows
        assertEquals(listOf(4L, 5L), load.rules.map { it.id })
        assertTrue(load.rules[0].canManage)
        assertFalse(load.rules[1].canManage)
        assertFalse(paths("GET").any { it.startsWith("/api/v1/recordings") })

        // Refresh on the Shows tab reloads the rules.
        rulesBody = "[]"
        vm.refresh()
        assertEquals(emptyList<Long>(), (vm.state.value.load as RecordingsViewModel.Load.Shows).rules.map { it.id })
    }

    @Test
    fun stopShow_deletesRule_thenReloads() = runBlocking {
        val vm = RecordingsViewModel(client = client(), scope = workScope)
        vm.selectTab(Tab.Shows)
        val rule = (vm.state.value.load as RecordingsViewModel.Load.Shows).rules.first()
        rulesBody = "[${ruleJson(id = 5, canManage = false)}]"

        assertTrue(vm.stopShow(rule))

        assertTrue("/api/v1/recording-rules/4" in paths("DELETE"))
        assertEquals(listOf(5L), (vm.state.value.load as RecordingsViewModel.Load.Shows).rules.map { it.id })
        assertNull(vm.state.value.message)
    }

    @Test
    fun stopShow_forbidden_setsMessage() = runBlocking {
        deleteRuleCode = 403
        val vm = RecordingsViewModel(client = client(), scope = workScope)
        vm.selectTab(Tab.Shows)
        val rule = (vm.state.value.load as RecordingsViewModel.Load.Shows).rules.first()

        assertFalse(vm.stopShow(rule))
        assertEquals("Only the person who scheduled it or an admin can change it.", vm.state.value.message)
    }

    @Test
    fun recordingTabs_stillLoadRecordings() = runBlocking {
        val vm = RecordingsViewModel(client = client(), scope = workScope)
        vm.selectTab(Tab.Shows)
        vm.selectTab(Tab.Upcoming)
        assertTrue(vm.state.value.load is RecordingsViewModel.Load.Loaded)
        assertEquals("/api/v1/recordings?state=upcoming", paths("GET").last())
    }
}
