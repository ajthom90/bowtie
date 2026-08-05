package app.bowtie.core

import android.content.Context
import android.content.SharedPreferences
import androidx.security.crypto.EncryptedSharedPreferences
import androidx.security.crypto.MasterKey

/**
 * Persists the server base URL and refresh token across process death.
 * Access tokens are memory-only (see [BowtieClient]).
 *
 * [save] treats `null` as clear for that field (independent of the other).
 */
interface TokenStore {
    fun loadServer(): String?
    fun loadRefreshToken(): String?
    fun save(server: String?, refreshToken: String?)
}

/** In-memory store for tests and non-Android hosts. */
class InMemoryTokenStore : TokenStore {
    private var server: String? = null
    private var refreshToken: String? = null

    @Synchronized
    override fun loadServer(): String? = server

    @Synchronized
    override fun loadRefreshToken(): String? = refreshToken

    @Synchronized
    override fun save(server: String?, refreshToken: String?) {
        this.server = server
        this.refreshToken = refreshToken
    }
}

/**
 * [EncryptedSharedPreferences] backed store (AES256 via AndroidKeyStore).
 * Thin wrapper — not unit-tested under Robolectric (KeyStore footgun).
 */
class EncryptedTokenStore(context: Context) : TokenStore {

    private val prefs: SharedPreferences

    init {
        val masterKey = MasterKey.Builder(context.applicationContext)
            .setKeyScheme(MasterKey.KeyScheme.AES256_GCM)
            .build()
        prefs = EncryptedSharedPreferences.create(
            context.applicationContext,
            PREFS_NAME,
            masterKey,
            EncryptedSharedPreferences.PrefKeyEncryptionScheme.AES256_SIV,
            EncryptedSharedPreferences.PrefValueEncryptionScheme.AES256_GCM,
        )
    }

    override fun loadServer(): String? = prefs.getString(KEY_SERVER, null)

    override fun loadRefreshToken(): String? = prefs.getString(KEY_REFRESH, null)

    override fun save(server: String?, refreshToken: String?) {
        prefs.edit().apply {
            if (server == null) remove(KEY_SERVER) else putString(KEY_SERVER, server)
            if (refreshToken == null) remove(KEY_REFRESH) else putString(KEY_REFRESH, refreshToken)
            apply()
        }
    }

    companion object {
        private const val PREFS_NAME = "bowtie_token_store"
        private const val KEY_SERVER = "server"
        private const val KEY_REFRESH = "refresh_token"
    }
}
