package app.bowtie.core

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

/** Player copy for antenna reception: quality leads (strength can be high while the picture breaks up). */
class SignalCopyTest {

    private val weak = SignalReading(strength = 96, quality = 46, symbolQuality = 0, weak = true)
    private val good = SignalReading(strength = 100, quality = 98, symbolQuality = 100, weak = false)

    @Test
    fun statsLineLeadsWithQuality() {
        assertEquals("Signal quality 46% · strength 96% · error-free 0%", SignalCopy.statsLine(weak))
        assertEquals("Signal quality 98% · strength 100% · error-free 100%", SignalCopy.statsLine(good))
    }

    @Test
    fun statsLineHiddenWhenUnknown() {
        assertNull(SignalCopy.statsLine(null))
    }

    @Test
    fun weakNoteCarriesQuality() {
        assertEquals("Weak signal (46%) — the picture may break up.", SignalCopy.weakNote(weak))
    }

    @Test
    fun weakNoteOnlyWhenWeak() {
        assertNull(SignalCopy.weakNote(good))
        assertNull(SignalCopy.weakNote(null))
    }

    @Test
    fun outOfRangePercentsAreClamped() {
        assertEquals(
            "Signal quality 100% · strength 0% · error-free 50%",
            SignalCopy.statsLine(SignalReading(strength = -3, quality = 140, symbolQuality = 50)),
        )
    }
}
