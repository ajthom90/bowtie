package app.bowtie.core

import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import java.util.Locale

/**
 * Player sleep timer: stops playback after a chosen time, or when the live
 * program ends. One per player screen — it survives channel changes there and
 * goes away with the player. Never persisted. Mirrors iOS `SleepTimer`.
 *
 * Time comes from [nowMs], and nothing happens on its own: the player calls
 * [tick] (about once a second), which updates [status], raises the warning in
 * the last minute, and calls [onFire] once at the end.
 */
class SleepTimer(
    private val nowMs: () -> Long = System::currentTimeMillis,
    /** Called once when the timer runs out: stop playback and leave the player. */
    private val onFire: () -> Unit,
) {
    enum class Option(val minutes: Int?, val label: String) {
        OFF(null, "Off"),
        MIN_15(15, "15 minutes"),
        MIN_30(30, "30 minutes"),
        MIN_45(45, "45 minutes"),
        MIN_60(60, "60 minutes"),
        MIN_90(90, "90 minutes"),
        HOURS_2(120, "2 hours"),

        /** Live TV only, when the guide knows when the program ends. */
        END_OF_PROGRAM(null, "End of this program"),
    }

    data class Status(
        val option: Option = Option.OFF,
        /** Time left, or null when off. */
        val remainingMs: Long? = null,
    ) {
        val isActive: Boolean get() = remainingMs != null

        /** In the last minute: show "Still watching?". */
        val warning: Boolean get() = remainingMs != null && remainingMs <= WARNING_LEAD_MS
    }

    private val _status = MutableStateFlow(Status())
    val status: StateFlow<Status> = _status.asStateFlow()

    private var deadlineMs: Long? = null

    /**
     * Start (or restart) with [option]. [Option.OFF] cancels. Returns false —
     * and leaves the timer off — for End of this program without a future end.
     */
    fun start(option: Option, programEndMs: Long? = null): Boolean {
        val now = nowMs()
        deadlineMs = when (option) {
            Option.OFF -> {
                cancel()
                return true
            }
            Option.END_OF_PROGRAM -> {
                if (programEndMs == null || programEndMs <= now) {
                    cancel()
                    return false
                }
                programEndMs
            }
            else -> now + option.minutes!! * 60_000L
        }
        _status.value = Status(option = option)
        refresh(now)
        return true
    }

    /** Keep watching: push the end out by the chosen duration (30 minutes for End of this program). */
    fun extend() {
        val deadline = deadlineMs ?: return
        val option = _status.value.option
        val extraMs = when (option) {
            Option.OFF -> return
            Option.END_OF_PROGRAM -> END_OF_PROGRAM_EXTENSION_MS
            else -> option.minutes!! * 60_000L
        }
        deadlineMs = deadline + extraMs
        refresh(nowMs())
    }

    fun cancel() {
        deadlineMs = null
        _status.value = Status()
    }

    /** Advance to the current time; fires (once) when the time is up. */
    fun tick() {
        if (deadlineMs == null) return
        refresh(nowMs())
    }

    private fun refresh(now: Long) {
        val deadline = deadlineMs ?: return
        val left = deadline - now
        if (left <= 0) {
            cancel()
            onFire()
            return
        }
        _status.value = _status.value.copy(remainingMs = left)
    }

    companion object {
        /** The "Still watching?" prompt shows for this long before the timer fires. */
        const val WARNING_LEAD_MS = 60_000L

        /** Keep watching after "End of this program" adds this much. */
        const val END_OF_PROGRAM_EXTENSION_MS = 30 * 60_000L

        /** Choices to offer: End of this program only when its end is known and still ahead. */
        fun options(programEndMs: Long?, nowMs: Long): List<Option> =
            Option.entries.filter {
                it != Option.END_OF_PROGRAM || (programEndMs != null && programEndMs > nowMs)
            }

        /** "1:00", "29:41", "1:05:00". Rounds up, so the last second reads 0:01. */
        fun format(ms: Long): String {
            val total = ((ms.coerceAtLeast(0) + 999) / 1000).toInt()
            val hours = total / 3600
            val minutes = (total % 3600) / 60
            val secs = total % 60
            return if (hours > 0) {
                String.format(Locale.ROOT, "%d:%02d:%02d", hours, minutes, secs)
            } else {
                String.format(Locale.ROOT, "%d:%02d", minutes, secs)
            }
        }

        fun promptText(remainingMs: Long): String = "Still watching? Sleeping in ${format(remainingMs)}"
    }
}
