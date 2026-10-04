package app.bowtie.core.vm

import app.bowtie.core.BowtieClient
import app.bowtie.core.BowtieClientRecordingsTest.Companion.TOKEN_PAIR
import app.bowtie.core.InMemoryTokenStore
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
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
import java.util.concurrent.CopyOnWriteArrayList
import java.util.concurrent.TimeUnit

/** ContinueWatchingViewModel over a real [BowtieClient] and MockWebServer. */
class ContinueWatchingViewModelTest {

    private data class Req(val method: String, val path: String, val body: String)

    private lateinit var server: MockWebServer
    private val requests = CopyOnWriteArrayList<Req>()
    @Volatile private var listCode = 200
    @Volatile private var putCode = 204
    @Volatile private var list = "[]"
    @Volatile private var listDelayMs = 0L

    private fun rec(id: Long, positionSec: Int, state: String = "ready", updated: String? = null) = """
        {"id":$id,"title":"Show $id","channelId":5,
         "start":"2026-10-04T00:00:00Z","stop":"2026-10-04T01:00:00Z",
         "state":"$state","durationSec":3600,"positionSec":$positionSec,"canManage":true
         ${updated?.let { ""","positionUpdatedAt":"$it"""" } ?: ""}}
    """.trimIndent()

    @Before
    fun setUp() {
        server = MockWebServer()
        server.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse {
                val r = Req(request.method.orEmpty(), request.path.orEmpty(), request.body.readUtf8())
                requests += r
                return when {
                    r.method == "POST" && r.path == "/api/v1/auth/login" -> MockResponse().setBody(TOKEN_PAIR)
                    r.method == "GET" && r.path.startsWith("/api/v1/recordings?state=") ->
                        MockResponse().setResponseCode(listCode)
                            .setBody(if (listCode == 200) list else """{"error":"boom"}""")
                            .setBodyDelay(listDelayMs, TimeUnit.MILLISECONDS)
                    r.method == "PUT" && r.path.endsWith("/position") ->
                        MockResponse().setResponseCode(putCode)
                            .setBody(if (putCode == 204) "" else """{"error":"not found"}""")
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

    private suspend fun vm(): ContinueWatchingViewModel {
        val client = BowtieClient(server.url("/"), InMemoryTokenStore())
        client.login("alice", "secret")
        return ContinueWatchingViewModel(client)
    }

    @Test
    fun refresh_asksForRecorded_andKeepsInProgressNewestFirst() = runBlocking {
        list = "[" + listOf(
            rec(1, positionSec = 0),
            rec(2, positionSec = 600, updated = "2026-10-06T01:00:00Z"),
            rec(3, positionSec = 300, updated = "2026-10-06T02:00:00Z"),
            rec(4, positionSec = 300, state = "recording"),
        ).joinToString(",") + "]"
        val vm = vm()
        assertEquals(emptyList<Long>(), vm.items.value.map { it.id })

        vm.refresh()

        assertEquals(listOf(3L, 2L), vm.items.value.map { it.id })
        assertTrue(requests.any { it.method == "GET" && it.path == "/api/v1/recordings?state=recorded" })
    }

    @Test
    fun refreshFailure_keepsWhatWasShown() = runBlocking {
        list = "[${rec(2, positionSec = 600)}]"
        val vm = vm()
        vm.refresh()
        listCode = 503

        vm.refresh()

        assertEquals(listOf(2L), vm.items.value.map { it.id })
    }

    @Test
    fun remove_resetsPositionToZero_andDropsIt() = runBlocking {
        list = "[${rec(2, positionSec = 600)},${rec(5, positionSec = 700)}]"
        val vm = vm()
        vm.refresh()

        val ok = vm.remove(vm.items.value.first { it.id == 2L })

        assertTrue(ok)
        assertEquals(listOf(5L), vm.items.value.map { it.id })
        val put = requests.last { it.method == "PUT" }
        assertEquals("/api/v1/recordings/2/position", put.path)
        assertEquals("""{"positionSec":0}""", put.body)
        assertNull(vm.message.value)
    }

    @Test
    fun removeFailure_keepsIt_andExplains() = runBlocking {
        list = "[${rec(2, positionSec = 600)}]"
        val vm = vm()
        vm.refresh()
        putCode = 404

        val ok = vm.remove(vm.items.value.first())

        assertFalse(ok)
        assertEquals(listOf(2L), vm.items.value.map { it.id })
        assertEquals("That recording is gone.", vm.message.value)
        vm.clearMessage()
        assertNull(vm.message.value)
    }

    @Test
    fun removeDuringARefresh_keepsItRemoved() = runBlocking {
        list = "[${rec(2, positionSec = 600)},${rec(5, positionSec = 700)}]"
        val vm = vm()
        vm.refresh()
        val target = vm.items.value.first { it.id == 2L }
        // A list fetch already under way answers after the remove, with the old position.
        listDelayMs = 400

        val stale = launch { vm.refresh() }
        delay(100)
        assertTrue(vm.remove(target))
        stale.join()

        assertEquals(listOf(5L), vm.items.value.map { it.id })
    }

    @Test
    fun removed_staysHiddenUntilTheServerConfirms() = runBlocking {
        list = "[${rec(2, positionSec = 600)},${rec(5, positionSec = 700)}]"
        val vm = vm()
        vm.refresh()
        assertTrue(vm.remove(vm.items.value.first { it.id == 2L }))

        // A list read that hasn't caught up with the reset yet.
        vm.refresh()
        assertEquals(listOf(5L), vm.items.value.map { it.id })

        // The server confirms (position 0); watching it again later brings it back.
        list = "[${rec(2, positionSec = 0)},${rec(5, positionSec = 700)}]"
        vm.refresh()
        assertEquals(listOf(5L), vm.items.value.map { it.id })
        list = "[${rec(2, positionSec = 900)},${rec(5, positionSec = 700)}]"
        vm.refresh()
        assertEquals(setOf(2L, 5L), vm.items.value.map { it.id }.toSet())
    }
}
