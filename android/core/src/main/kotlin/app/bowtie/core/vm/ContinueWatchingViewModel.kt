package app.bowtie.core.vm

import androidx.lifecycle.ViewModel
import app.bowtie.core.BowtieClient
import app.bowtie.core.ContinueWatching
import app.bowtie.core.Recording
import app.bowtie.core.RecordingLogic
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update

/**
 * The "Continue watching" row (phone and TV): the caller's part-watched
 * recordings. Playback goes through [RecordingsViewModel.play].
 */
class ContinueWatchingViewModel(
    private val client: BowtieClient,
) : ViewModel() {

    private val _items = MutableStateFlow<List<Recording>>(emptyList())

    /** Empty hides the row. */
    val items: StateFlow<List<Recording>> = _items.asStateFlow()

    private val _message = MutableStateFlow<String?>(null)

    /** Why the last remove failed (a toast / snackbar). */
    val message: StateFlow<String?> = _message.asStateFlow()

    /** Reload the row. A failure keeps what's shown (it's a convenience row). */
    suspend fun refresh() {
        val rows = try {
            client.recordings(RecordingLogic.Tab.Recorded)
        } catch (e: CancellationException) {
            throw e
        } catch (_: Exception) {
            return
        }
        _items.value = ContinueWatching.items(rows)
    }

    /** "Remove from Continue watching": resets the saved position to 0. */
    suspend fun remove(r: Recording): Boolean {
        return try {
            client.saveRecordingPosition(r.id, 0)
            _items.update { list -> list.filterNot { it.id == r.id } }
            true
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            _message.value = RecordingLogic.errorMessage(e)
            false
        }
    }

    fun clearMessage() {
        _message.value = null
    }
}
