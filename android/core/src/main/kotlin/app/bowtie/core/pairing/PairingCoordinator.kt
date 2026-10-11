package app.bowtie.core.pairing

import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.launch

/** Runs a [PairingServer] for the Connect screen and exposes the URL the TV shows as a QR code. */
class PairingCoordinator(
    private val scope: CoroutineScope,
    private val connect: suspend (String) -> Boolean,
    private val host: () -> String? = LanAddress::ipv4,
    private val lifetimeMs: Long = 15 * 60 * 1000L,
) {
    private val _pairUrl = MutableStateFlow<String?>(null)
    val pairUrl: StateFlow<String?> = _pairUrl

    private var session: PairingSession? = null
    private var server: PairingServer? = null
    private var expiry: Job? = null
    private var submitted: String? = null

    /** No-op while a live session is running; after [stop] or a lockout it starts a fresh session and token. */
    @Synchronized
    fun start() {
        if (session?.state?.let { it != PairingSession.State.Closed } == true) return
        stopLocked()
        val ip = host() ?: return
        val fresh = PairingSession()
        val srv = PairingServer(fresh, onClosed = { closed(fresh) }) { address ->
            synchronized(this) { if (session === fresh) submitted = address }
            scope.launch { if (!connect(address)) fresh.connectFailed(address) }
        }
        val port = srv.start(scope) ?: return
        session = fresh
        server = srv
        _pairUrl.value = "http://$ip:$port/p?t=${fresh.token}"
        expiry = scope.launch {
            delay(lifetimeMs)
            stop()
        }
    }

    /** The TV obtained a sign-in code from [server]; only the address the phone submitted gets a redirect. */
    @Synchronized
    fun codeIssued(server: String, userCode: String) {
        if (server != submitted) return
        session?.codeIssued(PairingSession.linkUrl(server, userCode))
    }

    @Synchronized
    fun stop() = stopLocked()

    private fun stopLocked() {
        expiry?.cancel()
        expiry = null
        server?.stop()
        server = null
        session?.close()
        session = null
        submitted = null
        _pairUrl.value = null
    }

    @Synchronized
    private fun closed(which: PairingSession) {
        if (session === which) stopLocked()
    }
}
