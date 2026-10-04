package app.bowtie.core.vm

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import app.bowtie.core.BowtieClient
import app.bowtie.core.Recording
import app.bowtie.core.RecordingLogic
import app.bowtie.core.RecordingRule
import app.bowtie.core.RecordingLogic.Tab
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch

/**
 * Recordings screen: Upcoming / Recorded / Missed tabs, delete (cancel), stop,
 * keep, and play with a resume decision. Shared by the phone and TV apps.
 *
 * Actions are `suspend` (the UI launches them); [savePosition] is fire-and-forget
 * on [scope] so the player can save from `onDispose`, where it can't suspend.
 */
class RecordingsViewModel(
    private val client: BowtieClient,
    scope: CoroutineScope? = null,
) : ViewModel() {

    sealed class Load {
        data object Loading : Load()
        data class Loaded(val items: List<Recording>) : Load()

        /** The Shows tab: series rules. */
        data class Shows(val rules: List<RecordingRule>) : Load()
        data class Failed(val message: String) : Load()
    }

    data class UiState(
        val tab: Tab = Tab.Upcoming,
        val load: Load = Load.Loading,
        /** Transient error from an action (shown as a snackbar / banner). */
        val message: String? = null,
    )

    /** What the player needs to start a recording. */
    data class PlayStart(
        val recording: Recording,
        /** Server-relative, token-signed playlist (resolve against the server; no bearer). */
        val playlistUrl: String,
        val resumeAtSec: Int,
        val durationSec: Int,
        /** Ask "Resume / Start over"; otherwise start from the beginning. */
        val offerResume: Boolean,
    )

    private val workScope: CoroutineScope = scope ?: viewModelScope

    private val _state = MutableStateFlow(UiState())
    val state: StateFlow<UiState> = _state.asStateFlow()

    /** Position saves, sent one at a time in call order (the server keeps the last). */
    private val saves = Channel<Pair<Long, Int>>(Channel.UNLIMITED)

    init {
        workScope.launch {
            for ((id, sec) in saves) {
                try {
                    client.saveRecordingPosition(id, sec)
                } catch (e: CancellationException) {
                    throw e
                } catch (_: Exception) {
                    // Best-effort: the next save (every 15 s) catches up.
                }
            }
        }
    }

    suspend fun selectTab(tab: Tab) {
        _state.update { it.copy(tab = tab, load = Load.Loading) }
        refresh()
    }

    /** Reload the current tab; keeps the shown list until the new one arrives. */
    suspend fun refresh() {
        val tab = _state.value.tab
        val load = try {
            if (tab.isShows) Load.Shows(client.recordingRules()) else Load.Loaded(client.recordings(tab))
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            Load.Failed(RecordingLogic.errorMessage(e))
        }
        // Drop a late answer for a tab the user already left.
        _state.update { if (it.tab == tab) it.copy(load = load) else it }
    }

    /** Cancel (upcoming) or delete (recorded/missed). */
    suspend fun delete(r: Recording): Boolean = act { client.deleteRecording(r.id) }

    suspend fun stop(r: Recording): Boolean = act { client.stopRecording(r.id) }

    /** "Stop recording this show": deletes the rule (its upcoming recordings are cancelled). */
    suspend fun stopShow(rule: RecordingRule): Boolean = act { client.deleteRecordingRule(rule.id) }

    suspend fun setKept(r: Recording, keep: Boolean): Boolean =
        act { client.setRecordingProtected(r.id, keep) }

    /** Fetch the playlist and decide whether to offer resuming; null (with a message) on error. */
    suspend fun play(r: Recording): PlayStart? {
        return try {
            val p = client.playRecording(r.id)
            PlayStart(
                recording = r,
                playlistUrl = p.playlistUrl,
                resumeAtSec = p.positionSec,
                durationSec = p.durationSec,
                offerResume = RecordingLogic.shouldOfferResume(p.positionSec, p.durationSec),
            )
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            _state.update { it.copy(message = RecordingLogic.errorMessage(e)) }
            null
        }
    }

    /** Save the resume position (best-effort, in order, never blocks the caller). */
    fun savePosition(recordingId: Long, positionMs: Long) {
        saves.trySend(recordingId to (positionMs / 1000).toInt().coerceAtLeast(0))
    }

    fun clearMessage() {
        _state.update { it.copy(message = null) }
    }

    private suspend fun act(block: suspend () -> Unit): Boolean {
        return try {
            block()
            _state.update { it.copy(message = null) }
            refresh()
            true
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            _state.update { it.copy(message = RecordingLogic.errorMessage(e)) }
            false
        }
    }
}
