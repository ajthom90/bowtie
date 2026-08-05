package app.bowtie

import app.bowtie.core.BowtieClient
import app.bowtie.core.InMemoryTokenStore
import app.bowtie.core.TokenStore
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.test.setMain
import okhttp3.HttpUrl
import okhttp3.OkHttpClient
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test

@OptIn(ExperimentalCoroutinesApi::class)
class AppViewModelTest {

    private lateinit var server: MockWebServer
    private lateinit var store: TokenStore

    private val userJson =
        """{"id":1,"username":"alice","role":"viewer","maxQuality":"high"}"""

    private fun tokenPairJson(
        access: String = "access-1",
        refresh: String = "refresh-1",
    ) = """
        {
          "accessToken":"$access",
          "refreshToken":"$refresh",
          "user":$userJson
        }
    """.trimIndent()

    @Before
    fun setUp() {
        Dispatchers.setMain(StandardTestDispatcher())
        server = MockWebServer()
        server.start()
        store = InMemoryTokenStore()
    }

    @After
    fun tearDown() {
        server.shutdown()
        Dispatchers.resetMain()
    }

    private fun baseUrl(): HttpUrl = server.url("/")

    private fun clientFactory(): (HttpUrl) -> BowtieClient = { url ->
        BowtieClient(url, store, OkHttpClient())
    }

    private fun makeVmWithScope(
        scope: kotlinx.coroutines.CoroutineScope,
        tokenStore: TokenStore = store,
    ): AppViewModel = AppViewModel(
        store = tokenStore,
        clientFactory = clientFactory(),
        scope = scope,
        healthClient = OkHttpClient(),
    )

    private fun TestScope.installMain() {
        Dispatchers.setMain(StandardTestDispatcher(testScheduler))
    }

    /**
     * Pump test scheduler + real IO until [condition].
     * [BowtieClient] uses [Dispatchers.IO]; advanceUntilIdle alone returns while IO runs.
     */
    private fun TestScope.pumpUntil(
        timeoutMs: Long = 5_000,
        condition: () -> Boolean,
    ) {
        val deadline = System.nanoTime() + timeoutMs * 1_000_000L
        while (!condition()) {
            advanceUntilIdle()
            if (condition()) break
            if (System.nanoTime() > deadline) {
                error("timeout waiting for condition")
            }
            Thread.sleep(2)
        }
        advanceUntilIdle()
    }

    // ── Phase transitions ───────────────────────────────────────────────────

    @Test
    fun freshStoreStartsAtConnect() = runTest {
        installMain()
        val vm = makeVmWithScope(this)
        assertEquals(AppViewModel.Phase.Connect, vm.phase.value)
        assertNull(vm.client)
    }

    @Test
    fun storedServerWithoutTokenStartsAtLogin() = runTest {
        installMain()
        store.save(baseUrl().toString(), null)
        val vm = makeVmWithScope(this)
        assertEquals(AppViewModel.Phase.Login, vm.phase.value)
        assertNotNull(vm.client)
    }

    @Test
    fun storedServerAndToken_checkingThenReady() = runTest {
        installMain()
        store.save(baseUrl().toString(), "stored-refresh")
        server.enqueue(MockResponse().setBody(tokenPairJson(access = "a2", refresh = "r2")))

        val vm = makeVmWithScope(this)
        assertEquals(AppViewModel.Phase.Checking, vm.phase.value)

        vm.start()
        pumpUntil { vm.phase.value is AppViewModel.Phase.Ready }

        val phase = vm.phase.value
        assertTrue("expected Ready, got $phase", phase is AppViewModel.Phase.Ready)
        assertEquals("alice", (phase as AppViewModel.Phase.Ready).user.username)
        assertEquals("r2", store.loadRefreshToken())
    }

    @Test
    fun bootstrapFailureFallsBackToLogin() = runTest {
        installMain()
        store.save(baseUrl().toString(), "dead-token")
        server.enqueue(
            MockResponse()
                .setResponseCode(401)
                .setBody("""{"error":"invalid refresh token"}"""),
        )

        val vm = makeVmWithScope(this)
        assertEquals(AppViewModel.Phase.Checking, vm.phase.value)

        vm.start()
        pumpUntil { vm.phase.value is AppViewModel.Phase.Login }

        assertEquals(AppViewModel.Phase.Login, vm.phase.value)
        // Server kept for reconnect.
        assertEquals(baseUrl().toString(), store.loadServer())
        assertNull(store.loadRefreshToken())
    }

    // ── Connect / sign-in / sign-out / change-server ────────────────────────

    @Test
    fun connectValidatesHealthzAndAdvancesToLogin() = runTest {
        installMain()
        server.enqueue(MockResponse().setResponseCode(200).setBody("ok"))

        val vm = makeVmWithScope(this)
        val host = server.hostName
        val port = server.port
        val ok = vm.connect("$host:$port")
        assertTrue(ok)
        assertEquals(AppViewModel.Phase.Login, vm.phase.value)
        assertNotNull(vm.client)
        assertNotNull(store.loadServer())
    }

    @Test
    fun connectRejectsUnreachableServer() = runTest {
        installMain()
        server.enqueue(MockResponse().setResponseCode(503))

        val vm = makeVmWithScope(this)
        val host = server.hostName
        val port = server.port
        val ok = vm.connect("$host:$port")
        assertFalse(ok)
        assertEquals(AppViewModel.Phase.Connect, vm.phase.value)
        assertNull(store.loadServer())
    }

    @Test
    fun signInAdvancesToReady() = runTest {
        installMain()
        store.save(baseUrl().toString(), null)
        server.enqueue(MockResponse().setBody(tokenPairJson()))

        val vm = makeVmWithScope(this)
        assertEquals(AppViewModel.Phase.Login, vm.phase.value)

        vm.signIn("alice", "s3cret")

        val phase = vm.phase.value
        assertTrue("expected Ready, got $phase", phase is AppViewModel.Phase.Ready)
        assertEquals("alice", (phase as AppViewModel.Phase.Ready).user.username)
    }

    @Test
    fun signOutReturnsToLoginKeepingServer() = runTest {
        installMain()
        store.save(baseUrl().toString(), "r1")
        server.enqueue(MockResponse().setBody(tokenPairJson()))
        server.enqueue(MockResponse().setResponseCode(204))

        val vm = makeVmWithScope(this)
        vm.start()
        pumpUntil { vm.phase.value is AppViewModel.Phase.Ready }
        assertTrue(vm.phase.value is AppViewModel.Phase.Ready)

        vm.signOut()

        assertEquals(AppViewModel.Phase.Login, vm.phase.value)
        assertEquals(baseUrl().toString(), store.loadServer())
        assertNull(store.loadRefreshToken())
    }

    @Test
    fun changeServerClearsEverything() = runTest {
        installMain()
        store.save(baseUrl().toString(), "r1")
        server.enqueue(MockResponse().setBody(tokenPairJson()))

        val vm = makeVmWithScope(this)
        vm.start()
        pumpUntil { vm.phase.value is AppViewModel.Phase.Ready }
        assertTrue(vm.phase.value is AppViewModel.Phase.Ready)

        vm.changeServer()

        assertEquals(AppViewModel.Phase.Connect, vm.phase.value)
        assertNull(vm.client)
        assertNull(store.loadServer())
        assertNull(store.loadRefreshToken())
    }
}
