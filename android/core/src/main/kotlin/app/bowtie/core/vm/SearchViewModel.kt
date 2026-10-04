package app.bowtie.core.vm

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import app.bowtie.core.BowtieClient
import app.bowtie.core.BowtieError
import app.bowtie.core.Channel
import app.bowtie.core.GuideRecordingMark
import app.bowtie.core.GuideSearchResult
import app.bowtie.core.RecordingLogic
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import java.time.Instant

/**
 * Guide search: type a title or episode, get upcoming and on-now airings,
 * then Watch (when on now), Record or Record series.
 *
 * Typing goes through [onQueryChange], debounced by [debounceMs] on [scope];
 * [search] runs one immediately (e.g. the keyboard's Search key).
 */
class SearchViewModel(
    private val client: BowtieClient,
    private val now: () -> Instant = { Instant.now() },
    scope: CoroutineScope? = null,
    private val debounceMs: Long = DEBOUNCE_MS,
) : ViewModel() {

    sealed class Results {
        /** Nothing typed yet. */
        data object Idle : Results()
        data object Loading : Results()
        data class Loaded(val items: List<GuideSearchResult>) : Results()
        data class Failed(val message: String) : Results()
    }

    data class UiState(
        val query: String = "",
        val results: Results = Results.Idle,
    )

    private val workScope: CoroutineScope by lazy { scope ?: viewModelScope }

    private val _state = MutableStateFlow(UiState())
    val state: StateFlow<UiState> = _state.asStateFlow()

    private var pending: Job? = null

    /** The search field changed: search [query] after a short pause in typing. */
    fun onQueryChange(query: String) {
        pending?.cancel()
        _state.update { it.copy(query = query) }
        if (query.isBlank()) {
            _state.update { it.copy(results = Results.Idle) }
            return
        }
        pending = workScope.launch {
            delay(debounceMs)
            search(query)
        }
    }

    /** Search [query] now; a late answer for an older query is dropped. */
    suspend fun search(query: String = _state.value.query) {
        val q = query.trim()
        _state.update { it.copy(query = query) }
        if (q.isEmpty()) {
            _state.update { it.copy(results = Results.Idle) }
            return
        }
        if (_state.value.results !is Results.Loaded) {
            _state.update { it.copy(results = Results.Loading) }
        }
        val results = try {
            Results.Loaded(client.searchGuide(q))
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            Results.Failed(messageFor(e))
        }
        _state.update { if (it.query.trim() == q) it.copy(results = results) else it }
    }

    /** On the air at this moment, so Watch makes sense. */
    fun isOnNow(r: GuideSearchResult): Boolean {
        val at = now()
        return !r.start.isAfter(at) && r.stop.isAfter(at)
    }

    /** The channel to play for Watch. */
    fun channelFor(r: GuideSearchResult): Channel = Channel(
        id = r.channelId,
        guideNumber = r.guideNumber,
        name = r.channelName,
        logoUrl = r.logoUrl,
    )

    /** Record this airing; on success the result shows it's set to record. */
    suspend fun record(r: GuideSearchResult, force: Boolean = false): ChannelListViewModel.ScheduleResult {
        val created = try {
            client.scheduleRecording(r.channelId, r.start, force)
        } catch (e: CancellationException) {
            throw e
        } catch (e: BowtieError.RecordingConflict) {
            return ChannelListViewModel.ScheduleResult.Conflict(e)
        } catch (e: Exception) {
            return ChannelListViewModel.ScheduleResult.Failed(RecordingLogic.scheduleErrorMessage(e))
        }
        val mark = GuideRecordingMark(id = created.recording.id, state = created.recording.state)
        updateItems { items ->
            items.map { if (it.channelId == r.channelId && it.start == r.start) it.copy(recording = mark) else it }
        }
        return ChannelListViewModel.ScheduleResult.Scheduled(created.recording, created.warnings.firstOrNull()?.message)
    }

    /** Record every new episode of this show on its channel, then re-run the search to mark them. */
    suspend fun recordSeries(r: GuideSearchResult): SeriesResult {
        val result = createSeriesRule(client, r.channelId, r.start)
        if (result is SeriesResult.Scheduled) search()
        return result
    }

    private fun updateItems(transform: (List<GuideSearchResult>) -> List<GuideSearchResult>) {
        _state.update { s ->
            val loaded = s.results as? Results.Loaded ?: return@update s
            s.copy(results = Results.Loaded(transform(loaded.items)))
        }
    }

    private fun messageFor(e: Throwable): String = when (e) {
        is BowtieError.Server -> if (e.status == 503) "Search isn't available on this server." else e.message
        is BowtieError.Network -> "Couldn't reach the server."
        is BowtieError.Unauthorized -> "Your session ended. Sign in again."
        else -> ChannelListViewModel.messageFor(e)
    }

    companion object {
        /** Pause in typing before searching. */
        const val DEBOUNCE_MS = 300L
    }
}
