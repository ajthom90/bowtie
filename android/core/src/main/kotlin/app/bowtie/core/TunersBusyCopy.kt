package app.bowtie.core

/** Copy for the tuners-busy screen, shared by the phone and TV apps. */
object TunersBusyCopy {
    /** Line naming tuners held by other apps (e.g. Plex); null when none. */
    fun otherAppsLine(otherInUse: Int): String? {
        if (otherInUse <= 0) return null
        val tuners = if (otherInUse == 1) "1 tuner is" else "$otherInUse tuners are"
        return "$tuners in use by another app (like Plex)."
    }
}
