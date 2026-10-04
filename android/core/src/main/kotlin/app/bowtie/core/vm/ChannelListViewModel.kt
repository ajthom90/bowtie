package app.bowtie.core.vm

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import app.bowtie.core.BowtieClient
import app.bowtie.core.BowtieError
import app.bowtie.core.Channel
import app.bowtie.core.GuideLogic
import app.bowtie.core.GuideProgram
import app.bowtie.core.GuideRecordingMark
import app.bowtie.core.RecentChannel
import app.bowtie.core.Recording
import app.bowtie.core.RecordingLogic
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import java.time.Duration
import java.time.Instant

/**
 * Loads channels and joins each row with guide now/next for a 4-hour window.
 * Mirrors iOS [ChannelListModel].
 *
 * Refresh policy (driven by UI):
 * - full [refresh] on first show / pull-to-refresh / [channelsStale]
 * - [refreshIfStale] on ON_START and every 5 minutes while STARTED
 *
 * Favorites: rows are ordered favorites first (guide-number order), then the
 * rest in server order. That order is also the TV zap order. Toggles are
 * optimistic and revert (with a [message]) when the server refuses.
 * A server without favorites omits `favorite`: [LoadState.Loaded.favoritesSupported]
 * is false and the star and the Recent row stay hidden.
 */
class ChannelListViewModel(
    private val client: BowtieClient,
    private val now: () -> Instant = { Instant.now() },
    scope: CoroutineScope? = null,
) : ViewModel() {

    data class Row(
        val channel: Channel,
        val nowNext: GuideLogic.NowNext,
    ) {
        val id: Long get() = channel.id

        /** The signed-in user starred this channel. */
        val isFavorite: Boolean get() = channel.favorite == true
    }

    sealed class LoadState {
        data object Loading : LoadState()
        data class Loaded(
            /** Favorites first (guide-number order), then the rest in server order. */
            val rows: List<Row>,
            /** The server reports favorites; false on older servers (hide stars and Recent). */
            val favoritesSupported: Boolean = false,
        ) : LoadState() {
            val favorites: List<Row> get() = rows.filter { it.isFavorite }
            val others: List<Row> get() = rows.filterNot { it.isFavorite }
        }
        data class Failed(val message: String) : LoadState()
        data object Empty : LoadState()
    }

    /** Long-lived work (favorite toggles) outlives the screen that started it. */
    private val workScope: CoroutineScope by lazy { scope ?: viewModelScope }

    private val _state = MutableStateFlow<LoadState>(LoadState.Loading)
    val state: StateFlow<LoadState> = _state.asStateFlow()

    private val _recents = MutableStateFlow<List<RecentChannel>>(emptyList())

    /** Recently watched channels, newest first; empty when none or unsupported. */
    val recents: StateFlow<List<RecentChannel>> = _recents.asStateFlow()

    private val _message = MutableStateFlow<String?>(null)

    /** One-shot user-facing notice (e.g. a favorite toggle the server refused). */
    val message: StateFlow<String?> = _message.asStateFlow()

    /** Channel id → position in the last server response; "the rest" keeps this order. */
    @Volatile
    private var serverOrder: Map<Long, Int> = emptyMap()

    /** Wall-clock of last **successful** load (empty or loaded). Null until first success. */
    private var lastLoadedAt: Instant? = null

    /**
     * Fetches channels + guide(now..now+4h) and joins via [GuideLogic.nowNext],
     * then the Recent row (when the server supports it).
     */
    suspend fun refresh() {
        _state.value = LoadState.Loading
        val at = now()
        val stop = at.plus(GUIDE_WINDOW)

        var supported = false
        try {
            val channels = client.channels()
            val guide = client.guide(start = at, stop = stop)

            if (channels.isEmpty()) {
                _state.value = LoadState.Empty
                _recents.value = emptyList()
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
            supported = channels.any { it.favorite != null }
            serverOrder = channels.withIndex().associate { (i, c) -> c.id to i }
            _state.value = LoadState.Loaded(
                rows = favoritesFirst(rows),
                favoritesSupported = supported,
            )
            lastLoadedAt = at
        } catch (e: Exception) {
            _state.value = LoadState.Failed(messageFor(e))
            return
        }
        loadRecents(supported)
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

    /**
     * Re-fetches only the Recent row (e.g. on returning from the player, which
     * is a route change rather than ON_START). No-op unless a load showed the
     * server supports favorites.
     */
    suspend fun refreshRecents() {
        val loaded = _state.value as? LoadState.Loaded ?: return
        loadRecents(loaded.favoritesSupported)
    }

    private suspend fun loadRecents(supported: Boolean) {
        if (!supported) {
            _recents.value = emptyList()
            return
        }
        try {
            _recents.value = client.recents(limit = RECENTS_LIMIT)
        } catch (e: CancellationException) {
            throw e
        } catch (_: BowtieError.NotFound) {
            // Server predates recents.
            _recents.value = emptyList()
        } catch (_: Exception) {
            // Best-effort: a failed Recent row never fails the channel list.
        }
    }

    /**
     * Stars ([on]) or unstars a channel. Optimistic: the row flips and re-sorts
     * immediately; on failure it flips back and [message] explains.
     */
    suspend fun setFavorite(channelId: Long, on: Boolean) {
        if (!applyFavorite(channelId, on)) return
        try {
            client.setFavorite(channelId, on)
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            // Revert only this channel: a refresh may have replaced the rows meanwhile.
            applyFavorite(channelId, !on)
            _message.value = "Couldn't update favorites: ${messageFor(e)}"
        }
    }

    /**
     * Flips the star on [channelId] from its current state. Runs in the
     * ViewModel's scope so leaving the screen (e.g. tapping play right after)
     * does not cancel the request. The optimistic flip happens before the
     * first suspension.
     */
    fun toggleFavorite(channelId: Long): Job {
        val current = (_state.value as? LoadState.Loaded)
            ?.rows?.firstOrNull { it.id == channelId }
        return workScope.launch {
            if (current != null) setFavorite(channelId, !current.isFavorite)
        }
    }

    /** Clears [message] once the UI has shown it. */
    fun consumeMessage() {
        _message.value = null
    }

    /** The listed channel for a Recent item, or one built from its fields. */
    fun channelFor(recent: RecentChannel): Channel {
        val listed = (_state.value as? LoadState.Loaded)
            ?.rows?.firstOrNull { it.id == recent.channelId }?.channel
        return listed ?: Channel(
            id = recent.channelId,
            guideNumber = recent.guideNumber,
            name = recent.name,
            logoUrl = recent.logoUrl,
        )
    }

    /** Sets the favorite flag on one loaded row and re-sorts; false when there is nothing to flip. */
    private fun applyFavorite(channelId: Long, on: Boolean): Boolean {
        var applied = false
        _state.update { s ->
            if (s !is LoadState.Loaded || !s.favoritesSupported ||
                s.rows.none { it.id == channelId }
            ) {
                applied = false
                return@update s
            }
            applied = true
            val updated = s.rows.map { row ->
                if (row.id == channelId) row.copy(channel = row.channel.copy(favorite = on)) else row
            }
            val order = serverOrder
            s.copy(rows = favoritesFirst(updated.sortedBy { order[it.id] ?: Int.MAX_VALUE }))
        }
        return applied
    }

    sealed class ScheduleResult {
        /** Scheduled; [warning] is e.g. "uses all tuners" copy to show, or null. */
        data class Scheduled(val recording: Recording, val warning: String?) : ScheduleResult()

        /** 409: the tuners are booked; offer "Record anyway" (force). */
        data class Conflict(val error: BowtieError.RecordingConflict) : ScheduleResult()

        data class Failed(val message: String) : ScheduleResult()
    }

    /**
     * "Record this program": schedules [program] on [channelId] by its exact start,
     * then marks it in the loaded rows so the list shows it's set to record.
     */
    suspend fun record(
        channelId: Long,
        program: GuideProgram,
        force: Boolean = false,
    ): ScheduleResult {
        val created = try {
            client.scheduleRecording(channelId, program.start, force)
        } catch (e: CancellationException) {
            throw e
        } catch (e: BowtieError.RecordingConflict) {
            return ScheduleResult.Conflict(e)
        } catch (e: Exception) {
            return ScheduleResult.Failed(RecordingLogic.scheduleErrorMessage(e))
        }
        val mark = GuideRecordingMark(id = created.recording.id, state = created.recording.state)
        _state.update { s ->
            if (s !is LoadState.Loaded) return@update s
            // copy() keeps favoritesSupported; map() keeps the favorites-first order.
            s.copy(
                rows = s.rows.map { row ->
                    if (row.channel.id != channelId) return@map row
                    fun GuideProgram?.marked() =
                        if (this != null && start == program.start) copy(recording = mark) else this
                    row.copy(nowNext = GuideLogic.NowNext(row.nowNext.now.marked(), row.nowNext.next.marked()))
                },
            )
        }
        return ScheduleResult.Scheduled(created.recording, created.warnings.firstOrNull()?.message)
    }

    companion object {
        /** Guide request window length: now … now+4h. */
        val GUIDE_WINDOW: Duration = Duration.ofHours(4)

        /** Freshness window matching the 5-minute auto-refresh timer. */
        val STALE_INTERVAL: Duration = Duration.ofMinutes(5)

        /** Items in the Recent row. */
        const val RECENTS_LIMIT = 8

        /**
         * Orders guide numbers numerically part by part ("9.1" < "11.1",
         * "4.2" < "4.10", "7" < "7.1"); non-numeric parts compare as text.
         */
        val GuideNumberOrder: Comparator<String> = Comparator { a, b ->
            val pa = a.split('.', '-')
            val pb = b.split('.', '-')
            for (i in 0 until maxOf(pa.size, pb.size)) {
                val x = pa.getOrNull(i) ?: return@Comparator -1
                val y = pb.getOrNull(i) ?: return@Comparator 1
                val xi = x.toLongOrNull()
                val yi = y.toLongOrNull()
                val c = if (xi != null && yi != null) xi.compareTo(yi) else x.compareTo(y)
                if (c != 0) return@Comparator c
            }
            a.compareTo(b)
        }

        /**
         * Favorites first in guide-number order, then the rest in the order
         * given ([rows] should be in server order).
         */
        fun favoritesFirst(rows: List<Row>): List<Row> {
            val (favorites, others) = rows.partition { it.isFavorite }
            return favorites.sortedWith(compareBy(GuideNumberOrder) { it.channel.guideNumber }) + others
        }

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
                is BowtieError.Parental -> error.message
                is BowtieError.Unauthorized -> "Unauthorized"
                is BowtieError.TunersBusy -> "All tuners are in use"
                is BowtieError.RecordingConflict -> "Not enough tuners then"
                is BowtieError.NegotiationFailed -> error.message ?: "Negotiation failed"
                is BowtieError.NotFound -> "Not found"
                is BowtieError.Server -> error.message
                is BowtieError.Network -> error.cause2.message ?: error.cause2.toString()
                else -> error.message ?: error.toString()
            }
        }
    }
}
