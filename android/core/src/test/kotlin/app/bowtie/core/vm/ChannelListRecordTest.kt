package app.bowtie.core.vm

import app.bowtie.core.BowtieClient
import app.bowtie.core.BowtieClientRecordingsTest.Companion.TOKEN_PAIR
import app.bowtie.core.BowtieClientRecordingsTest.Companion.recordingJson
import app.bowtie.core.GuideRecordingMark
import app.bowtie.core.InMemoryTokenStore
import kotlinx.coroutines.runBlocking
import okhttp3.mockwebserver.Dispatcher
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import okhttp3.mockwebserver.RecordedRequest
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import java.time.Instant
import java.util.concurrent.CopyOnWriteArrayList

/** "Record this program" from the channel list. */
class ChannelListRecordTest {

    private lateinit var server: MockWebServer
    private val posts = CopyOnWriteArrayList<String>()
    private var createCode = 201
    private var channelsBody = """[{"id":5,"guideNumber":"5.1","name":"KSTP","logoUrl":""}]"""
    private var createBody = """{"recording":${recordingJson(id = 9, state = "scheduled")},"warnings":[]}"""

    private val clock = Instant.parse("2026-10-04T00:10:00Z")

    @Before
    fun setUp() {
        server = MockWebServer()
        server.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse {
                val path = request.path.orEmpty()
                val method = request.method.orEmpty()
                return when {
                    method == "POST" && path == "/api/v1/auth/login" -> MockResponse().setBody(TOKEN_PAIR)
                    method == "GET" && path == "/api/v1/channels" ->
                        MockResponse().setBody(channelsBody)
                    method == "GET" && path.startsWith("/api/v1/guide") -> MockResponse().setBody(
                        """[{"channelId":5,"guideNumber":"5.1","name":"KSTP","logoUrl":"","programs":[
                            {"start":"2026-10-04T00:00:00Z","stop":"2026-10-04T00:30:00Z","title":"Jeopardy!","subtitle":"","description":"","category":""},
                            {"start":"2026-10-04T00:30:00Z","stop":"2026-10-04T01:00:00Z","title":"Wheel","subtitle":"","description":"","category":""}
                        ]}]""",
                    )
                    method == "POST" && path == "/api/v1/recordings" -> {
                        posts += request.body.readUtf8()
                        MockResponse().setResponseCode(createCode).setBody(createBody)
                    }
                    else -> MockResponse().setResponseCode(500).setBody("""{"error":"unhandled"}""")
                }
            }
        }
        server.start()
    }

    @After
    fun tearDown() {
        server.shutdown()
    }

    private suspend fun loadedVm(): ChannelListViewModel {
        val client = BowtieClient(server.url("/"), InMemoryTokenStore())
        client.login("alice", "secret")
        val vm = ChannelListViewModel(client = client, now = { clock })
        vm.refresh()
        return vm
    }

    private fun ChannelListViewModel.row() =
        (state.value as ChannelListViewModel.LoadState.Loaded).rows.single()

    @Test
    fun record_keepsFavoritesSupportAndOrder() = runBlocking {
        // 7.1 is a favorite, so it sorts above 5.1 even though the server lists 5.1 first.
        channelsBody = """[
            {"id":5,"guideNumber":"5.1","name":"KSTP","logoUrl":"","favorite":false},
            {"id":7,"guideNumber":"7.1","name":"WCCO","logoUrl":"","favorite":true}
        ]"""
        val vm = loadedVm()
        val before = vm.state.value as ChannelListViewModel.LoadState.Loaded
        assertTrue(before.favoritesSupported)
        assertEquals(listOf(7L, 5L), before.rows.map { it.id })

        val program = before.rows.first { it.id == 5L }.nowNext.now!!
        assertTrue(vm.record(channelId = 5, program = program) is ChannelListViewModel.ScheduleResult.Scheduled)

        val after = vm.state.value as ChannelListViewModel.LoadState.Loaded
        assertTrue("recording must not hide stars / Recent", after.favoritesSupported)
        assertEquals(listOf(7L, 5L), after.rows.map { it.id })
        assertEquals(listOf(7L), after.favorites.map { it.id })
        assertEquals(9L, after.rows.first { it.id == 5L }.nowNext.now!!.recording!!.id)
    }

    @Test
    fun record_schedulesProgram_andMarksTheRow() = runBlocking {
        val vm = loadedVm()
        val program = vm.row().nowNext.now!!
        assertNull(program.recording)

        val result = vm.record(channelId = 5, program = program)

        assertTrue(result is ChannelListViewModel.ScheduleResult.Scheduled)
        assertEquals("""{"channelId":5,"programStart":"2026-10-04T00:00:00Z"}""", posts.single())
        assertEquals(GuideRecordingMark(id = 9, state = "scheduled"), vm.row().nowNext.now!!.recording)
        assertNull(vm.row().nowNext.next!!.recording)
    }

    @Test
    fun record_nextProgram_marksNext() = runBlocking {
        val vm = loadedVm()
        vm.record(channelId = 5, program = vm.row().nowNext.next!!)
        assertEquals(9L, vm.row().nowNext.next!!.recording!!.id)
        assertNull(vm.row().nowNext.now!!.recording)
    }

    @Test
    fun record_warning_isPassedThrough() = runBlocking {
        createBody = """{"recording":${recordingJson(id = 9, state = "scheduled")},"warnings":[{"code":"usesAllTuners","message":"If Plex is using a tuner then, this may not record."}]}"""
        val vm = loadedVm()
        val result = vm.record(5, vm.row().nowNext.now!!) as ChannelListViewModel.ScheduleResult.Scheduled
        assertEquals("If Plex is using a tuner then, this may not record.", result.warning)
    }

    @Test
    fun record_409_isConflict_thenForceSendsForce() = runBlocking {
        createCode = 409
        createBody = """{"error":"not enough tuners","tunerCount":2,"conflicts":[${recordingJson(state = "scheduled")}]}"""
        val vm = loadedVm()
        val program = vm.row().nowNext.now!!

        val result = vm.record(5, program)
        val conflict = result as ChannelListViewModel.ScheduleResult.Conflict
        assertEquals(2, conflict.error.tunerCount)
        assertNull(vm.row().nowNext.now!!.recording)

        createCode = 201
        createBody = """{"recording":${recordingJson(id = 9, state = "scheduled")},"warnings":[]}"""
        val forced = vm.record(5, program, force = true)
        assertTrue(forced is ChannelListViewModel.ScheduleResult.Scheduled)
        assertEquals("""{"channelId":5,"programStart":"2026-10-04T00:00:00Z","force":true}""", posts.last())
    }

    @Test
    fun record_404_isPlainFailure() = runBlocking {
        createCode = 404
        createBody = """{"error":"no such program"}"""
        val vm = loadedVm()
        val result = vm.record(5, vm.row().nowNext.now!!) as ChannelListViewModel.ScheduleResult.Failed
        assertEquals("That program isn't in the guide anymore.", result.message)
    }
}
