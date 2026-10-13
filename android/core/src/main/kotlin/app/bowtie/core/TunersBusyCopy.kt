package app.bowtie.core

/** Copy for the tuners-busy screen, shared by the phone and TV apps. */
object TunersBusyCopy {
    /** Above a channel list that hides channels nobody can start right now. */
    const val LIST_NOTE = "All tuners are in use — showing channels you can join."

    /** Instead of a channel list when no channel can be started. */
    const val NONE_WATCHABLE = "All tuners are in use. Try again in a few minutes."

    /** Line naming tuners held by other apps (e.g. Plex); null when none. */
    fun otherAppsLine(otherInUse: Int): String? {
        if (otherInUse <= 0) return null
        val tuners = if (otherInUse == 1) "1 tuner is" else "$otherInUse tuners are"
        return "$tuners in use by another app (like Plex)."
    }
}
