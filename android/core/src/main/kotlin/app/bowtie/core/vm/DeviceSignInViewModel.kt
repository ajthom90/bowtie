package app.bowtie.core.vm

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import app.bowtie.core.BowtieClient
import app.bowtie.core.BowtieError
import app.bowtie.core.DevicePoll
import app.bowtie.core.DeviceSignIn
import app.bowtie.core.User
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch

/**
 * "Sign in with your phone" for Android TV / Fire TV.
 *
 * [start] asks the server for a code ([State.Waiting]: the code, the /link
 * address and a QR PNG in [qrPng]), then polls every `interval` seconds:
 * pending keeps waiting, approval ends in [State.SignedIn] (the client holds
 * the session, as after a password sign-in), and 410 — or outliving
 * `expiresIn` — ends in [State.Expired], where [start] gets a new code.
 * A failed poll (network, 5xx) just waits for the next one.
 */
class DeviceSignInViewModel(
    private val client: BowtieClient,
    private val deviceName: String,
    scope: CoroutineScope? = null,
    /** Waits between polls. Production: [delay]; tests record the interval. */
    private val sleeper: suspend (Long) -> Unit = { ms -> delay(ms) },
) : ViewModel() {

    sealed class State {
        data object Idle : State()
        data object Starting : State()

        /** Show [userCode] and the QR; "Or go to [link] and enter [userCode]". */
        data class Waiting(val userCode: String, val link: String) : State()
        data object Expired : State()
        data class Failed(val message: String) : State()
        data class SignedIn(val user: User) : State()
    }

    private val workScope: CoroutineScope by lazy { scope ?: viewModelScope }

    private val _state = MutableStateFlow<State>(State.Idle)
    val state: StateFlow<State> = _state.asStateFlow()

    private val _qrPng = MutableStateFlow<ByteArray?>(null)

    /** The current code's QR image (PNG bytes); null until loaded or if it can't be. */
    val qrPng: StateFlow<ByteArray?> = _qrPng.asStateFlow()

    private var job: Job? = null

    /** Get a (new) code and wait for a phone to approve it. */
    fun start() {
        job?.cancel()
        _state.value = State.Starting
        _qrPng.value = null
        job = workScope.launch { run() }
    }

    /** Stop polling (leaving the screen or switching to password sign-in). */
    fun stop() {
        job?.cancel()
        job = null
        if (_state.value !is State.SignedIn) _state.value = State.Idle
    }

    private suspend fun run() {
        val code = try {
            client.startDeviceSignIn(deviceName)
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            _state.value = State.Failed(startErrorMessage(e))
            return
        }
        _state.value = State.Waiting(userCode = code.userCode, link = linkText(code.verifyUrl))
        // Children of this job: stop() cancels the picture load too.
        coroutineScope {
            launch { loadQr(code) }
            poll(code)
        }
    }

    private suspend fun loadQr(code: DeviceSignIn) {
        if (code.qrUrl.isEmpty()) return
        try {
            val bytes = client.deviceQrPng(code.qrUrl)
            // Only for the code still shown.
            if ((_state.value as? State.Waiting)?.userCode == code.userCode) _qrPng.value = bytes
        } catch (e: CancellationException) {
            throw e
        } catch (_: Exception) {
            // The typed code still works without the picture.
        }
    }

    private suspend fun poll(code: DeviceSignIn) {
        val intervalSec = code.interval.takeIf { it > 0 } ?: DEFAULT_INTERVAL_SEC
        var elapsedSec = 0
        while (true) {
            sleeper(intervalSec * 1000L)
            elapsedSec += intervalSec
            if (code.expiresIn in 1..elapsedSec) {
                _state.value = State.Expired
                return
            }
            val result = try {
                client.pollDeviceSignIn(code.deviceCode)
            } catch (e: CancellationException) {
                throw e
            } catch (_: Exception) {
                continue // transient: try again next interval
            }
            when (result) {
                DevicePoll.Pending -> Unit
                DevicePoll.Expired -> {
                    _state.value = State.Expired
                    return
                }
                is DevicePoll.SignedIn -> {
                    _state.value = State.SignedIn(result.user)
                    return
                }
            }
        }
    }

    override fun onCleared() {
        job?.cancel()
        super.onCleared()
    }

    companion object {
        /** Poll interval when the server doesn't give one. */
        const val DEFAULT_INTERVAL_SEC = 5

        /** "192.168.1.5:8080/link" from a verify URL: no scheme, no query. */
        fun linkText(verifyUrl: String): String =
            verifyUrl.substringBefore('?').substringBefore('#')
                .removePrefix("https://").removePrefix("http://")
                .trimEnd('/')

        /**
         * What the approver sees asking to sign in. Fire TV models are codes
         * ("AFTMM"), so Amazon devices say "Fire TV".
         */
        fun deviceName(manufacturer: String, model: String): String = when {
            manufacturer.equals("Amazon", ignoreCase = true) -> "Fire TV"
            model.isNotBlank() -> model.trim()
            else -> "Android TV"
        }

        private fun startErrorMessage(e: Throwable): String = when (e) {
            is BowtieError.Network -> "Couldn't reach the server."
            is BowtieError.NotFound -> "This server doesn't support signing in with a phone. Use your password."
            is BowtieError.Server -> e.message.ifBlank { "Couldn't get a code. Try again." }
            else -> e.message ?: "Couldn't get a code. Try again."
        }
    }
}
