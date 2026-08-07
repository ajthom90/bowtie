package app.bowtie.core

import kotlinx.coroutines.async
import kotlinx.coroutines.awaitAll
import kotlinx.coroutines.runBlocking
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.mockwebserver.Dispatcher
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import okhttp3.mockwebserver.RecordedRequest
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Before
import org.junit.Test
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicInteger

class BowtieClientTest {

    private lateinit var server: MockWebServer
    private lateinit var store: InMemoryTokenStore

    private val userJson = """
        {"id":1,"username":"alice","role":"viewer","maxQuality":"high"}
    """.trimIndent()

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
        server = MockWebServer()
        server.start()
        store = InMemoryTokenStore()
    }

    @After
    fun tearDown() {
        server.shutdown()
    }

    private fun client(okHttp: OkHttpClient = OkHttpClient()): BowtieClient {
        return BowtieClient(server.url("/"), store, okHttp)
    }

    private fun take(pathContains: String? = null): RecordedRequest {
        val req = server.takeRequest(5, TimeUnit.SECONDS)
            ?: error("expected request${pathContains?.let { " matching $it" } ?: ""}")
        if (pathContains != null) {
            assertTrue(
                "expected path containing $pathContains, got ${req.path}",
                req.path!!.contains(pathContains),
            )
        }
        return req
    }

    // ── bearer + request-body shapes ────────────────────────────────────────

    @Test
    fun bearerAttached() = runBlocking {
        server.enqueue(MockResponse().setBody(tokenPairJson()))
        server.enqueue(MockResponse().setBody(userJson))

        val c = client()
        c.login("alice", "secret")
        c.me()

        val login = take("/api/v1/auth/login")
        assertNull(login.getHeader("Authorization"))

        val me = take("/api/v1/me")
        assertEquals("Bearer access-1", me.getHeader("Authorization"))
    }

    @Test
    fun loginRequestBody_usernamePassword() = runBlocking {
        server.enqueue(MockResponse().setBody(tokenPairJson()))
        client().login("alice", "secret")
        val req = take("/api/v1/auth/login")
        assertEquals("POST", req.method)
        val body = req.body.readUtf8()
        assertTrue("body=$body", body.contains("\"username\":\"alice\""))
        assertTrue("body=$body", body.contains("\"password\":\"secret\""))
        // Exact OpenAPI shape: only those two fields
        assertEquals("""{"username":"alice","password":"secret"}""", body)
    }

    @Test
    fun refreshAndLogoutRequestBody_refreshToken() = runBlocking {
        store.save(server.url("/").toString(), "rt-old")
        server.enqueue(
            MockResponse().setBody(tokenPairJson(access = "access-new", refresh = "rt-new")),
        )
        server.enqueue(MockResponse().setResponseCode(204))

        val c = client()
        c.bootstrapFromStoredToken()
        c.logout()

        val refresh = take("/api/v1/auth/refresh")
        assertEquals("POST", refresh.method)
        assertEquals("""{"refreshToken":"rt-old"}""", refresh.body.readUtf8())

        val logout = take("/api/v1/auth/logout")
        assertEquals("POST", logout.method)
        // logout uses the rotated token that was persisted
        assertEquals("""{"refreshToken":"rt-new"}""", logout.body.readUtf8())
    }

    @Test
    fun passwordRequestBody_currentAndNew() = runBlocking {
        server.enqueue(MockResponse().setBody(tokenPairJson()))
        server.enqueue(MockResponse().setResponseCode(204))

        val c = client()
        c.login("alice", "secret")
        c.changePassword("secret", "newer")

        take("/api/v1/auth/login")
        val pwd = take("/api/v1/me/password")
        assertEquals("POST", pwd.method)
        assertEquals(
            """{"currentPassword":"secret","newPassword":"newer"}""",
            pwd.body.readUtf8(),
        )
        assertEquals("Bearer access-1", pwd.getHeader("Authorization"))
    }

    @Test
    fun sessionCreateRequestBody_shape() = runBlocking {
        server.enqueue(MockResponse().setBody(tokenPairJson()))
        server.enqueue(
            MockResponse().setBody(
                """{"viewerId":"v1","playlistUrl":"/api/v1/stream/v1/index.m3u8?token=t"}""",
            ),
        )

        val caps = ClientCaps(
            videoCodecs = listOf("h264"),
            audioCodecs = listOf("aac"),
            maxHeight = 1080,
            profile = "high",
        )
        val c = client()
        c.login("alice", "secret")
        c.createSession(channelId = 7L, caps = caps)

        take("/api/v1/auth/login")
        val create = take("/api/v1/sessions")
        assertEquals("POST", create.method)
        val body = create.body.readUtf8()
        assertEquals(
            """{"channelId":7,"caps":{"videoCodecs":["h264"],"audioCodecs":["aac"],"maxHeight":1080,"profile":"high"}}""",
            body,
        )
    }

    // ── single-flight refresh ───────────────────────────────────────────────

    @Test
    fun singleFlightRefresh() = runBlocking {
        val refreshCount = AtomicInteger(0)

        server.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse {
                val path = request.path ?: ""
                return when {
                    path.contains("/auth/login") -> MockResponse().setBody(
                        tokenPairJson(access = "access-old", refresh = "rt-old"),
                    )
                    path.contains("/auth/refresh") -> {
                        val n = refreshCount.incrementAndGet()
                        val body = request.body.readUtf8()
                        // Only the first refresh should fire; it must use the pre-rotation token.
                        // (Do not assertEquals here — failures in the dispatcher hang the client.)
                        if (n == 1 && body != """{"refreshToken":"rt-old"}""") {
                            return MockResponse().setResponseCode(500)
                                .setBody("""{"error":"unexpected refresh body: $body"}""")
                        }
                        if (n > 1) {
                            return MockResponse().setResponseCode(500)
                                .setBody("""{"error":"second refresh not allowed: $body"}""")
                        }
                        MockResponse().setBody(
                            tokenPairJson(access = "access-new", refresh = "rt-new"),
                        )
                    }
                    path.contains("/api/v1/me") -> {
                        val auth = request.getHeader("Authorization")
                        when (auth) {
                            "Bearer access-old" -> MockResponse().setResponseCode(401)
                                .setBody("""{"error":"unauthorized"}""")
                            "Bearer access-new" -> {
                                // New refresh must be persisted BEFORE retry fires
                                assertEquals("rt-new", store.loadRefreshToken())
                                MockResponse().setBody(userJson)
                            }
                            else -> MockResponse().setResponseCode(500)
                                .setBody("""{"error":"bad auth $auth"}""")
                        }
                    }
                    else -> MockResponse().setResponseCode(404)
                }
            }
        }

        val c = client()
        c.login("alice", "secret")
        assertEquals("rt-old", store.loadRefreshToken())

        // 3 concurrent me() calls → all 401 → exactly ONE refresh → all succeed
        val results = listOf(
            async { c.me() },
            async { c.me() },
            async { c.me() },
        ).awaitAll()

        assertEquals(3, results.size)
        results.forEach { assertEquals("alice", it.username) }
        assertEquals("exactly one refresh", 1, refreshCount.get())
        assertEquals("rt-new", store.loadRefreshToken())
    }

    @Test
    fun refreshFailureSignsOut() = runBlocking {
        server.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse {
                val path = request.path ?: ""
                return when {
                    path.contains("/auth/login") -> MockResponse().setBody(
                        tokenPairJson(access = "access-old", refresh = "rt-old"),
                    )
                    path.contains("/auth/refresh") -> MockResponse()
                        .setResponseCode(401)
                        .setBody("""{"error":"invalid or expired refresh token"}""")
                    path.contains("/api/v1/me") -> MockResponse()
                        .setResponseCode(401)
                        .setBody("""{"error":"unauthorized"}""")
                    else -> MockResponse().setResponseCode(404)
                }
            }
        }

        val c = client()
        c.login("alice", "secret")
        assertEquals("rt-old", store.loadRefreshToken())
        assertEquals("alice", c.currentUser.value?.username)

        try {
            c.me()
            fail("expected Unauthorized")
        } catch (_: BowtieError.Unauthorized) {
            // expected
        }

        assertNull("refresh token cleared", store.loadRefreshToken())
        assertNull("currentUser cleared", c.currentUser.value)
        // server URL kept (Connect state preserved)
        assertEquals(server.url("/").toString(), store.loadServer())
    }

    // ── error mapping ───────────────────────────────────────────────────────

    @Test
    fun tunersBusyDecoded() = runBlocking {
        // FULL wire-shape 503 fixture (SessionInfo + ViewerInfo fields beyond our model)
        val full503 = """
            {
              "error": "all tuners in use",
              "sessions": [
                {
                  "id": "sess-abc",
                  "channelId": 7,
                  "channelName": "WABC",
                  "key": "7|h264|high",
                  "videoCodec": "h264",
                  "profile": "high",
                  "backend": "videotoolbox",
                  "startedAt": "2026-08-05T12:00:00Z",
                  "viewers": [
                    {
                      "id": "viewer-1",
                      "username": "alice",
                      "lastSeen": "2026-08-05T12:05:00Z"
                    },
                    {
                      "id": "viewer-2",
                      "username": "bob",
                      "lastSeen": "2026-08-05T12:06:00Z"
                    }
                  ]
                }
              ]
            }
        """.trimIndent()

        server.enqueue(MockResponse().setBody(tokenPairJson()))
        server.enqueue(MockResponse().setResponseCode(503).setBody(full503))

        val c = client()
        c.login("alice", "secret")
        try {
            c.createSession(
                7L,
                ClientCaps(listOf("h264"), listOf("aac"), 1080, "high"),
            )
            fail("expected TunersBusy")
        } catch (e: BowtieError.TunersBusy) {
            assertEquals(1, e.sessions.size)
            assertEquals("WABC", e.sessions[0].channelName)
            assertEquals(listOf("alice", "bob"), e.sessions[0].viewers.map { it.username })
        }
    }

    @Test
    fun negotiation422() = runBlocking {
        server.enqueue(MockResponse().setBody(tokenPairJson()))
        server.enqueue(
            MockResponse()
                .setResponseCode(422)
                .setBody("""{"error":"no compatible codec/profile"}"""),
        )

        val c = client()
        c.login("alice", "secret")
        try {
            c.createSession(
                1L,
                ClientCaps(listOf("h264"), listOf("aac"), 1080, "ultra"),
            )
            fail("expected NegotiationFailed")
        } catch (e: BowtieError.NegotiationFailed) {
            assertEquals("no compatible codec/profile", e.message)
        }
    }

    @Test
    fun deleteSwallows() = runBlocking {
        server.enqueue(MockResponse().setBody(tokenPairJson()))
        server.enqueue(MockResponse().setResponseCode(500).setBody("""{"error":"boom"}"""))
        server.enqueue(MockResponse().setResponseCode(401).setBody("""{"error":"nope"}"""))
        // refresh also fails — still must not throw from deleteSession
        server.enqueue(MockResponse().setResponseCode(401).setBody("""{"error":"dead"}"""))

        val c = client()
        c.login("alice", "secret")
        // 500
        c.deleteSession("viewer-x")
        // 401 path (refresh fails) — still swallowed
        c.deleteSession("viewer-y")
    }

    @Test
    fun noAuthHeaderOnStreamPaths() = runBlocking {
        // BowtieClient must not install a global auth interceptor. Stream/HLS
        // requests use ?token= only; Media3 must not share a Bearer-intercepted client.
        server.enqueue(MockResponse().setBody(tokenPairJson()))
        server.enqueue(MockResponse().setBody("#EXTM3U\n"))

        val okHttp = OkHttpClient()
        val c = client(okHttp)
        c.login("alice", "secret")

        // Same OkHttp instance (as Media3 would if wrongly shared) — no interceptor
        // means stream requests stay free of Authorization.
        assertTrue(
            "client must not install application interceptors",
            okHttp.interceptors.isEmpty(),
        )
        assertTrue(
            "client must not install network interceptors",
            okHttp.networkInterceptors.isEmpty(),
        )

        val streamUrl = server.url("/api/v1/stream/vid-1/index.m3u8?token=signed")
        okHttp.newCall(Request.Builder().url(streamUrl).get().build()).execute().use {
            assertEquals(200, it.code)
        }

        take("/api/v1/auth/login")
        val streamReq = take("/api/v1/stream/")
        assertNull(
            "stream paths must not carry Authorization",
            streamReq.getHeader("Authorization"),
        )
        assertTrue(BowtieClient.isStreamPath(streamReq.path!!))
    }

    @Test
    fun heartbeatRequestShape_tokenQueryNoAuthorization() = runBlocking {
        server.enqueue(MockResponse().setBody(tokenPairJson()))
        server.enqueue(MockResponse().setResponseCode(204))

        val c = client()
        c.login("alice", "secret")
        c.heartbeat("viewer-abc", "stream-tok-xyz")

        take("/api/v1/auth/login")
        val beat = take("/api/v1/sessions/viewer-abc/heartbeat")
        assertEquals("POST", beat.method)
        assertTrue(
            "path must include token query: ${beat.path}",
            beat.path!!.contains("token=stream-tok-xyz"),
        )
        assertNull(
            "heartbeat must not send Bearer — stream token alone authorizes",
            beat.getHeader("Authorization"),
        )
        Unit
    }

    @Test
    fun heartbeatSwallowsErrors() = runBlocking {
        server.enqueue(MockResponse().setBody(tokenPairJson()))
        server.enqueue(MockResponse().setResponseCode(500).setBody("""{"error":"boom"}"""))

        val c = client()
        c.login("alice", "secret")
        // Must not throw.
        c.heartbeat("vid", "tok")
        take("/api/v1/auth/login")
        take("heartbeat")
        Unit
    }

    @Test
    fun logoutClearsEvenWhenNetworkFails() = runBlocking {
        server.enqueue(MockResponse().setBody(tokenPairJson()))
        // logout endpoint fails
        server.enqueue(MockResponse().setResponseCode(500))

        val c = client()
        c.login("alice", "secret")
        c.logout()

        assertNull(store.loadRefreshToken())
        assertNull(c.currentUser.value)
        assertEquals(server.url("/").toString(), store.loadServer())
    }

    @Test
    fun bootstrapFromStoredToken_absentThrows() = runBlocking {
        try {
            client().bootstrapFromStoredToken()
            fail("expected Unauthorized")
        } catch (_: BowtieError.Unauthorized) {
            // expected
        }
    }
}
