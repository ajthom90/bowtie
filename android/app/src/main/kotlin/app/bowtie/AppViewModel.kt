package app.bowtie

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import app.bowtie.core.BowtieClient
import app.bowtie.core.ServerUrl
import app.bowtie.core.TokenStore
import app.bowtie.core.User
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import okhttp3.HttpUrl
import okhttp3.OkHttpClient

/**
 * Auth / connection state machine and DI root for the viewer app.
 *
 * Phase transitions:
 * - fresh store → [Phase.Connect]
 * - stored server, no token → [Phase.Login]
 * - stored server + token → [Phase.Checking] → [start] → [Phase.Ready] | [Phase.Login]
 */
class AppViewModel(
    private val store: TokenStore,
    private val clientFactory: (HttpUrl) -> BowtieClient,
    private val scope: CoroutineScope? = null,
    private val healthClient: OkHttpClient = OkHttpClient(),
) : ViewModel() {

    sealed class Phase {
        data object Connect : Phase()
        data object Login : Phase()
        data object Checking : Phase()
        data class Ready(val user: User) : Phase()
    }

    private val workScope: CoroutineScope = scope ?: viewModelScope

    private val _phase = MutableStateFlow<Phase>(Phase.Connect)
    val phase: StateFlow<Phase> = _phase.asStateFlow()

    var client: BowtieClient? = null
        private set

    init {
        val serverStr = store.loadServer()
        if (serverStr != null) {
            val url = ServerUrl.normalize(serverStr)
            if (url != null) {
                client = clientFactory(url)
                _phase.value = if (store.loadRefreshToken() != null) {
                    Phase.Checking
                } else {
                    Phase.Login
                }
            } else {
                _phase.value = Phase.Connect
            }
        } else {
            _phase.value = Phase.Connect
        }
    }

    /**
     * Normalize + healthz-validate [raw], persist server, advance to [Phase.Login].
     * @return false when the URL is invalid or the server is unreachable.
     */
    suspend fun connect(raw: String): Boolean {
        val url = ServerUrl.normalize(raw) ?: return false
        val ok = ServerUrl.validate(url, healthClient)
        if (!ok) return false

        store.save(url.toString(), null)
        client = clientFactory(url)
        _phase.value = Phase.Login
        return true
    }

    /**
     * Bootstrap from a stored refresh token while in [Phase.Checking].
     * Success → [Phase.Ready]; failure → [Phase.Login] (server kept).
     */
    fun start() {
        if (_phase.value !is Phase.Checking) return
        val c = client ?: return
        workScope.launch {
            try {
                val user = c.bootstrapFromStoredToken()
                _phase.value = Phase.Ready(user)
            } catch (_: Exception) {
                _phase.value = Phase.Login
            }
        }
    }

    suspend fun signIn(username: String, password: String) {
        val c = client ?: error("No client — connect to a server first")
        val user = c.login(username, password)
        _phase.value = Phase.Ready(user)
    }

    /** Sign out → [Phase.Login], keeping the server URL for reconnect. */
    suspend fun signOut() {
        client?.logout()
        _phase.value = Phase.Login
    }

    /** Clears server + tokens and returns to [Phase.Connect]. */
    fun changeServer() {
        store.save(null, null)
        client = null
        _phase.value = Phase.Connect
    }
}
