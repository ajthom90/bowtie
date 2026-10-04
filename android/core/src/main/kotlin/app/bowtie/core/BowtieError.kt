package app.bowtie.core

/**
 * Viewer-facing error taxonomy mapped from HTTP / network failures.
 * Nested subclasses (not top-level sealed hierarchy siblings) for clear packaging.
 */
sealed class BowtieError : Exception() {
    /** Post-refresh 401 — caller should sign out. */
    data object Unauthorized : BowtieError()

    /** 503 — all tuners in use; [sessions] is the trimmed UI summary. */
    /** [otherInUse]: tuners held by other apps (e.g. Plex); 0 when none or from an older server. */
    data class TunersBusy(val sessions: List<ActiveSessionSummary>, val otherInUse: Int = 0) : BowtieError()

    /** 422 — codec/profile negotiation failed. */
    data class NegotiationFailed(override val message: String) : BowtieError()

    /**
     * 409 from scheduling a recording: more distinct channels than tuners.
     * [conflicts] are the recordings already holding the tuners then.
     */
    data class RecordingConflict(
        override val message: String,
        val tunerCount: Int,
        val conflicts: List<Recording>,
    ) : BowtieError()

    /** 404 — unknown or disabled channel / resource. */
    data object NotFound : BowtieError()

    /** Other non-success HTTP status. */
    data class Server(val status: Int, override val message: String) : BowtieError()

    /**
     * Transport / I/O failure.
     * Named [cause2] to avoid clashing with [Throwable.cause] property overrides.
     */
    data class Network(val cause2: Throwable) : BowtieError() {
        override val cause: Throwable get() = cause2
    }
}
