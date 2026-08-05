package app.bowtie.core

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test
import java.time.Instant

/**
 * Mirrors iOS GuideLogicTests fixtures:
 * nowNext mid-program, exclusive stop boundary, gap → next only, empty;
 * allowedProfiles "", "medium", "original", unknown.
 */
class GuideLogicTest {

    private fun prog(
        start: String,
        stop: String,
        title: String = "Show",
    ): GuideProgram = GuideProgram(
        start = Instant.parse(start),
        stop = Instant.parse(stop),
        title = title,
        subtitle = "",
        description = "",
        category = "",
    )

    // ── nowNext ──────────────────────────────────────────────────────────

    @Test
    fun nowNext_midProgram() {
        val a = prog("2026-08-05T18:00:00Z", "2026-08-05T19:00:00Z", "News")
        val b = prog("2026-08-05T19:00:00Z", "2026-08-05T20:00:00Z", "Weather")
        val at = Instant.parse("2026-08-05T18:30:00Z")

        val nn = GuideLogic.nowNext(listOf(a, b), at)
        assertEquals(a, nn.now)
        assertEquals(b, nn.next)
    }

    @Test
    fun nowNext_exactStopBoundaryIsExclusive() {
        // Program ends at 19:00; at exactly 19:00 it is NOT current.
        val a = prog("2026-08-05T18:00:00Z", "2026-08-05T19:00:00Z", "News")
        val b = prog("2026-08-05T19:00:00Z", "2026-08-05T20:00:00Z", "Weather")
        val at = Instant.parse("2026-08-05T19:00:00Z")

        val nn = GuideLogic.nowNext(listOf(a, b), at)
        assertEquals(b, nn.now)
        assertNull(nn.next)
    }

    @Test
    fun nowNext_gapYieldsNextOnly() {
        // Gap between A stop and B start; at sits in the hole.
        val a = prog("2026-08-05T17:00:00Z", "2026-08-05T18:00:00Z", "Earlier")
        val b = prog("2026-08-05T19:00:00Z", "2026-08-05T20:00:00Z", "Later")
        val at = Instant.parse("2026-08-05T18:30:00Z")

        val nn = GuideLogic.nowNext(listOf(a, b), at)
        assertNull(nn.now)
        assertEquals(b, nn.next)
    }

    @Test
    fun nowNext_emptyPrograms() {
        val at = Instant.parse("2026-08-05T18:00:00Z")
        val nn = GuideLogic.nowNext(emptyList(), at)
        assertNull(nn.now)
        assertNull(nn.next)
    }

    @Test
    fun nowNext_onlyNowNoNext() {
        val a = prog("2026-08-05T18:00:00Z", "2026-08-05T19:00:00Z", "Solo")
        val at = Instant.parse("2026-08-05T18:15:00Z")

        val nn = GuideLogic.nowNext(listOf(a), at)
        assertEquals(a, nn.now)
        assertNull(nn.next)
    }

    @Test
    fun nowNext_startBoundaryIsInclusive() {
        val a = prog("2026-08-05T18:00:00Z", "2026-08-05T19:00:00Z", "OnAir")
        val at = Instant.parse("2026-08-05T18:00:00Z")

        val nn = GuideLogic.nowNext(listOf(a), at)
        assertEquals(a, nn.now)
    }

    // ── allowedProfiles ──────────────────────────────────────────────────

    @Test
    fun allowedProfiles_emptyMeansAll() {
        assertEquals(
            listOf("original", "high", "medium", "low"),
            GuideLogic.allowedProfiles(""),
        )
    }

    @Test
    fun allowedProfiles_mediumAndBelow() {
        assertEquals(
            listOf("medium", "low"),
            GuideLogic.allowedProfiles("medium"),
        )
    }

    @Test
    fun allowedProfiles_originalMeansAll() {
        assertEquals(
            listOf("original", "high", "medium", "low"),
            GuideLogic.allowedProfiles("original"),
        )
    }

    @Test
    fun allowedProfiles_unknownMeansAllDefensive() {
        assertEquals(
            listOf("original", "high", "medium", "low"),
            GuideLogic.allowedProfiles("ultra"),
        )
        assertEquals(
            listOf("original", "high", "medium", "low"),
            GuideLogic.allowedProfiles("720p"),
        )
    }

    @Test
    fun allowedProfiles_highAndBelow() {
        assertEquals(
            listOf("high", "medium", "low"),
            GuideLogic.allowedProfiles("high"),
        )
    }

    @Test
    fun allowedProfiles_lowOnly() {
        assertEquals(
            listOf("low"),
            GuideLogic.allowedProfiles("low"),
        )
    }
}
