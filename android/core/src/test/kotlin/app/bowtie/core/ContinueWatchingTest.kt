package app.bowtie.core

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import java.time.Instant

class ContinueWatchingTest {

    private fun rec(
        id: Long,
        state: String = Recording.READY,
        durationSec: Int = 3600,
        positionSec: Int = 600,
        locked: Boolean = false,
        start: String = "2026-10-05T00:00:00Z",
        updated: String? = null,
    ) = Recording(
        id = id,
        title = "Show $id",
        channelId = 5,
        start = Instant.parse(start),
        stop = Instant.parse(start).plusSeconds(durationSec.toLong()),
        state = state,
        durationSec = durationSec,
        positionSec = positionSec,
        locked = locked,
        positionUpdatedAt = updated?.let(Instant::parse),
    )

    private fun json(extra: String = "") = """
        {"id":7,"title":"Jeopardy!","channelId":5,
         "start":"2026-10-04T00:00:00Z","stop":"2026-10-04T00:30:00Z",
         "state":"ready","durationSec":1800,"positionSec":120$extra}
    """.trimIndent()

    // ── Decoding ────────────────────────────────────────────────────────────

    @Test
    fun decode_positionUpdatedAt_present() {
        val r = BowtieJson.decodeFromString<Recording>(json(""","positionUpdatedAt":"2026-10-06T01:02:03Z""""))
        assertEquals(Instant.parse("2026-10-06T01:02:03Z"), r.positionUpdatedAt)
    }

    @Test
    fun decode_positionUpdatedAt_fractionalSeconds() {
        val r = BowtieJson.decodeFromString<Recording>(json(""","positionUpdatedAt":"2026-10-06T01:02:03.25Z""""))
        assertEquals(Instant.parse("2026-10-06T01:02:03.25Z"), r.positionUpdatedAt)
    }

    @Test
    fun decode_positionUpdatedAt_absentOrNull() {
        assertNull(BowtieJson.decodeFromString<Recording>(json()).positionUpdatedAt)
        assertNull(BowtieJson.decodeFromString<Recording>(json(""","positionUpdatedAt":null""")).positionUpdatedAt)
    }

    // ── Eligibility ─────────────────────────────────────────────────────────

    @Test
    fun needsAtLeastAMinuteWatched() {
        assertFalse(ContinueWatching.isEligible(rec(1, positionSec = 59)))
        assertTrue(ContinueWatching.isEligible(rec(1, positionSec = 60)))
    }

    @Test
    fun excludesTheLastTwoMinutes() {
        assertTrue(ContinueWatching.isEligible(rec(1, durationSec = 3600, positionSec = 3479)))
        assertFalse(ContinueWatching.isEligible(rec(1, durationSec = 3600, positionSec = 3480)))
        assertFalse(ContinueWatching.isEligible(rec(1, durationSec = 3600, positionSec = 3600)))
    }

    @Test
    fun excludesUnknownDuration() {
        assertFalse(ContinueWatching.isEligible(rec(1, durationSec = 0, positionSec = 600)))
    }

    @Test
    fun onlyReadyAndUnlocked() {
        listOf(Recording.SCHEDULED, Recording.WAITING, Recording.RECORDING, Recording.CONVERTING, Recording.FAILED)
            .forEach { assertFalse(it, ContinueWatching.isEligible(rec(1, state = it))) }
        assertFalse(ContinueWatching.isEligible(rec(1, locked = true)))
        assertTrue(ContinueWatching.isEligible(rec(1)))
    }

    // ── Ordering ────────────────────────────────────────────────────────────

    @Test
    fun sortsByLastSavedNewestFirst_thenUnsavedByStart() {
        val rows = listOf(
            rec(1, start = "2026-10-01T00:00:00Z"),
            rec(2, start = "2026-10-03T00:00:00Z", updated = "2026-10-04T10:00:00Z"),
            rec(3, start = "2026-10-02T00:00:00Z"),
            rec(4, start = "2026-10-01T00:00:00Z", updated = "2026-10-04T12:00:00Z"),
            rec(5, positionSec = 0, updated = "2026-10-04T13:00:00Z"),
            rec(6, locked = true, updated = "2026-10-04T14:00:00Z"),
        )
        assertEquals(listOf(4L, 2L, 3L, 1L), ContinueWatching.items(rows).map { it.id })
    }

    @Test
    fun sameSaveTime_fallsBackToStart() {
        val rows = listOf(
            rec(1, start = "2026-10-01T00:00:00Z", updated = "2026-10-04T10:00:00Z"),
            rec(2, start = "2026-10-02T00:00:00Z", updated = "2026-10-04T10:00:00Z"),
        )
        assertEquals(listOf(2L, 1L), ContinueWatching.items(rows).map { it.id })
    }

    @Test
    fun capsAtTen() {
        val rows = (1..15).map { i -> rec(i.toLong(), updated = "2026-10-04T10:%02d:00Z".format(i)) }
        val items = ContinueWatching.items(rows)
        assertEquals(10, ContinueWatching.MAX_ITEMS)
        assertEquals(10, items.size)
        assertEquals(15L, items.first().id)
        assertEquals(6L, items.last().id)
    }

    @Test
    fun nothingEligible_isEmpty() {
        assertEquals(emptyList<Recording>(), ContinueWatching.items(listOf(rec(1, positionSec = 0))))
        assertEquals(emptyList<Recording>(), ContinueWatching.items(emptyList()))
    }

    // ── Copy ────────────────────────────────────────────────────────────────

    @Test
    fun remainingText() {
        assertEquals("less than a minute left", ContinueWatching.remainingText(rec(1, durationSec = 3600, positionSec = 3541)))
        assertEquals("1 min left", ContinueWatching.remainingText(rec(1, durationSec = 3600, positionSec = 3540)))
        assertEquals("23 min left", ContinueWatching.remainingText(rec(1, durationSec = 1980, positionSec = 600)))
        assertEquals("59 min left", ContinueWatching.remainingText(rec(1, durationSec = 3600, positionSec = 1)))
        assertEquals("1 hr left", ContinueWatching.remainingText(rec(1, durationSec = 3660, positionSec = 60)))
        assertEquals("1 hr 5 min left", ContinueWatching.remainingText(rec(1, durationSec = 7200, positionSec = 3300)))
        assertEquals("less than a minute left", ContinueWatching.remainingText(rec(1, durationSec = 600, positionSec = 900)))
    }

    @Test
    fun progress() {
        assertEquals(0.25f, ContinueWatching.progress(rec(1, durationSec = 3600, positionSec = 900)), 0.0001f)
        assertEquals(0f, ContinueWatching.progress(rec(1, durationSec = 0, positionSec = 900)), 0f)
        assertEquals(1f, ContinueWatching.progress(rec(1, durationSec = 600, positionSec = 900)), 0f)
    }
}
