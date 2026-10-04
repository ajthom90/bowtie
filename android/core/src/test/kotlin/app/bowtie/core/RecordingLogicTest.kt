package app.bowtie.core

import app.bowtie.core.RecordingLogic.Action
import app.bowtie.core.RecordingLogic.Badge
import app.bowtie.core.RecordingLogic.Tone
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import java.time.Instant
import java.time.ZoneId

class RecordingLogicTest {

    private val zone: ZoneId = ZoneId.of("America/Chicago")

    /** Sat 2026-10-03 19:00 Chicago (00:00Z Sun). */
    private val now: Instant = Instant.parse("2026-10-04T00:00:00Z")

    private fun rec(
        state: String = Recording.READY,
        failure: String = "",
        partial: Boolean = false,
        protected: Boolean = false,
        canManage: Boolean = true,
        title: String = "Jeopardy!",
        channelName: String = "5.1 KSTP",
        start: Instant = Instant.parse("2026-10-04T01:00:00Z"),
        stop: Instant = Instant.parse("2026-10-04T01:30:00Z"),
    ) = Recording(
        id = 7,
        title = title,
        channelId = 5,
        channelName = channelName,
        start = start,
        stop = stop,
        state = state,
        partial = partial,
        failure = failure,
        isProtected = protected,
        canManage = canManage,
    )

    // ── Tabs ────────────────────────────────────────────────────────────────

    @Test
    fun tabs_mapToServerFilters() {
        assertEquals("upcoming", RecordingLogic.Tab.Upcoming.query)
        assertEquals("recorded", RecordingLogic.Tab.Recorded.query)
        assertEquals("failed", RecordingLogic.Tab.Missed.query)
        assertEquals(
            listOf("Upcoming", "Recorded", "Missed"),
            RecordingLogic.Tab.entries.map { it.title },
        )
    }

    // ── Failure labels ──────────────────────────────────────────────────────

    @Test
    fun failureLabel_plainWords() {
        assertEquals("No tuner was free", RecordingLogic.failureLabel("noTuner"))
        assertEquals("The channel had no signal", RecordingLogic.failureLabel("noSignal"))
        assertEquals("The recordings disk was full", RecordingLogic.failureLabel("diskFull"))
        assertEquals("Something went wrong", RecordingLogic.failureLabel("error"))
        assertEquals("Something went wrong", RecordingLogic.failureLabel("somethingNew"))
        assertNull(RecordingLogic.failureLabel(""))
    }

    // ── Badges / status line ────────────────────────────────────────────────

    @Test
    fun badges_perState() {
        assertEquals(listOf(Badge("Scheduled", Tone.Neutral)), RecordingLogic.badges(rec(state = "scheduled")))
        assertEquals(
            listOf(Badge("Waiting for a tuner", Tone.Warn)),
            RecordingLogic.badges(rec(state = "waiting", failure = "noTuner")),
        )
        assertEquals(listOf(Badge("Recording", Tone.Live)), RecordingLogic.badges(rec(state = "recording")))
        assertEquals(listOf(Badge("Processing", Tone.Neutral)), RecordingLogic.badges(rec(state = "converting")))
        assertEquals(emptyList<Badge>(), RecordingLogic.badges(rec(state = "ready")))
        assertEquals(listOf(Badge("Missed", Tone.Alert)), RecordingLogic.badges(rec(state = "failed", failure = "noTuner")))
    }

    @Test
    fun badges_partialAndKept() {
        assertEquals(
            listOf(Badge("Partial", Tone.Warn), Badge("Kept", Tone.Good)),
            RecordingLogic.badges(rec(state = "ready", partial = true, protected = true)),
        )
    }

    @Test
    fun statusLine_explainsInPlainWords() {
        assertEquals(
            "Missed: No tuner was free",
            RecordingLogic.statusLine(rec(state = "failed", failure = "noTuner")),
        )
        assertEquals(
            "Missed: Something went wrong",
            RecordingLogic.statusLine(rec(state = "failed", failure = "")),
        )
        assertEquals(
            "No tuner is free yet. Still trying.",
            RecordingLogic.statusLine(rec(state = "waiting", failure = "noTuner")),
        )
        assertEquals(
            "No signal yet. Still trying.",
            RecordingLogic.statusLine(rec(state = "waiting", failure = "noSignal")),
        )
        assertEquals("Still trying.", RecordingLogic.statusLine(rec(state = "waiting")))
        assertEquals(
            "Part of this program is missing",
            RecordingLogic.statusLine(rec(state = "ready", partial = true)),
        )
        assertNull(RecordingLogic.statusLine(rec(state = "ready")))
        assertNull(RecordingLogic.statusLine(rec(state = "scheduled")))
    }

    // ── Actions ─────────────────────────────────────────────────────────────

    @Test
    fun actions_gatedByStateAndCanManage() {
        assertEquals(setOf(Action.Delete), RecordingLogic.actions(rec(state = "scheduled")))
        assertEquals(setOf(Action.Delete), RecordingLogic.actions(rec(state = "waiting")))
        assertEquals(setOf(Action.Stop, Action.Delete), RecordingLogic.actions(rec(state = "recording")))
        assertEquals(setOf(Action.Keep, Action.Delete), RecordingLogic.actions(rec(state = "converting")))
        assertEquals(
            setOf(Action.Play, Action.Keep, Action.Delete),
            RecordingLogic.actions(rec(state = "ready")),
        )
        assertEquals(setOf(Action.Delete), RecordingLogic.actions(rec(state = "failed")))
    }

    @Test
    fun actions_withoutCanManage_onlyPlay() {
        assertEquals(setOf(Action.Play), RecordingLogic.actions(rec(state = "ready", canManage = false)))
        assertEquals(emptySet<Action>(), RecordingLogic.actions(rec(state = "recording", canManage = false)))
        assertEquals(emptySet<Action>(), RecordingLogic.actions(rec(state = "scheduled", canManage = false)))
    }

    @Test
    fun deleteLabel_cancelForUpcoming() {
        assertEquals("Cancel", RecordingLogic.deleteLabel(rec(state = "scheduled")))
        assertEquals("Cancel", RecordingLogic.deleteLabel(rec(state = "waiting")))
        assertEquals("Delete", RecordingLogic.deleteLabel(rec(state = "recording")))
        assertEquals("Delete", RecordingLogic.deleteLabel(rec(state = "ready")))
        assertEquals("Delete", RecordingLogic.deleteLabel(rec(state = "failed")))
    }

    // ── Resume decision ─────────────────────────────────────────────────────

    @Test
    fun shouldOfferResume_boundaries() {
        assertFalse(RecordingLogic.shouldOfferResume(positionSec = 0, durationSec = 1800))
        assertFalse(RecordingLogic.shouldOfferResume(positionSec = 10, durationSec = 1800))
        assertTrue(RecordingLogic.shouldOfferResume(positionSec = 11, durationSec = 1800))
        assertTrue(RecordingLogic.shouldOfferResume(positionSec = 1769, durationSec = 1800))
        assertFalse(RecordingLogic.shouldOfferResume(positionSec = 1770, durationSec = 1800))
        assertFalse(RecordingLogic.shouldOfferResume(positionSec = 1800, durationSec = 1800))
        assertFalse(RecordingLogic.shouldOfferResume(positionSec = 20, durationSec = 0))
    }

    // ── Formatting ──────────────────────────────────────────────────────────

    @Test
    fun formatClock() {
        assertEquals("0:00", RecordingLogic.formatClock(0))
        assertEquals("0:05", RecordingLogic.formatClock(5))
        assertEquals("12:34", RecordingLogic.formatClock(754))
        assertEquals("1:02:03", RecordingLogic.formatClock(3723))
        assertEquals("0:00", RecordingLogic.formatClock(-4))
    }

    @Test
    fun formatDuration() {
        assertEquals("Under a minute", RecordingLogic.formatDuration(59))
        assertEquals("1 min", RecordingLogic.formatDuration(60))
        assertEquals("45 min", RecordingLogic.formatDuration(45 * 60 + 20))
        assertEquals("1 hr", RecordingLogic.formatDuration(3600))
        assertEquals("1 hr 5 min", RecordingLogic.formatDuration(3600 + 5 * 60))
        assertEquals("2 hr 30 min", RecordingLogic.formatDuration(9000))
    }

    @Test
    fun formatSize() {
        assertEquals("Under 1 MB", RecordingLogic.formatSize(400_000))
        assertEquals("850 MB", RecordingLogic.formatSize(850_000_000))
        assertEquals("1.9 GB", RecordingLogic.formatSize(1_900_000_000))
        assertEquals("12.4 GB", RecordingLogic.formatSize(12_400_000_000))
    }

    @Test
    fun formatWhen_todayTomorrowYesterdayAndDate() {
        // 20:00–20:30 Chicago, same day as now (19:00 Chicago).
        assertEquals(
            "Today, 8:00–8:30 PM",
            RecordingLogic.formatWhen(
                Instant.parse("2026-10-04T01:00:00Z"),
                Instant.parse("2026-10-04T01:30:00Z"),
                now = now,
                zone = zone,
            ),
        )
        // Crosses noon/midnight halves → both meridiems shown.
        assertEquals(
            "Tomorrow, 11:30 AM–12:30 PM",
            RecordingLogic.formatWhen(
                Instant.parse("2026-10-04T16:30:00Z"),
                Instant.parse("2026-10-04T17:30:00Z"),
                now = now,
                zone = zone,
            ),
        )
        assertEquals(
            "Yesterday, 12:00–1:00 AM",
            RecordingLogic.formatWhen(
                Instant.parse("2026-10-02T05:00:00Z"),
                Instant.parse("2026-10-02T06:00:00Z"),
                now = now,
                zone = zone,
            ),
        )
        assertEquals(
            "Thu Oct 8, 7:05–8:00 PM",
            RecordingLogic.formatWhen(
                Instant.parse("2026-10-09T00:05:00Z"),
                Instant.parse("2026-10-09T01:00:00Z"),
                now = now,
                zone = zone,
            ),
        )
    }

    // ── 409 conflict copy ───────────────────────────────────────────────────

    @Test
    fun conflictMessage_listsWhatHoldsTheTuners() {
        val conflict = BowtieError.RecordingConflict(
            message = "not enough tuners",
            tunerCount = 2,
            conflicts = listOf(
                rec(title = "Jeopardy!", channelName = "5.1 KSTP"),
                rec(title = "News", channelName = "9.1 FOX 9"),
            ),
        )
        assertEquals(
            "Both tuners are already set to record then:\n" +
                "• Jeopardy! on 5.1 KSTP, Today, 8:00–8:30 PM\n" +
                "• News on 9.1 FOX 9, Today, 8:00–8:30 PM\n" +
                "If you record anyway, this only records if a tuner is free then.",
            RecordingLogic.conflictMessage(conflict, now = now, zone = zone),
        )
    }

    @Test
    fun conflictMessage_tunerCountWording() {
        val one = BowtieError.RecordingConflict("x", 1, listOf(rec()))
        assertTrue(
            RecordingLogic.conflictMessage(one, now, zone)
                .startsWith("The tuner is already set to record then:"),
        )
        val four = BowtieError.RecordingConflict("x", 4, listOf(rec()))
        assertTrue(
            RecordingLogic.conflictMessage(four, now, zone)
                .startsWith("All 4 tuners are already set to record then:"),
        )
    }

    // ── Schedule error copy ─────────────────────────────────────────────────

    @Test
    fun scheduleErrorMessage_plainWords() {
        assertEquals(
            "That program isn't in the guide anymore.",
            RecordingLogic.scheduleErrorMessage(BowtieError.NotFound),
        )
        assertEquals(
            "Recording isn't available on this server.",
            RecordingLogic.scheduleErrorMessage(BowtieError.Server(503, "recording is not available")),
        )
        assertEquals(
            "program already ended",
            RecordingLogic.scheduleErrorMessage(BowtieError.Server(400, "program already ended")),
        )
    }
}
