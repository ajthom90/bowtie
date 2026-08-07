package app.bowtie.core.vm

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import app.bowtie.core.ActiveSessionSummary
import app.bowtie.core.BowtieClient
import app.bowtie.core.BowtieError
import app.bowtie.core.Channel
import app.bowtie.core.ClientCaps
import app.bowtie.core.CreatedSession
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch

/**
 * Session-replace playback state machine.
 *
 * CONTRACT:
 * - Every [createSession] sends `effectiveCaps` = base [caps] with
 *   `profile = selectedProfile` (`""` = Auto).
 * - On 422: reset [selectedProfile] to `""` and retry ONCE; a second 422
 *   → [State.Failed] with the device-can't-play copy.
 * - On 404: [State.Failed] + [channelsStale] = true (channel list should refresh).
 * - Mid-play 403 via [onPlaybackAuthError]: one silent replace, then fail.
 * - [stop] is for real leave only (dismissal, sign-out, change-server) — never PiP.
 * - Zap / quality: cancel in-flight → DELETE old → debounce → POST new.
 * - Heartbeats (spec C / A6): 15s while session is open (Playing OR Stalled);
 *   stream-token auth; stop only on real leave.
 */
class PlayerViewModel(
    private val client: BowtieClient,
    private val caps: ClientCaps,
    private val debounceMs: Long = 400,
    private val scope: CoroutineScope? = null,
    private val heartbeatIntervalMs: Long = HEARTBEAT_INTERVAL_MS,
    /**
     * Suspends for the heartbeat interval. Production: [delay].
     * Tests that need virtual-time beats inject `delay`; other tests inject a
     * sleeper that never schedules so `advanceUntilIdle` stays finite.
     */
    private val heartbeatSleeper: suspend (Long) -> Unit = { ms -> delay(ms) },
) : ViewModel() {

    sealed class State {
        data object Idle : State()
        data object Starting : State()
        data class Playing(val s: CreatedSession) : State()
        data object Stalled : State()
        data class Failed(val message: String) : State()
        data class TunersBusy(val sessions: List<ActiveSessionSummary>) : State()
    }

    companion object {
        /** Spec-mandated copy for double-422 negotiation failure. */
        const val DEVICE_CANT_PLAY_MESSAGE =
            "This device can't play this channel at that quality"

        /** Surface after a second mid-play 403 without a successful silent replace. */
        const val PLAYBACK_AUTH_FAILED_MESSAGE =
            "Playback authorization failed"

        const val CHANNEL_NOT_FOUND_MESSAGE = "Channel not found"

        /** Out-of-window clamp notice (spec B) — exact copy. */
        const val OUT_OF_WINDOW_NOTICE =
            "Jumped to live — paused longer than the buffer"

        /** Client heartbeat interval (spec C). */
        const val HEARTBEAT_INTERVAL_MS = 15_000L

        /** Extract `token` query from a playlist path/URL. */
        fun streamTokenFromPlaylist(playlistUrl: String): String? {
            val q = playlistUrl.indexOf('?')
            if (q < 0) return null
            return playlistUrl.substring(q + 1)
                .split('&')
                .map { part ->
                    val eq = part.indexOf('=')
                    if (eq < 0) part to ""
                    else part.substring(0, eq) to part.substring(eq + 1)
                }
                .firstOrNull { it.first == "token" }
                ?.second
                ?.let {
                    try {
                        java.net.URLDecoder.decode(it, Charsets.UTF_8.name())
                    } catch (_: Exception) {
                        it
                    }
                }
                ?.takeIf { it.isNotEmpty() }
        }
    }

    private val workScope: CoroutineScope = scope ?: viewModelScope

    private val _state = MutableStateFlow<State>(State.Idle)
    val state: StateFlow<State> = _state.asStateFlow()

    private val _currentChannel = MutableStateFlow<Channel?>(null)
    val currentChannel: StateFlow<Channel?> = _currentChannel.asStateFlow()

    /**
     * Becomes true when create fails with 404 (unknown/disabled channel).
     * UI should refresh the channel list when this flips to true; call
     * [clearChannelsStale] after handling.
     */
    private val _channelsStale = MutableStateFlow(false)
    val channelsStale: StateFlow<Boolean> = _channelsStale.asStateFlow()

    /** `""` = Auto. */
    var selectedProfile: String = ""

    private var replaceJob: Job? = null
    private var heartbeatJob: Job? = null
    private var activeViewerId: String? = null
    /** Last successfully created session — used to recover from [State.Stalled]. */
    private var lastSession: CreatedSession? = null
    /** Generation token so cancelled replace tasks never clobber newer state. */
    private var generation: Long = 0
    /** Mid-play 403: one silent replace, then fail. */
    private var authFailureRetried: Boolean = false

    /** Caps sent on every create: base device caps + current profile selection. */
    val effectiveCaps: ClientCaps
        get() = caps.copy(profile = selectedProfile)

    // ── Public API ──────────────────────────────────────────────────────────

    /** Session-replace play: cancel in-flight create, DELETE old, debounce, POST new. */
    fun play(channel: Channel) {
        _currentChannel.value = channel
        authFailureRetried = false
        scheduleReplace()
    }

    /** Quality change: same replace machine, keeps the current channel. */
    fun setProfile(p: String) {
        selectedProfile = p
        if (_currentChannel.value == null) return
        authFailureRetried = false
        scheduleReplace()
    }

    /** Real leave only: DELETE active session and return to idle. */
    fun stop() {
        replaceJob?.cancel()
        replaceJob = null
        stopHeartbeat()
        generation++

        val viewerId = activeViewerId
        activeViewerId = null
        lastSession = null
        _currentChannel.value = null
        authFailureRetried = false

        if (viewerId != null) {
            workScope.launch {
                client.deleteSession(viewerId)
            }
        }
        _state.value = State.Idle
    }

    /**
     * Mid-play playlist/segment 403 handler: one silent session replace, then
     * [State.Failed].
     */
    fun onPlaybackAuthError() {
        val current = _state.value
        if (current !is State.Playing && current !is State.Stalled) return
        if (authFailureRetried) {
            _state.value = State.Failed(PLAYBACK_AUTH_FAILED_MESSAGE)
            return
        }
        authFailureRetried = true
        scheduleReplace()
    }

    /**
     * Media3 behind-live / buffer stall: flip UI to [State.Stalled] while the
     * session stays alive. Caller should seek/retry the player.
     */
    fun onPlaybackStalled() {
        val current = _state.value
        if (current is State.Playing || current is State.Stalled) {
            _state.value = State.Stalled
        }
    }

    /** After a stall recovery (seek + rebuffer), restore [State.Playing]. */
    fun onPlaybackRecovered() {
        val session = lastSession ?: return
        if (_state.value is State.Stalled) {
            _state.value = State.Playing(session)
        }
    }

    /**
     * Exhausted Media3 network retries: surface [State.Failed] without
     * deleting the session so Retry can re-create via [play].
     */
    fun onPlaybackFailed(message: String) {
        _state.value = State.Failed(message)
    }

    fun clearChannelsStale() {
        _channelsStale.value = false
    }

    // ── Replace machine ─────────────────────────────────────────────────────

    private fun scheduleReplace() {
        replaceJob?.cancel()
        generation++
        val gen = generation
        replaceJob = workScope.launch {
            performReplace(gen)
        }
    }

    private suspend fun performReplace(gen: Long) {
        val channel = _currentChannel.value ?: return

        val oldViewerId = activeViewerId
        activeViewerId = null
        stopHeartbeat()
        _state.value = State.Starting

        if (oldViewerId != null) {
            client.deleteSession(oldViewerId)
        }

        if (!isCurrent(gen)) return

        delay(debounceMs)

        if (!isCurrent(gen)) return

        createSession(channel = channel, gen = gen, isRetry = false)
    }

    private suspend fun createSession(
        channel: Channel,
        gen: Long,
        isRetry: Boolean,
    ) {
        try {
            val session = client.createSession(
                channelId = channel.id,
                caps = effectiveCaps,
            )
            if (!isCurrent(gen)) {
                // Orphaned success — tear down so we don't leak a tuner.
                client.deleteSession(session.viewerId)
                return
            }
            activeViewerId = session.viewerId
            lastSession = session
            _channelsStale.value = false
            _state.value = State.Playing(session)
            startHeartbeat(viewerId = session.viewerId, playlistUrl = session.playlistUrl)
        } catch (e: CancellationException) {
            throw e
        } catch (e: BowtieError) {
            if (!isCurrent(gen)) return
            handleCreateError(e, channel, gen, isRetry)
        } catch (e: Exception) {
            if (!isCurrent(gen)) return
            _state.value = State.Failed(e.message ?: e.toString())
        }
    }

    // ── Heartbeats (A6: session-open, continues through Stalled) ────────────

    private fun startHeartbeat(viewerId: String, playlistUrl: String) {
        stopHeartbeat()
        val token = streamTokenFromPlaylist(playlistUrl) ?: return
        // Detach from parent Job so kotlinx `runTest` / `advanceUntilIdle` are not
        // kept alive by the open-ended beat loop; still uses workScope's dispatcher.
        // Cancelled via stopHeartbeat / onCleared / stop().
        val job = Job()
        heartbeatJob = job
        CoroutineScope(workScope.coroutineContext.minusKey(Job) + job).launch {
            while (true) {
                heartbeatSleeper(heartbeatIntervalMs)
                // A6: continue while this viewer is still active (Playing or Stalled).
                if (activeViewerId != viewerId) return@launch
                try {
                    client.heartbeat(viewerId, token)
                } catch (_: Exception) {
                    // best-effort
                }
            }
        }
    }

    private fun stopHeartbeat() {
        heartbeatJob?.cancel()
        heartbeatJob = null
    }

    override fun onCleared() {
        stopHeartbeat()
        replaceJob?.cancel()
        super.onCleared()
    }

    private suspend fun handleCreateError(
        error: BowtieError,
        channel: Channel,
        gen: Long,
        isRetry: Boolean,
    ) {
        when (error) {
            is BowtieError.NegotiationFailed -> {
                // 422: force Auto and retry once; second 422 → device-can't-play.
                selectedProfile = ""
                if (!isRetry) {
                    createSession(channel = channel, gen = gen, isRetry = true)
                } else {
                    _state.value = State.Failed(DEVICE_CANT_PLAY_MESSAGE)
                }
            }
            is BowtieError.TunersBusy -> {
                _state.value = State.TunersBusy(error.sessions)
            }
            is BowtieError.NotFound -> {
                _channelsStale.value = true
                _state.value = State.Failed(CHANNEL_NOT_FOUND_MESSAGE)
            }
            is BowtieError.Unauthorized -> {
                _state.value = State.Failed("Signed out")
            }
            is BowtieError.Server -> {
                _state.value = State.Failed(error.message)
            }
            is BowtieError.Network -> {
                _state.value = State.Failed(
                    error.cause2.message ?: error.cause2.toString(),
                )
            }
        }
    }

    private fun isCurrent(gen: Long): Boolean = gen == generation
}
