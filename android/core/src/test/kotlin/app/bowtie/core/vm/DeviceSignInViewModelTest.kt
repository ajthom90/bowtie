package app.bowtie.core.vm

import app.bowtie.core.BowtieClient
import app.bowtie.core.BowtieClientRecordingsTest.Companion.TOKEN_PAIR
import app.bowtie.core.BowtieClientRecordingsTest.Companion.recordingJson
import app.bowtie.core.BowtieError
import app.bowtie.core.DevicePoll
import app.bowtie.core.InMemoryTokenStore
import app.bowtie.core.User
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.delay
import kotlinx.coroutines.runBlocking
import okhttp3.OkHttpClient
import okhttp3.mockwebserver.Dispatcher
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import okhttp3.mockwebserver.RecordedRequest
import okhttp3.mockwebserver.SocketPolicy
import okio.Buffer
import org.junit.After
import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Before
import org.junit.Test
import java.util.concurrent.CopyOnWriteArrayList
import java.util.concurrent.atomic.AtomicInteger

/** TV quick sign-in: `POST /auth/device`, the QR PNG, and polling `/auth/device/token`. */
class DeviceSignInViewModelTest {

    private data class Req(val method: String, val path: String, val body: String, val auth: String?)

    private lateinit var server: MockWebServer
    private val store = InMemoryTokenStore()
    private val requests = CopyOnWriteArrayList<Req>()
    private val workScope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    private val sleeps = CopyOnWriteArrayList<Long>()

    private val starts = AtomicInteger(0)
    private var startCode = 200
    private var expiresIn = 600
    private var interval = 5

    /** Scripted poll answers in order; the last one repeats. */
    @Volatile
    private var polls: List<MockResponse> = listOf(pending())
    private val pollCount = AtomicInteger(0)

    private val png = byteArrayOf(0x89.toByte(), 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0xFF.toByte(), 0xFE.toByte(), 0x80.toByte())

    private fun pending() = MockResponse().setResponseCode(428).setBody("""{"error":"authorization_pending"}""")
    private fun expired() = MockResponse().setResponseCode(410).setBody("""{"error":"expired_token"}""")
    private fun approved() = MockResponse().setBody(TOKEN_PAIR)

    @Before
    fun setUp() {
        server = MockWebServer()
        server.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse {
                val r = Req(
                    request.method.orEmpty(),
                    request.path.orEmpty(),
                    request.body.readUtf8(),
                    request.getHeader("Authorization"),
                )
                requests += r
                val p = r.path
                return when {
                    r.method == "POST" && p == "/api/v1/auth/device" -> {
                        val n = starts.incrementAndGet()
                        if (startCode != 200) {
                            MockResponse().setResponseCode(startCode)
                                .setBody("""{"error":"too many sign-ins in progress; try again shortly"}""")
                        } else {
                            MockResponse().setBody(
                                """{"deviceCode":"dev-$n","userCode":"BCDF-234$n",
                                   "verifyUrl":"http://192.168.1.5:8080/link?code=BCDF234$n",
                                   "qrUrl":"/api/v1/auth/device/qr/BCDF234$n.png",
                                   "expiresIn":$expiresIn,"interval":$interval}""",
                            )
                        }
                    }
                    r.method == "POST" && p == "/api/v1/auth/device/token" -> {
                        val i = pollCount.getAndIncrement()
                        polls.getOrElse(i) { polls.last() }
                    }
                    r.method == "GET" && p.startsWith("/api/v1/auth/device/qr/") ->
                        if (p.endsWith("BCDF2341.png")) {
                            MockResponse().setHeader("Content-Type", "image/png").setBody(Buffer().write(png))
                        } else {
                            MockResponse().setResponseCode(404).setBody("""{"error":"that code has expired"}""")
                        }
                    r.method == "GET" && p.startsWith("/api/v1/recordings") ->
                        MockResponse().setBody("[${recordingJson()}]")
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

    private fun client() = BowtieClient(server.url("/"), store, OkHttpClient())

    private fun vm(client: BowtieClient = client()) = DeviceSignInViewModel(
        client = client,
        deviceName = "Fire TV",
        scope = workScope,
        // Records the interval; a short real pause keeps pending loops from spinning.
        sleeper = { ms ->
            sleeps += ms
            delay(20)
        },
    )

    private fun waitFor(timeoutMs: Long = 5_000, condition: () -> Boolean) {
        val deadline = System.currentTimeMillis() + timeoutMs
        while (!condition()) {
            check(System.currentTimeMillis() < deadline) { "timed out" }
            Thread.sleep(5)
        }
    }

    private fun tokenPolls() = requests.filter { it.path == "/api/v1/auth/device/token" }

    // ── Client ──────────────────────────────────────────────────────────────

    @Test
    fun client_start_sendsDeviceName_unauthenticated() = runBlocking {
        val code = client().startDeviceSignIn("Fire TV")

        val req = requests.single()
        assertEquals("""{"deviceName":"Fire TV"}""", req.body)
        assertNull(req.auth)
        assertEquals("dev-1", code.deviceCode)
        assertEquals("BCDF-2341", code.userCode)
        assertEquals("http://192.168.1.5:8080/link?code=BCDF2341", code.verifyUrl)
        assertEquals("/api/v1/auth/device/qr/BCDF2341.png", code.qrUrl)
        assertEquals(600, code.expiresIn)
        assertEquals(5, code.interval)
    }

    @Test
    fun client_poll_pendingExpiredThenSignedIn_persistsTokens() = runBlocking {
        polls = listOf(pending(), expired(), approved())
        val c = client()

        assertEquals(DevicePoll.Pending, c.pollDeviceSignIn("dev-1"))
        assertEquals(DevicePoll.Expired, c.pollDeviceSignIn("dev-1"))
        assertNull(store.loadRefreshToken())

        val signedIn = c.pollDeviceSignIn("dev-1") as DevicePoll.SignedIn
        assertEquals("alice", signedIn.user.username)
        assertEquals("""{"deviceCode":"dev-1"}""", tokenPolls().last().body)
        // Signed in exactly as with a password: refresh token stored, access token used.
        assertEquals("refresh-1", store.loadRefreshToken())
        assertEquals("alice", c.currentUser.value?.username)
        c.recordings()
        assertEquals("Bearer access-1", requests.last().auth)
    }

    @Test
    fun client_qrPng_keepsBinaryBytes() = runBlocking {
        val bytes = client().deviceQrPng("/api/v1/auth/device/qr/BCDF2341.png")
        assertArrayEquals(png, bytes)
        assertNull(requests.last().auth)
    }

    @Test
    fun client_qrPng_404_isNotFound() = runBlocking {
        try {
            client().deviceQrPng("/api/v1/auth/device/qr/GONE.png")
            fail("expected NotFound")
        } catch (_: BowtieError.NotFound) {
        }
    }

    // ── View model ──────────────────────────────────────────────────────────

    @Test
    fun polls_everyInterval_untilApproved() {
        polls = listOf(pending(), pending(), approved())
        val m = vm()
        m.start()
        waitFor { m.state.value is DeviceSignInViewModel.State.SignedIn }

        assertEquals("alice", (m.state.value as DeviceSignInViewModel.State.SignedIn).user.username)
        assertEquals(3, tokenPolls().size)
        assertEquals(listOf(5_000L, 5_000L, 5_000L), sleeps.toList())
        assertEquals("refresh-1", store.loadRefreshToken())
    }

    @Test
    fun waiting_showsCodeLinkAndQr() {
        val m = vm()
        m.start()
        waitFor { m.qrPng.value != null }

        val waiting = m.state.value as DeviceSignInViewModel.State.Waiting
        assertEquals("BCDF-2341", waiting.userCode)
        assertEquals("192.168.1.5:8080/link", waiting.link)
        assertArrayEquals(png, m.qrPng.value)
        m.stop()
    }

    @Test
    fun expired410_offersNewCode_andStartAgainGetsOne() {
        polls = listOf(pending(), expired())
        val m = vm()
        m.start()
        waitFor { m.state.value is DeviceSignInViewModel.State.Expired }
        assertEquals(2, tokenPolls().size)

        polls = listOf(pending())
        pollCount.set(0)
        m.start()
        waitFor { (m.state.value as? DeviceSignInViewModel.State.Waiting)?.userCode == "BCDF-2342" }
        assertEquals(2, starts.get())
        m.stop()
    }

    @Test
    fun expiresLocally_whenTheCodeOutlivesExpiresIn() {
        expiresIn = 12
        interval = 5
        val m = vm()
        m.start()
        waitFor { m.state.value is DeviceSignInViewModel.State.Expired }
        // Polls at 5 s and 10 s; at 15 s the code is past its 12 s life.
        assertEquals(2, tokenPolls().size)
    }

    @Test
    fun networkErrorWhilePolling_keepsPolling() {
        polls = listOf(
            pending(),
            MockResponse().setSocketPolicy(SocketPolicy.DISCONNECT_AT_START),
            MockResponse().setResponseCode(500).setBody("""{"error":"boom"}"""),
            approved(),
        )
        val m = vm()
        m.start()
        waitFor { m.state.value is DeviceSignInViewModel.State.SignedIn }
        assertEquals(4, pollCount.get())
    }

    @Test
    fun startRefused_isFailedWithServerMessage() {
        startCode = 429
        val m = vm()
        m.start()
        waitFor { m.state.value is DeviceSignInViewModel.State.Failed }
        assertEquals(
            "too many sign-ins in progress; try again shortly",
            (m.state.value as DeviceSignInViewModel.State.Failed).message,
        )
        assertEquals(0, tokenPolls().size)
    }

    @Test
    fun stop_endsPolling() {
        val m = vm()
        m.start()
        waitFor { tokenPolls().size >= 2 }
        m.stop()
        Thread.sleep(50)
        val n = tokenPolls().size
        Thread.sleep(100)
        assertEquals(n, tokenPolls().size)
    }

    @Test
    fun helpers_linkTextAndDeviceName() {
        assertEquals("192.168.1.5:8080/link", DeviceSignInViewModel.linkText("http://192.168.1.5:8080/link?code=BCDF2345"))
        assertEquals("tv.example.com/link", DeviceSignInViewModel.linkText("https://tv.example.com/link"))
        assertEquals("Fire TV", DeviceSignInViewModel.deviceName(manufacturer = "Amazon", model = "AFTMM"))
        assertEquals("SHIELD Android TV", DeviceSignInViewModel.deviceName("NVIDIA", "SHIELD Android TV"))
        assertEquals("Android TV", DeviceSignInViewModel.deviceName("Google", "  "))
    }

    @Test
    fun appViewModel_completeSignIn_goesReady() {
        store.save(server.url("/").toString(), null)
        val app = AppViewModel(
            store = store,
            clientFactory = { url -> BowtieClient(url, store, OkHttpClient()) },
            scope = workScope,
        )
        assertEquals(AppViewModel.Phase.Login, app.phase.value)
        val user = User(id = 1, username = "alice", role = "viewer", maxQuality = "high")
        app.completeSignIn(user)
        assertEquals(AppViewModel.Phase.Ready(user), app.phase.value)
        assertTrue(app.client != null)
    }
}
