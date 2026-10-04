package app.bowtie.core

import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.suspendCancellableCoroutine
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import kotlinx.serialization.Serializable
import kotlinx.serialization.encodeToString
import okhttp3.Call
import okhttp3.Callback
import okhttp3.HttpUrl
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import okhttp3.Response
import java.io.IOException
import java.time.Instant
import kotlin.coroutines.resume
import kotlin.coroutines.resumeWithException

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

    /**
     * Star ([on]) or unstar a channel for the signed-in user (PUT / DELETE, 204, idempotent).
     * Throws [BowtieError.NotFound] when the channel is unknown or disabled.
     */
    suspend fun setFavorite(channelId: Long, on: Boolean): Unit = withContext(Dispatchers.IO) {
        authed(if (on) "PUT" else "DELETE", "/api/v1/me/favorites/$channelId")
        Unit
    }

    /**
     * Channels the user watched recently, newest first (server caps [limit] at 20).
     * A server without favorites/recents answers [BowtieError.NotFound].
     */
    suspend fun recents(limit: Int = 8): List<RecentChannel> = withContext(Dispatchers.IO) {
        val body = authed("GET", "/api/v1/me/recents?limit=$limit")
        BowtieJson.decodeFromString(body)
    }

    /** Clear the user's watch history (204). */
    suspend fun clearRecents(): Unit = withContext(Dispatchers.IO) {
        authed("DELETE", "/api/v1/me/recents")
        Unit
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

    /**
     * Session liveness beat (spec C). Auth is the stream token query param only —
     * never Bearer (avoids racing access-token refresh mid-session). Best-effort.
     */
    suspend fun heartbeat(viewerId: String, token: String): Unit = withContext(Dispatchers.IO) {
        try {
            val path = "/api/v1/sessions/$viewerId/heartbeat?token=${java.net.URLEncoder.encode(token, Charsets.UTF_8.name())}"
            val request = Request.Builder()
                .url(apiUrl(path))
                .post("".toRequestBody(null))
                .build()
            okHttp.newCall(request).execute().use { response ->
                // 204 success; all other statuses swallowed (best-effort).
                response.body?.close()
            }
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

    // ── DVR recordings ──────────────────────────────────────────────────────

    /** Everyone's recordings; [tab] filters upcoming / recorded / missed (null = all). */
    suspend fun recordings(tab: RecordingLogic.Tab? = null): List<Recording> =
        withContext(Dispatchers.IO) {
            val path = if (tab == null) "/api/v1/recordings" else "/api/v1/recordings?state=${tab.query}"
            BowtieJson.decodeFromString(authed("GET", path))
        }

    /**
     * Record the guide program on [channelId] starting exactly at [programStart].
     * Throws [BowtieError.RecordingConflict] (409) when the tuners are booked,
     * unless [force].
     */
    suspend fun scheduleRecording(
        channelId: Long,
        programStart: Instant,
        force: Boolean = false,
    ): ScheduledRecording = withContext(Dispatchers.IO) {
        val body = authed(
            method = "POST",
            path = "/api/v1/recordings",
            bodyJson = BowtieJson.encodeToString(
                ScheduleRecordingRequest(channelId = channelId, programStart = programStart, force = force),
            ),
        )
        BowtieJson.decodeFromString(body)
    }

    /** Cancel a scheduled recording, or delete a recording and its files. */
    suspend fun deleteRecording(id: Long): Unit = withContext(Dispatchers.IO) {
        authed("DELETE", "/api/v1/recordings/$id")
    }

    /** Stop a recording now, keeping what was recorded. */
    suspend fun stopRecording(id: Long): Unit = withContext(Dispatchers.IO) {
        authed("POST", "/api/v1/recordings/$id/stop")
    }

    /** Server-relative, token-signed VOD playlist plus the caller's resume position. */
    suspend fun playRecording(id: Long): RecordingPlayback = withContext(Dispatchers.IO) {
        BowtieJson.decodeFromString(authed("POST", "/api/v1/recordings/$id/play"))
    }

    suspend fun saveRecordingPosition(id: Long, positionSec: Int): Unit = withContext(Dispatchers.IO) {
        authed(
            method = "PUT",
            path = "/api/v1/recordings/$id/position",
            bodyJson = BowtieJson.encodeToString(PositionRequest(positionSec = positionSec)),
        )
    }

    /** Keep (protect) a recording from automatic deletion, or stop keeping it. */
    suspend fun setRecordingProtected(id: Long, protected: Boolean): Recording =
        withContext(Dispatchers.IO) {
            val body = authed(
                method = "PATCH",
                path = "/api/v1/recordings/$id",
                bodyJson = BowtieJson.encodeToString(ProtectRequest(isProtected = protected)),
            )
            BowtieJson.decodeFromString(body)
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
                "POST", "PUT", "PATCH" -> {
                    val rb = (bodyJson ?: "").toRequestBody(JSON_MEDIA_TYPE)
                    b.method(method, rb)
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
            val first = okHttp.newCall(build(tokenUsed)).await()
            try {
                if (first.code == 401 && retryOn401 && attachAuth) {
                    first.close()
                    singleFlightRefresh(failedAccessToken = tokenUsed)
                    okHttp.newCall(build(accessToken)).await().use { retry ->
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
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            throw BowtieError.Network(e)
        }
    }

    /**
     * Runs the call, cancelling it (and closing its connection) when the
     * coroutine is cancelled. A blocking execute() would keep the request
     * alive after a zap, so the server would finish a start nobody wants —
     * an orphan viewer that counts against the account's stream limit.
     */
    private suspend fun Call.await(): Response = suspendCancellableCoroutine { cont ->
        cont.invokeOnCancellation { cancel() }
        enqueue(
            object : Callback {
                override fun onResponse(call: Call, response: Response) {
                    cont.resume(response) { _, value, _ -> value.close() }
                }

                override fun onFailure(call: Call, e: IOException) {
                    if (cont.isActive) cont.resumeWithException(e)
                }
            },
        )
    }

    private fun handleBody(response: Response): String {
        val body = response.body?.string().orEmpty()
        if (response.isSuccessful) {
            return body
        }
        throw mapHttpError(response.code, body, response.request.url.encodedPath)
    }

    private fun mapHttpError(code: Int, body: String, path: String = ""): BowtieError {
        return when (code) {
            401 -> BowtieError.Unauthorized
            404 -> BowtieError.NotFound
            409 -> {
                // Only a schedule conflict carries `conflicts`; /play's 409 is a plain error.
                try {
                    val payload = BowtieJson.decodeFromString<RecordingConflictPayload>(body)
                    BowtieError.RecordingConflict(payload.error, payload.tunerCount, payload.conflicts)
                } catch (_: Exception) {
                    BowtieError.Server(409, extractErrorMessage(body) ?: body.ifEmpty { "HTTP 409" })
                }
            }
            422 -> BowtieError.NegotiationFailed(
                extractErrorMessage(body) ?: "negotiation failed",
            )
            503 -> {
                // A recordings 503 means the DVR is off, not that tuners are busy.
                if (path.startsWith("/api/v1/recordings")) {
                    return BowtieError.Server(503, extractErrorMessage(body) ?: "HTTP 503")
                }
                try {
                    val payload = BowtieJson.decodeFromString<TunersBusyPayload>(body)
                    BowtieError.TunersBusy(payload.sessions, payload.otherInUse)
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

/** `force` is optional: false is omitted (BowtieJson has encodeDefaults = false). */
@Serializable
private data class ScheduleRecordingRequest(
    val channelId: Long,
    @Serializable(with = InstantIso8601Serializer::class)
    val programStart: Instant,
    val force: Boolean = false,
)

/** No default: positionSec 0 (start over) must be sent. */
@Serializable
private data class PositionRequest(val positionSec: Int)

/** No default: protected false (stop keeping) must be sent. */
@Serializable
private data class ProtectRequest(
    @kotlinx.serialization.SerialName("protected")
    val isProtected: Boolean,
)

/** 409 from POST /recordings; `conflicts` is required so /play's plain 409 doesn't match. */
@Serializable
private data class RecordingConflictPayload(
    val error: String,
    val tunerCount: Int,
    val conflicts: List<Recording>,
)

@Serializable
private data class TunersBusyPayload(
    val error: String,
    val sessions: List<ActiveSessionSummary> = emptyList(),
    val otherInUse: Int = 0,
)
