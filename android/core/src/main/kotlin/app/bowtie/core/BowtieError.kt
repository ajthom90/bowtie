package app.bowtie.core

/**
 * Viewer-facing error taxonomy mapped from HTTP / network failures.
 * Nested subclasses (not top-level sealed hierarchy siblings) for clear packaging.
 */
sealed class BowtieError : Exception() {
    /** Post-refresh 401 — caller should sign out. */
    data object Unauthorized : BowtieError()

    /** 503 — all tuners in use; [sessions] is the trimmed UI summary. */
    data class TunersBusy(val sessions: List<ActiveSessionSummary>) : BowtieError()

    /** 422 — codec/profile negotiation failed. */
    data class NegotiationFailed(override val message: String) : BowtieError()

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
