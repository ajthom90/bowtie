package app.bowtie.core

import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import kotlinx.serialization.Serializable
import kotlinx.serialization.encodeToString
import okhttp3.HttpUrl
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import okhttp3.Response
import java.time.Instant

/**
 * Viewer-allowlist HTTP client: login/refresh/logout/me/password/channels/guide/sessions.
 *
 * Access token is memory-only. Refresh is **single-flight** (Mutex-coalesced): concurrent
 * 401s share one `/auth/refresh`; the new refresh token is persisted **before** retries.
 *
 * No OkHttp auth interceptor — Media3 must use an unauthenticated data source;
 * stream auth is the playlist `?token=` query (Global Constraints).
 */
class BowtieClient(
    val server: HttpUrl,
    private val store: TokenStore,
    private val okHttp: OkHttpClient = OkHttpClient(),
) {
    private val _currentUser = MutableStateFlow<User?>(null)
    val currentUser: StateFlow<User?> = _currentUser.asStateFlow()

    @Volatile
    private var accessToken: String? = null

    private val refreshMutex = Mutex()

    suspend fun login(username: String, password: String): User = withContext(Dispatchers.IO) {
        val pair = postUnauthed(
            path = "/api/v1/auth/login",
            bodyJson = BowtieJson.encodeToString(
                LoginRequest(username = username, password = password),
            ),
        )
        applyTokens(pair)
        pair.user
    }

    /**
     * Rotate the stored refresh token into a live session.
     * Throws [BowtieError.Unauthorized] when absent or rotation fails.
     */
    suspend fun bootstrapFromStoredToken(): User = withContext(Dispatchers.IO) {
        val rt = store.loadRefreshToken() ?: throw BowtieError.Unauthorized
        try {
            performRefresh(rt)
        } catch (_: BowtieError.Unauthorized) {
            throw BowtieError.Unauthorized
        } catch (_: BowtieError) {
            clearSessionKeepServer()
            throw BowtieError.Unauthorized
        }
        _currentUser.value ?: throw BowtieError.Unauthorized
    }

    /** Best-effort logout; always clears in-memory session and stored refresh token. */
    suspend fun logout() = withContext(Dispatchers.IO) {
        val rt = store.loadRefreshToken()
        if (rt != null) {
            try {
                val request = Request.Builder()
                    .url(apiUrl("/api/v1/auth/logout"))
                    .post(jsonBody(BowtieJson.encodeToString(RefreshRequest(refreshToken = rt))))
                    .header("Content-Type", JSON_MEDIA)
                    .build()
                okHttp.newCall(request).execute().close()
            } catch (_: Exception) {
                // best-effort
            }
        }
        clearSessionKeepServer()
    }

    suspend fun changePassword(current: String, new: String): Unit = withContext(Dispatchers.IO) {
        authed(
            method = "POST",
            path = "/api/v1/me/password",
            bodyJson = BowtieJson.encodeToString(
                ChangePasswordRequest(currentPassword = current, newPassword = new),
            ),
        )
    }

    suspend fun channels(): List<Channel> = withContext(Dispatchers.IO) {
        val body = authed("GET", "/api/v1/channels")
        BowtieJson.decodeFromString(body)
    }

    suspend fun guide(start: Instant, stop: Instant): List<GuideChannel> =
        withContext(Dispatchers.IO) {
            val path = "/api/v1/guide?start=${start}&stop=${stop}"
            val body = authed("GET", path)
            BowtieJson.decodeFromString(body)
        }

    suspend fun createSession(channelId: Long, caps: ClientCaps): CreatedSession =
        withContext(Dispatchers.IO) {
            val body = authed(
                method = "POST",
                path = "/api/v1/sessions",
                bodyJson = BowtieJson.encodeToString(
                    CreateSessionRequest(channelId = channelId, caps = caps),
                ),
            )
            BowtieJson.decodeFromString(body)
        }

    /** Best-effort delete; swallows all errors. */
    suspend fun deleteSession(viewerId: String): Unit = withContext(Dispatchers.IO) {
        try {
            authed("DELETE", "/api/v1/sessions/$viewerId")
        } catch (_: Exception) {
            // swallow
        }
    }

    suspend fun me(): User = withContext(Dispatchers.IO) {
        val body = authed("GET", "/api/v1/me")
        val user = BowtieJson.decodeFromString<User>(body)
        _currentUser.value = user
        user
    }

    // ── Auth / single-flight refresh ────────────────────────────────────────

    private fun applyTokens(pair: TokenPair) {
        accessToken = pair.accessToken
        // Persist new refresh BEFORE any retry can fire.
        store.save(server.toString(), pair.refreshToken)
        _currentUser.value = pair.user
    }

    private fun clearSessionKeepServer() {
        accessToken = null
        _currentUser.value = null
        store.save(store.loadServer(), null)
    }

    /**
     * Coalesce concurrent refresh attempts onto one network call.
     *
     * [failedAccessToken] is the Bearer that got 401. Waiters that acquire the
     * mutex after a successful rotation see a different access token and skip
     * (server refresh tokens are single-use — a second rotate would sign out).
     */
    private suspend fun singleFlightRefresh(failedAccessToken: String?) {
        refreshMutex.withLock {
            // Another coroutine already rotated past the token that failed.
            if (accessToken != null && accessToken != failedAccessToken) {
                return
            }
            // A prior refresh already cleared the session.
            if (store.loadRefreshToken() == null) {
                throw BowtieError.Unauthorized
            }
            try {
                val rt = store.loadRefreshToken()
                    ?: throw BowtieError.Unauthorized
                performRefresh(rt)
            } catch (e: BowtieError.Unauthorized) {
                throw e
            } catch (_: BowtieError) {
                clearSessionKeepServer()
                throw BowtieError.Unauthorized
            } catch (_: Exception) {
                clearSessionKeepServer()
                throw BowtieError.Unauthorized
            }
        }
    }

    private fun performRefresh(refreshToken: String) {
        val request = Request.Builder()
            .url(apiUrl("/api/v1/auth/refresh"))
            .post(jsonBody(BowtieJson.encodeToString(RefreshRequest(refreshToken = refreshToken))))
            .header("Content-Type", JSON_MEDIA)
            .build()
        try {
            okHttp.newCall(request).execute().use { response ->
                val body = response.body?.string().orEmpty()
                if (!response.isSuccessful) {
                    clearSessionKeepServer()
                    throw BowtieError.Unauthorized
                }
                val pair = BowtieJson.decodeFromString<TokenPair>(body)
                // Persist before any waiter retries.
                applyTokens(pair)
            }
        } catch (e: BowtieError) {
            throw e
        } catch (e: Exception) {
            throw BowtieError.Network(e)
        }
    }

    // ── HTTP helpers ────────────────────────────────────────────────────────

    private fun postUnauthed(path: String, bodyJson: String): TokenPair {
        val request = Request.Builder()
            .url(apiUrl(path))
            .post(jsonBody(bodyJson))
            .header("Content-Type", JSON_MEDIA)
            .build()
        try {
            okHttp.newCall(request).execute().use { response ->
                val body = response.body?.string().orEmpty()
                if (!response.isSuccessful) {
                    throw mapHttpError(response.code, body)
                }
                return BowtieJson.decodeFromString(body)
            }
        } catch (e: BowtieError) {
            throw e
        } catch (e: Exception) {
            throw BowtieError.Network(e)
        }
    }

    /**
     * Authenticated request. On 401: single-flight refresh then one retry.
     * Never attaches Bearer to `/api/v1/stream/` paths.
     */
    private suspend fun authed(
        method: String,
        path: String,
        bodyJson: String? = null,
        retryOn401: Boolean = true,
    ): String {
        val attachAuth = !isStreamPath(path)

        fun build(token: String?): Request {
            val b = Request.Builder().url(apiUrl(path))
            when (method) {
                "GET" -> b.get()
                "DELETE" -> b.delete()
                "POST" -> {
                    val rb = (bodyJson ?: "").toRequestBody(JSON_MEDIA_TYPE)
                    b.post(rb)
                    b.header("Content-Type", JSON_MEDIA)
                }
                else -> error("unsupported method $method")
            }
            if (attachAuth && token != null) {
                b.header("Authorization", "Bearer $token")
            }
            return b.build()
        }

        try {
            // Snapshot the token actually sent — concurrent refresh may rotate
            // accessToken before we process a late 401 for the old token.
            val tokenUsed = accessToken
            val first = okHttp.newCall(build(tokenUsed)).execute()
            try {
                if (first.code == 401 && retryOn401 && attachAuth) {
                    first.close()
                    singleFlightRefresh(failedAccessToken = tokenUsed)
                    okHttp.newCall(build(accessToken)).execute().use { retry ->
                        if (retry.code == 401) {
                            clearSessionKeepServer()
                            throw BowtieError.Unauthorized
                        }
                        return handleBody(retry)
                    }
                }
                return handleBody(first)
            } finally {
                first.close()
            }
        } catch (e: BowtieError) {
            throw e
        } catch (e: Exception) {
            throw BowtieError.Network(e)
        }
    }

    private fun handleBody(response: Response): String {
        val body = response.body?.string().orEmpty()
        if (response.isSuccessful) {
            return body
        }
        throw mapHttpError(response.code, body)
    }

    private fun mapHttpError(code: Int, body: String): BowtieError {
        return when (code) {
            401 -> BowtieError.Unauthorized
            404 -> BowtieError.NotFound
            422 -> BowtieError.NegotiationFailed(
                extractErrorMessage(body) ?: "negotiation failed",
            )
            503 -> {
                try {
                    val payload = BowtieJson.decodeFromString<TunersBusyPayload>(body)
                    BowtieError.TunersBusy(payload.sessions)
                } catch (_: Exception) {
                    BowtieError.Server(503, extractErrorMessage(body) ?: body)
                }
            }
            else -> BowtieError.Server(
                code,
                extractErrorMessage(body) ?: body.ifEmpty { "HTTP $code" },
            )
        }
    }

    private fun extractErrorMessage(body: String): String? {
        if (body.isBlank()) return null
        return try {
            BowtieJson.decodeFromString<ErrorBody>(body).error
        } catch (_: Exception) {
            null
        }
    }

    private fun apiUrl(path: String): HttpUrl {
        val qIndex = path.indexOf('?')
        return if (qIndex < 0) {
            ServerUrl.resolve(path, server)
        } else {
            val pathOnly = path.substring(0, qIndex)
            val query = path.substring(qIndex + 1)
            val base = ServerUrl.resolve(pathOnly, server)
            base.newBuilder().encodedQuery(query).build()
        }
    }

    private fun jsonBody(json: String) = json.toRequestBody(JSON_MEDIA_TYPE)

    companion object {
        private const val JSON_MEDIA = "application/json"
        private val JSON_MEDIA_TYPE = JSON_MEDIA.toMediaType()

        fun isStreamPath(path: String): Boolean =
            path.contains("/api/v1/stream/")
    }
}

// ── Wire request / error envelopes (OpenAPI field names) ────────────────────

@Serializable
private data class LoginRequest(val username: String, val password: String)

@Serializable
private data class RefreshRequest(val refreshToken: String)

@Serializable
private data class ChangePasswordRequest(
    val currentPassword: String,
    val newPassword: String,
)

@Serializable
private data class CreateSessionRequest(
    val channelId: Long,
    val caps: ClientCaps,
)

@Serializable
private data class ErrorBody(val error: String)

@Serializable
private data class TunersBusyPayload(
    val error: String,
    val sessions: List<ActiveSessionSummary> = emptyList(),
)
