package app.bowtie.core

import java.time.Instant
import java.time.LocalDate
import java.time.ZoneId
import java.time.ZonedDateTime
import java.time.temporal.ChronoUnit
import java.util.Locale

/**
 * Pure DVR helpers shared by the phone and TV apps: tabs, plain-words labels,
 * which actions a row offers, the resume decision, and formatting.
 *
 * Times are formatted by hand (not `DateTimeFormatter` "a"), so output doesn't
 * change with the JDK's CLDR data (newer JDKs put U+202F before AM/PM).
 */
object RecordingLogic {

    /**
     * Recordings screen tabs and their `GET /recordings?state=` filter.
     * [Shows] lists series rules (`GET /recording-rules`) instead; [isShows] tells them apart.
     */
    enum class Tab(val query: String, val title: String) {
        Upcoming("upcoming", "Upcoming"),
        Recorded("recorded", "Recorded"),
        Missed("failed", "Missed"),
        Shows("", "Shows"),
        ;

        val isShows: Boolean get() = this == Shows
    }

    enum class Tone { Neutral, Live, Warn, Alert, Good }

    data class Badge(val text: String, val tone: Tone)

    enum class Action { Play, Stop, Keep, Delete }

    /** Seconds into a recording before resuming is worth offering. */
    const val RESUME_MIN_SEC = 10

    /** Seconds before the end inside which a recording counts as watched. */
    const val RESUME_END_MARGIN_SEC = 30

    /** How often the player saves the resume position. */
    const val POSITION_SAVE_INTERVAL_MS = 15_000L

    /** Why a recording failed (or is waiting), in plain words; null when nothing failed. */
    fun failureLabel(failure: String): String? = when (failure) {
        "" -> null
        "noTuner" -> "No tuner was free"
        "noSignal" -> "The channel had no signal"
        "diskFull" -> "The recordings disk was full"
        "skipped" -> "Skipped"
        else -> "Something went wrong"
    }

    /** A series episode someone deleted before it aired: not recorded on purpose. */
    fun isSkipped(r: Recording): Boolean = r.state == Recording.FAILED && r.failure == "skipped"

    /** State badges for a row; a ready recording shows none unless partial or kept. */
    fun badges(r: Recording): List<Badge> {
        val out = mutableListOf<Badge>()
        when (r.state) {
            Recording.SCHEDULED -> out += Badge("Scheduled", Tone.Neutral)
            Recording.WAITING -> out += Badge("Waiting for a tuner", Tone.Warn)
            Recording.RECORDING -> out += Badge("Recording", Tone.Live)
            Recording.CONVERTING -> out += Badge("Processing", Tone.Neutral)
            Recording.FAILED ->
                out += if (isSkipped(r)) Badge("Skipped", Tone.Neutral) else Badge("Missed", Tone.Alert)
        }
        if (r.ruleId > 0) out += Badge("Series", Tone.Neutral)
        if (r.partial) out += Badge("Partial", Tone.Warn)
        if (r.isProtected) out += Badge("Kept", Tone.Good)
        return out
    }

    /** A sentence explaining a missed, waiting or partial recording; null otherwise. */
    fun statusLine(r: Recording): String? = when (r.state) {
        Recording.FAILED ->
            if (isSkipped(r)) "Skipped" else "Missed: " + (failureLabel(r.failure) ?: "Something went wrong")
        Recording.WAITING -> when (r.failure) {
            "noTuner" -> "No tuner is free yet. Still trying."
            "noSignal" -> "No signal yet. Still trying."
            else -> "Still trying."
        }
        else -> if (r.partial) "Part of this program is missing" else null
    }

    /**
     * What a row offers: play when ready (and not locked by parental controls);
     * stop/keep/delete only when [Recording.canManage].
     */
    fun actions(r: Recording): Set<Action> {
        val out = linkedSetOf<Action>()
        if (r.state == Recording.READY && !r.locked) out += Action.Play
        if (!r.canManage) return out
        if (r.state == Recording.RECORDING) out += Action.Stop
        if (r.state == Recording.READY || r.state == Recording.CONVERTING) out += Action.Keep
        out += Action.Delete
        return out
    }

    /** "Cancel" for a recording that hasn't started; "Delete" once there's something to delete. */
    fun deleteLabel(r: Recording): String =
        if (r.state == Recording.SCHEDULED || r.state == Recording.WAITING) "Cancel" else "Delete"

    /** Button text for [action] on [r]. */
    fun actionLabel(r: Recording, action: Action): String = when (action) {
        Action.Play -> "Play"
        Action.Stop -> "Stop"
        Action.Keep -> if (r.isProtected) "Don't keep" else "Keep"
        Action.Delete -> deleteLabel(r)
    }

    /** Length, saved position and size for a recording; who scheduled it when it isn't yours. */
    fun detailLine(r: Recording): String? {
        val parts = mutableListOf<String>()
        if (r.state == Recording.READY && r.durationSec > 0) {
            parts += formatDuration(r.durationSec)
            if (shouldOfferResume(r.positionSec, r.durationSec)) {
                parts += "stopped at ${formatClock(r.positionSec)}"
            }
        }
        if (r.sizeBytes > 0) parts += formatSize(r.sizeBytes)
        if (!r.canManage && r.scheduledBy.isNotEmpty()) parts += "by ${r.scheduledBy}"
        return parts.takeIf { it.isNotEmpty() }?.joinToString(" · ")
    }

    /** Deleting a recording's files is permanent, so confirm; cancelling a schedule isn't. */
    fun deleteNeedsConfirm(r: Recording): Boolean = deleteLabel(r) == "Delete"

    /** Offer "Resume" when past the first 10 s and not within the last 30 s. */
    fun shouldOfferResume(positionSec: Int, durationSec: Int): Boolean =
        positionSec > RESUME_MIN_SEC && positionSec < durationSec - RESUME_END_MARGIN_SEC

    /** "0:05", "12:34", "1:02:03". */
    fun formatClock(sec: Int): String {
        val s = sec.coerceAtLeast(0)
        val h = s / 3600
        val m = (s % 3600) / 60
        val ss = s % 60
        return if (h > 0) {
            "%d:%02d:%02d".format(Locale.US, h, m, ss)
        } else {
            "%d:%02d".format(Locale.US, m, ss)
        }
    }

    /** "Under a minute", "45 min", "1 hr", "1 hr 5 min". */
    fun formatDuration(sec: Int): String {
        if (sec < 60) return "Under a minute"
        val h = sec / 3600
        val m = (sec % 3600) / 60
        return when {
            h == 0 -> "$m min"
            m == 0 -> "$h hr"
            else -> "$h hr $m min"
        }
    }

    /** Decimal units, as disks are sold: "850 MB", "1.9 GB". */
    fun formatSize(bytes: Long): String {
        val mb = bytes / 1_000_000.0
        if (mb < 1) return "Under 1 MB"
        if (mb < 1000) return "%.0f MB".format(Locale.US, mb)
        return "%.1f GB".format(Locale.US, mb / 1000)
    }

    /** "Today, 8:00–8:30 PM", "Tomorrow, 11:30 AM–12:30 PM", "Thu Oct 8, 7:05–8:00 PM". */
    fun formatWhen(
        start: Instant,
        stop: Instant,
        now: Instant = Instant.now(),
        zone: ZoneId = ZoneId.systemDefault(),
    ): String {
        val s = start.atZone(zone)
        val e = stop.atZone(zone)
        val days = ChronoUnit.DAYS.between(now.atZone(zone).toLocalDate(), s.toLocalDate())
        val day = when (days) {
            0L -> "Today"
            1L -> "Tomorrow"
            -1L -> "Yesterday"
            else -> dateLabel(s.toLocalDate())
        }
        val sameHalf = meridiem(s) == meridiem(e)
        val from = if (sameHalf) clock12(s) else "${clock12(s)} ${meridiem(s)}"
        return "$day, $from–${clock12(e)} ${meridiem(e)}"
    }

    /** Body of the 409 "not enough tuners" dialog, naming what holds the tuners. */
    fun conflictMessage(
        conflict: BowtieError.RecordingConflict,
        now: Instant = Instant.now(),
        zone: ZoneId = ZoneId.systemDefault(),
    ): String {
        val head = when (conflict.tunerCount) {
            1 -> "The tuner is"
            2 -> "Both tuners are"
            else -> "All ${conflict.tunerCount} tuners are"
        }
        val lines = conflict.conflicts.joinToString("\n") { r ->
            "• ${r.title} on ${r.channelName}, ${formatWhen(r.start, r.stop, now, zone)}"
        }
        return "$head already set to record then:\n$lines\n" +
            "If you record anyway, this only records if a tuner is free then."
    }

    /** Confirmation after "Record series": "Scheduled 1 episode", "Scheduled 6 episodes". */
    fun seriesScheduledMessage(count: Int): String =
        if (count == 1) "Scheduled 1 episode" else "Scheduled $count episodes"

    /** A Shows row's detail: channel, which episodes, how many it keeps, and who made it when it isn't yours. */
    fun ruleDetail(rule: RecordingRule): String {
        val parts = mutableListOf<String>()
        parts += if (rule.channelId == 0L) "Any channel" else rule.channelName.ifEmpty { "One channel" }
        parts += if (rule.newOnly) "New episodes only" else "All episodes"
        if (rule.keepLatest > 0) parts += "Keeps the latest ${rule.keepLatest}"
        if (!rule.canManage && rule.scheduledBy.isNotEmpty()) parts += "by ${rule.scheduledBy}"
        return parts.joinToString(" · ")
    }

    /** Plain-words error for a failed "Record this program". */
    fun scheduleErrorMessage(e: Throwable): String = when (e) {
        is BowtieError.Parental -> e.message
        is BowtieError.NotFound -> "That program isn't in the guide anymore."
        is BowtieError.Server ->
            if (e.status == 503) "Recording isn't available on this server." else e.message
        else -> errorMessage(e)
    }

    /** "🔒 TV-MA" / "🔒 Not rated" for a program or recording parental controls block; null otherwise. */
    fun lockLabel(locked: Boolean, rating: String): String? =
        if (!locked) null else "🔒 " + rating.ifEmpty { "Not rated" }

    fun lockLabel(r: Recording): String? = lockLabel(r.locked, r.rating)

    fun lockLabel(p: GuideProgram): String? = lockLabel(p.locked, p.rating)

    fun lockLabel(p: GuideSearchResult): String? = lockLabel(p.locked, p.rating)

    /** Generic error copy for DVR actions. */
    fun errorMessage(e: Throwable): String = when (e) {
        is BowtieError.Parental -> e.message
        is BowtieError.Unauthorized -> "Your session ended. Sign in again."
        is BowtieError.NotFound -> "That recording is gone."
        is BowtieError.Server -> when (e.status) {
            403 -> "Only the person who scheduled it or an admin can change it."
            503 -> "Recording isn't available on this server."
            else -> e.message
        }
        is BowtieError.Network -> "Couldn't reach the server."
        is BowtieError -> e.message ?: "Something went wrong"
        else -> e.message ?: "Something went wrong"
    }

    private val DAY_NAMES = arrayOf("Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun")
    private val MONTH_NAMES = arrayOf(
        "Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec",
    )

    private fun dateLabel(d: LocalDate): String =
        "${DAY_NAMES[d.dayOfWeek.value - 1]} ${MONTH_NAMES[d.monthValue - 1]} ${d.dayOfMonth}"

    private fun meridiem(t: ZonedDateTime): String = if (t.hour < 12) "AM" else "PM"

    private fun clock12(t: ZonedDateTime): String {
        val h = if (t.hour % 12 == 0) 12 else t.hour % 12
        return "%d:%02d".format(Locale.US, h, t.minute)
    }
}
