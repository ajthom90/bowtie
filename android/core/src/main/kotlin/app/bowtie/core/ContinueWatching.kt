package app.bowtie.core

/**
 * "Continue watching": recordings the caller started and hasn't finished.
 * Pure rules shared by the phone and TV apps (BowtieKit mirrors them).
 */
object ContinueWatching {
    /** At least a minute watched… */
    const val MIN_POSITION_SEC = 60

    /** …and more than two minutes still to go. */
    const val END_MARGIN_SEC = 120

    const val MAX_ITEMS = 10

    /** Ready, not parental-locked, and part-watched (known length). */
    fun isEligible(r: Recording): Boolean =
        r.state == Recording.READY && !r.locked && r.durationSec > 0 &&
            r.positionSec >= MIN_POSITION_SEC && r.positionSec < r.durationSec - END_MARGIN_SEC

    /**
     * Most recently watched first (when the position was last saved);
     * recordings without a save time go last, newest recording first.
     */
    fun items(recordings: List<Recording>): List<Recording> =
        recordings.filter(::isEligible)
            .sortedWith(
                compareBy<Recording> { it.positionUpdatedAt == null }
                    .thenByDescending { it.positionUpdatedAt }
                    .thenByDescending { it.start }
                    .thenByDescending { it.id },
            )
            .take(MAX_ITEMS)

    /** "less than a minute left", "23 min left", "1 hr left", "1 hr 5 min left". */
    fun remainingText(r: Recording): String {
        val remaining = r.durationSec - r.positionSec
        if (remaining < 60) return "less than a minute left"
        val h = remaining / 3600
        val m = (remaining % 3600) / 60
        return when {
            h == 0 -> "$m min left"
            m == 0 -> "$h hr left"
            else -> "$h hr $m min left"
        }
    }

    /** 0…1 watched, for the card's progress bar. */
    fun progress(r: Recording): Float =
        if (r.durationSec <= 0) 0f else (r.positionSec.toFloat() / r.durationSec).coerceIn(0f, 1f)
}
