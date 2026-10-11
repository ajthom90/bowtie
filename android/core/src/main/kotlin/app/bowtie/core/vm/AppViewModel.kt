package app.bowtie.core.vm

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import app.bowtie.core.BowtieClient
import app.bowtie.core.ServerUrl
import app.bowtie.core.TokenStore
import app.bowtie.core.User
import app.bowtie.core.pairing.LanAddress
import app.bowtie.core.pairing.PairingCoordinator
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
    private val pairingHost: () -> String? = LanAddress::ipv4,
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

    /** TV "set up from your phone" listener; lazy so the phone app never builds one. */
    private val pairingLazy = lazy { PairingCoordinator(workScope, connect = ::connectFromPairing, host = pairingHost) }
    val pairing: PairingCoordinator by pairingLazy

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
    suspend fun connect(raw: String): Boolean = connect(raw, onlyFromConnect = false)

    /** A phone submitted [raw]; a slow check must not overwrite a server typed or signed in meanwhile. */
    internal suspend fun connectFromPairing(raw: String): Boolean = connect(raw, onlyFromConnect = true)

    private suspend fun connect(raw: String, onlyFromConnect: Boolean): Boolean {
        if (onlyFromConnect && _phase.value !is Phase.Connect) return false
        val url = ServerUrl.normalize(raw) ?: return false
        val ok = ServerUrl.validate(url, healthClient)
        if (!ok) return false
        if (onlyFromConnect && _phase.value !is Phase.Connect) return false

        store.save(url.toString(), null)
        client = clientFactory(url)
        _phase.value = Phase.Login
        return true
    }

    /** The user typed an address on the TV: once it connects, that supersedes any phone pairing (a typo keeps the QR). */
    suspend fun connectTyped(raw: String): Boolean {
        val ok = connect(raw)
        if (ok) stopPairing()
        return ok
    }

    private fun stopPairing() {
        if (pairingLazy.isInitialized()) pairing.stop()
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
                stopPairing()
                _phase.value = Phase.Ready(user)
            } catch (_: Exception) {
                _phase.value = Phase.Login
            }
        }
    }

    suspend fun signIn(username: String, password: String) {
        val c = client ?: error("No client — connect to a server first")
        val user = c.login(username, password)
        stopPairing()
        _phase.value = Phase.Ready(user)
    }

    /**
     * The client already signed in another way (quick sign-in from a phone):
     * go to [Phase.Ready] as [signIn] would.
     */
    fun completeSignIn(user: User) {
        if (client == null) return
        stopPairing()
        _phase.value = Phase.Ready(user)
    }

    /** Sign out → [Phase.Login], keeping the server URL for reconnect. */
    suspend fun signOut() {
        client?.logout()
        _phase.value = Phase.Login
    }

    /** Clears server + tokens and returns to [Phase.Connect], where pairing starts over with a new code. */
    fun changeServer() {
        stopPairing()
        store.save(null, null)
        client = null
        _phase.value = Phase.Connect
    }

    override fun onCleared() {
        // Cancelling viewModelScope can't unblock the listener's accept(); close it explicitly.
        stopPairing()
    }
}
