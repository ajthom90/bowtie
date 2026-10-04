package app.bowtie.core

import kotlinx.serialization.Serializable

/**
 * One detected commercial break (`Recording.commercials`): seconds on the
 * recording's playback timeline, [start] inclusive, [end] exclusive.
 */
@Serializable
data class Commercial(val start: Double, val end: Double)

/**
 * "Skip ad" for one playback session of a recording.
 *
 * Holds the cleaned-up breaks and remembers which ones were already skipped,
 * so auto-skip happens at most once per break: seeking back into one that was
 * skipped plays it (the Skip ad button still offers to skip it). Time comes
 * from the player; nothing here runs on its own. Not thread-safe (main thread).
 */
class CommercialSkipper(commercials: List<Commercial>?) {
    /** Sorted, non-overlapping, non-empty breaks. */
    val segments: List<Commercial> = normalize(commercials)
    private val handled = mutableSetOf<Int>()

    /** The break playing at [timeSec], if any. */
    fun active(timeSec: Double): Commercial? = indexAt(timeSec)?.let { segments[it] }

    /** Skip ad pressed: where to seek (the break's end, seconds). Also counts as skipped for auto-skip. */
    fun skip(timeSec: Double): Double? {
        val i = indexAt(timeSec) ?: return null
        handled += i
        return segments[i].end
    }

    /** Auto-skip: where to seek the first time playback is inside a break; null once that break was skipped. */
    fun autoSkipTarget(timeSec: Double): Double? {
        val i = indexAt(timeSec) ?: return null
        if (!handled.add(i)) return null
        return segments[i].end
    }

    private fun indexAt(timeSec: Double): Int? {
        if (!timeSec.isFinite()) return null
        return segments.indexOfFirst { it.start <= timeSec && timeSec < it.end }.takeIf { it >= 0 }
    }

    companion object {
        /**
         * Drops empty, backwards and non-finite breaks, clamps a negative start
         * to 0, sorts by start and merges breaks that overlap or touch.
         */
        fun normalize(commercials: List<Commercial>?): List<Commercial> {
            val clean = commercials.orEmpty()
                .filter { it.start.isFinite() && it.end.isFinite() }
                .map { Commercial(maxOf(0.0, it.start), it.end) }
                .filter { it.end > it.start }
                .sortedWith(compareBy({ it.start }, { it.end }))
            val merged = mutableListOf<Commercial>()
            for (seg in clean) {
                val last = merged.lastOrNull()
                if (last != null && seg.start <= last.end) {
                    merged[merged.lastIndex] = Commercial(last.start, maxOf(last.end, seg.end))
                } else {
                    merged += seg
                }
            }
            return merged
        }
    }
}
