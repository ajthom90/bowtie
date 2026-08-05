package app.bowtie.core

import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import okhttp3.HttpUrl
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull
import okhttp3.OkHttpClient
import okhttp3.Request
import java.util.concurrent.TimeUnit

/**
 * Server base-URL helpers: normalize user input, probe /healthz, resolve relative
 * paths (preserving query — required for HLS `?token=`).
 */
object ServerUrl {

    /**
     * Normalize a user-entered server string into an [HttpUrl].
     * - Default scheme is `http` when missing
     * - Trailing `/` is stripped from the path (root stays `/` in OkHttp form)
     * - Empty / unparseable input → null
     */
    fun normalize(raw: String): HttpUrl? {
        val trimmed = raw.trim()
        if (trimmed.isEmpty()) return null

        val withScheme = if (trimmed.contains("://")) {
            trimmed
        } else {
            "http://$trimmed"
        }

        val parsed = withScheme.toHttpUrlOrNull() ?: return null
        if (parsed.host.isEmpty()) return null

        val strippedPath = parsed.encodedPath.trimEnd('/')
        return parsed.newBuilder()
            .encodedPath(if (strippedPath.isEmpty()) "/" else strippedPath)
            .build()
    }

    /**
     * GET `{url}/healthz` — returns true only on HTTP 200 within [timeoutMs].
     */
    suspend fun validate(
        url: HttpUrl,
        client: OkHttpClient,
        timeoutMs: Long = 2000,
    ): Boolean = withContext(Dispatchers.IO) {
        val health = url.resolve("/healthz") ?: return@withContext false
        val request = Request.Builder().url(health).get().build()
        val timed = client.newBuilder()
            .callTimeout(timeoutMs, TimeUnit.MILLISECONDS)
            .connectTimeout(timeoutMs, TimeUnit.MILLISECONDS)
            .readTimeout(timeoutMs, TimeUnit.MILLISECONDS)
            .build()
        try {
            timed.newCall(request).execute().use { response ->
                response.code == 200
            }
        } catch (_: Exception) {
            false
        }
    }

    /**
     * Resolve [path] against [base], preserving any query string on [path]
     * (critical for HLS playlist URLs carrying `?token=`).
     */
    fun resolve(path: String, base: HttpUrl): HttpUrl {
        return base.resolve(path)
            ?: error("Failed to resolve path '$path' against $base")
    }
}
