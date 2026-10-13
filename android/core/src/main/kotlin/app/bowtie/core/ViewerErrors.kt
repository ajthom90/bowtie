package app.bowtie.core

import android.util.Log
import java.io.IOException

/**
 * Plain-words error copy for viewers, shared by the phone and TV apps.
 *
 * Viewers never see exception text, HTTP codes or raw response bodies; the
 * technical cause goes to the log. A server's own `error` message is already
 * plain words and is shown as-is.
 */
object ViewerErrors {
    const val CANT_REACH_SERVER =
        "Can't reach your Bowtie server. Check your connection and try again."
    const val SOMETHING_WRONG = "Something went wrong. Try again."
    const val STREAM_STOPPED = "The stream stopped. Try again."
    const val SESSION_ENDED = "Your session ended. Sign in again."
    const val NOT_AVAILABLE = "That isn't available anymore."

    private const val TAG = "Bowtie"

    fun message(e: Throwable): String {
        log(e)
        return when (e) {
            is BowtieError.Parental -> e.message
            is BowtieError.Server -> e.message.ifBlank { SOMETHING_WRONG }
            is BowtieError.RecordingConflict -> e.message.ifBlank { SOMETHING_WRONG }
            is BowtieError.Network -> CANT_REACH_SERVER
            is BowtieError.Unauthorized -> SESSION_ENDED
            is BowtieError.TunersBusy -> TunersBusyCopy.NONE_WATCHABLE
            is BowtieError.NotFound -> NOT_AVAILABLE
            is BowtieError.NegotiationFailed -> SOMETHING_WRONG
            is IOException -> CANT_REACH_SERVER
            else -> SOMETHING_WRONG
        }
    }

    /** Keeps the technical detail for whoever reads the device log. */
    fun log(e: Throwable) {
        try {
            Log.w(TAG, "viewer-facing error: $e", e.cause ?: e)
        } catch (_: RuntimeException) {
            // JVM unit tests: android.util.Log isn't available.
        }
    }

    /** Logs technical [detail] (e.g. a raw error body) that viewers don't see. */
    fun logDetail(detail: String) {
        try {
            Log.w(TAG, detail)
        } catch (_: RuntimeException) {
            // JVM unit tests: android.util.Log isn't available.
        }
    }
}
