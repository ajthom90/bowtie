package app.bowtie.core

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class CommercialSkipperTest {

    private fun c(start: Double, end: Double) = Commercial(start, end)

    private fun recordingJson(extra: String = ""): String =
        """
        {"id":7,"title":"Jeopardy!","channelId":3,"start":"2026-10-05T00:00:00Z",
         "stop":"2026-10-05T00:30:00Z","state":"ready"${if (extra.isEmpty()) "" else ",$extra"}}
        """.trimIndent()

    // ── Decoding ────────────────────────────────────────────────────────────

    @Test
    fun `recording from an older server has no commercials`() {
        val r = BowtieJson.decodeFromString(Recording.serializer(), recordingJson())
        assertEquals(emptyList<Commercial>(), r.commercialSegments)
    }

    @Test
    fun `null commercials decode as none`() {
        val r = BowtieJson.decodeFromString(Recording.serializer(), recordingJson(""""commercials":null"""))
        assertEquals(emptyList<Commercial>(), r.commercialSegments)
    }

    @Test
    fun `recording decodes commercials`() {
        val r = BowtieJson.decodeFromString(
            Recording.serializer(),
            recordingJson(""""commercials":[{"start":312.5,"end":401},{"start":900,"end":1020.25}]"""),
        )
        assertEquals(listOf(c(312.5, 401.0), c(900.0, 1020.25)), r.commercialSegments)
    }

    // ── Normalizing ─────────────────────────────────────────────────────────

    @Test
    fun `sorts segments`() {
        assertEquals(
            listOf(c(100.0, 200.0), c(900.0, 1000.0)),
            CommercialSkipper.normalize(listOf(c(900.0, 1000.0), c(100.0, 200.0))),
        )
    }

    @Test
    fun `merges overlapping and touching segments`() {
        assertEquals(
            listOf(c(100.0, 300.0), c(400.0, 500.0)),
            CommercialSkipper.normalize(
                listOf(c(100.0, 200.0), c(150.0, 250.0), c(250.0, 300.0), c(400.0, 500.0), c(420.0, 450.0)),
            ),
        )
    }

    @Test
    fun `drops empty, backwards and non-finite segments`() {
        assertEquals(
            listOf(c(600.0, 700.0)),
            CommercialSkipper.normalize(
                listOf(
                    c(100.0, 100.0),
                    c(300.0, 200.0),
                    c(Double.NaN, 50.0),
                    c(10.0, Double.POSITIVE_INFINITY),
                    c(Double.NEGATIVE_INFINITY, 5.0),
                    c(600.0, 700.0),
                ),
            ),
        )
    }

    @Test
    fun `clamps a negative start to zero`() {
        assertEquals(listOf(c(0.0, 30.0)), CommercialSkipper.normalize(listOf(c(-5.0, 30.0))))
        assertEquals(emptyList<Commercial>(), CommercialSkipper.normalize(listOf(c(-10.0, -2.0))))
    }

    @Test
    fun `null input normalizes to nothing`() {
        assertEquals(emptyList<Commercial>(), CommercialSkipper.normalize(null))
    }

    // ── Active segment ──────────────────────────────────────────────────────

    @Test
    fun `no segments are never active`() {
        val s = CommercialSkipper(emptyList())
        assertNull(s.active(0.0))
        assertNull(s.active(500.0))
    }

    @Test
    fun `active is start inclusive and end exclusive`() {
        val s = CommercialSkipper(listOf(c(100.0, 200.0), c(500.0, 600.0)))
        assertNull(s.active(99.9))
        assertEquals(c(100.0, 200.0), s.active(100.0))
        assertEquals(c(100.0, 200.0), s.active(199.9))
        assertNull(s.active(200.0))
        assertNull(s.active(350.0))
        assertEquals(c(500.0, 600.0), s.active(550.0))
        assertNull(s.active(600.0))
    }

    @Test
    fun `active uses normalized segments`() {
        val s = CommercialSkipper(listOf(c(250.0, 300.0), c(100.0, 260.0)))
        assertEquals(c(100.0, 300.0), s.active(120.0))
        assertEquals(listOf(c(100.0, 300.0)), s.segments)
    }

    @Test
    fun `non-finite time is never active`() {
        assertNull(CommercialSkipper(listOf(c(0.0, 100.0))).active(Double.NaN))
    }

    // ── Skipping ────────────────────────────────────────────────────────────

    @Test
    fun `skip target is the segment end`() {
        val s = CommercialSkipper(listOf(c(100.0, 200.0)))
        assertEquals(200.0, s.skip(150.0)!!, 0.0)
        assertNull(s.skip(250.0))
    }

    @Test
    fun `auto-skips each segment once`() {
        val s = CommercialSkipper(listOf(c(100.0, 200.0), c(500.0, 600.0)))
        assertNull(s.autoSkipTarget(50.0))
        assertEquals(200.0, s.autoSkipTarget(100.2)!!, 0.0)
        assertEquals(600.0, s.autoSkipTarget(500.0)!!, 0.0)
    }

    @Test
    fun `seeking back into an auto-skipped segment does not skip again`() {
        val s = CommercialSkipper(listOf(c(100.0, 200.0)))
        assertEquals(200.0, s.autoSkipTarget(101.0)!!, 0.0)
        assertNull(s.autoSkipTarget(150.0))
        // Still inside it, so the Skip ad button still shows and still works.
        assertEquals(c(100.0, 200.0), s.active(150.0))
        assertEquals(200.0, s.skip(150.0)!!, 0.0)
    }

    @Test
    fun `manual skip counts as handled`() {
        val s = CommercialSkipper(listOf(c(100.0, 200.0)))
        assertEquals(200.0, s.skip(120.0)!!, 0.0)
        assertNull(s.autoSkipTarget(120.0))
    }

    @Test
    fun `merged segment is skipped in one go`() {
        val s = CommercialSkipper(listOf(c(100.0, 200.0), c(200.0, 260.0)))
        assertEquals(260.0, s.autoSkipTarget(100.0)!!, 0.0)
        assertNull(s.autoSkipTarget(210.0))
    }
}
