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
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.atomic.AtomicLong

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

    /** Bumped per refresh and per remove, so an older fetch can't land. */
    private val generation = AtomicLong(0)

    /**
     * Removed recordings → their position when removed. A list read still
     * showing that position hasn't caught up with the reset; any other
     * position (0, or watched again) confirms it and the id is forgotten.
     */
    private val removedAt = ConcurrentHashMap<Long, Int>()

    /** Reload the row. A failure keeps what's shown (it's a convenience row). */
    suspend fun refresh() {
        val gen = generation.incrementAndGet()
        val rows = try {
            client.recordings(RecordingLogic.Tab.Recorded)
        } catch (e: CancellationException) {
            throw e
        } catch (_: Exception) {
            return
        }
        if (gen != generation.get()) return
        _items.value = ContinueWatching.items(dropStaleRemoved(rows))
    }

    private fun dropStaleRemoved(rows: List<Recording>): List<Recording> {
        if (removedAt.isEmpty()) return rows
        val stale = rows.filter { removedAt[it.id] == it.positionSec }.map { it.id }.toSet()
        removedAt.keys.retainAll(stale)
        return rows.filterNot { it.id in stale }
    }

    /** "Remove from Continue watching": resets the saved position to 0. */
    suspend fun remove(r: Recording): Boolean {
        return try {
            client.saveRecordingPosition(r.id, 0)
            // A refresh already under way may have read the old position.
            generation.incrementAndGet()
            removedAt[r.id] = r.positionSec
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
