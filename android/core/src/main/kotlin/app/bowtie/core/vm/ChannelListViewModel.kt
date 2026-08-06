package app.bowtie.core.vm

import androidx.lifecycle.ViewModel
import app.bowtie.core.BowtieClient
import app.bowtie.core.BowtieError
import app.bowtie.core.Channel
import app.bowtie.core.GuideLogic
import app.bowtie.core.GuideProgram
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import java.time.Duration
import java.time.Instant

/**
 * Loads channels and joins each row with guide now/next for a 4-hour window.
 * Mirrors iOS [ChannelListModel].
 *
 * Refresh policy (driven by UI):
 * - full [refresh] on first show / pull-to-refresh / [channelsStale]
 * - [refreshIfStale] on ON_START and every 5 minutes while STARTED
 */
class ChannelListViewModel(
    private val client: BowtieClient,
    private val now: () -> Instant = { Instant.now() },
) : ViewModel() {

    data class Row(
        val channel: Channel,
        val nowNext: GuideLogic.NowNext,
    ) {
        val id: Long get() = channel.id
    }

    sealed class LoadState {
        data object Loading : LoadState()
        data class Loaded(val rows: List<Row>) : LoadState()
        data class Failed(val message: String) : LoadState()
        data object Empty : LoadState()
    }

    private val _state = MutableStateFlow<LoadState>(LoadState.Loading)
    val state: StateFlow<LoadState> = _state.asStateFlow()

    /** Wall-clock of last **successful** load (empty or loaded). Null until first success. */
    private var lastLoadedAt: Instant? = null

    /**
     * Fetches channels + guide(now..now+4h) and joins via [GuideLogic.nowNext].
     */
    suspend fun refresh() {
        _state.value = LoadState.Loading
        val at = now()
        val stop = at.plus(GUIDE_WINDOW)

        try {
            val channels = client.channels()
            val guide = client.guide(start = at, stop = stop)

            if (channels.isEmpty()) {
                _state.value = LoadState.Empty
                lastLoadedAt = at
                return
            }

            val byId = guide.associateBy { it.channelId }
            val rows = channels.map { channel ->
                val programs = byId[channel.id]?.programs.orEmpty()
                Row(
                    channel = channel,
                    nowNext = GuideLogic.nowNext(programs = programs, at = at),
                )
            }
            _state.value = LoadState.Loaded(rows)
            lastLoadedAt = at
        } catch (e: Exception) {
            _state.value = LoadState.Failed(messageFor(e))
        }
    }

    /**
     * Reloads when never loaded, or when the last successful load is ≥ 5 minutes old.
     * Called on foreground (ON_START) and by a 5-minute timer while STARTED.
     */
    suspend fun refreshIfStale() {
        val last = lastLoadedAt
        if (last == null) {
            refresh()
            return
        }
        if (Duration.between(last, now()) >= STALE_INTERVAL) {
            refresh()
        }
    }

    companion object {
        /** Guide request window length: now … now+4h. */
        val GUIDE_WINDOW: Duration = Duration.ofHours(4)

        /** Freshness window matching the 5-minute auto-refresh timer. */
        val STALE_INTERVAL: Duration = Duration.ofMinutes(5)

        /**
         * Progress through the current program in `[0, 1]`.
         * Returns 0 when there is no current program or the window is invalid.
         */
        fun programProgress(program: GuideProgram?, at: Instant = Instant.now()): Float {
            if (program == null) return 0f
            val totalMs = Duration.between(program.start, program.stop).toMillis()
            if (totalMs <= 0L) return 0f
            val elapsedMs = Duration.between(program.start, at).toMillis()
            return (elapsedMs.toFloat() / totalMs.toFloat()).coerceIn(0f, 1f)
        }

        fun messageFor(error: Throwable): String {
            return when (error) {
                is BowtieError.Unauthorized -> "Unauthorized"
                is BowtieError.TunersBusy -> "All tuners are in use"
                is BowtieError.NegotiationFailed -> error.message ?: "Negotiation failed"
                is BowtieError.NotFound -> "Not found"
                is BowtieError.Server -> error.message
                is BowtieError.Network -> error.cause2.message ?: error.cause2.toString()
                else -> error.message ?: error.toString()
            }
        }
    }
}
