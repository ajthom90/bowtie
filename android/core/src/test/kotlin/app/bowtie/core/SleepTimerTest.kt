package app.bowtie.core

import app.bowtie.core.SleepTimer.Option
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test

/** Sleep timer on a fake clock: no real waiting. */
class SleepTimerTest {

    private var nowMs = 1_700_000_000_000L
    private var fired = 0
    private lateinit var timer: SleepTimer

    private fun advance(ms: Long) {
        nowMs += ms
    }

    private val min = 60_000L

    @Before
    fun setUp() {
        fired = 0
        timer = SleepTimer(nowMs = { nowMs }, onFire = { fired++ })
    }

    @Test
    fun startsOff() {
        val s = timer.status.value
        assertEquals(Option.OFF, s.option)
        assertNull(s.remainingMs)
        assertFalse(s.isActive)
        assertFalse(s.warning)
    }

    @Test
    fun remainingTime_countsDown() {
        assertTrue(timer.start(Option.MIN_30))
        assertEquals(Option.MIN_30, timer.status.value.option)
        assertEquals(30 * min, timer.status.value.remainingMs)
        advance(61_000)
        timer.tick()
        assertEquals(30 * min - 61_000, timer.status.value.remainingMs)
        assertTrue(timer.status.value.isActive)
    }

    @Test
    fun warning_startsSixtySecondsBeforeFiring() {
        timer.start(Option.MIN_15)
        advance(15 * min - 61_000)
        timer.tick()
        assertFalse(timer.status.value.warning)
        advance(1_000)
        timer.tick()
        assertTrue("warning at T-60s", timer.status.value.warning)
        assertEquals(60_000L, timer.status.value.remainingMs)
        assertEquals(0, fired)
    }

    @Test
    fun extend_addsTheChosenDuration() {
        timer.start(Option.MIN_15)
        advance(15 * min - 30_000)
        timer.tick()
        assertTrue(timer.status.value.warning)
        timer.extend()
        assertFalse(timer.status.value.warning)
        assertEquals(15 * min + 30_000, timer.status.value.remainingMs)
        assertEquals(Option.MIN_15, timer.status.value.option)
        advance(31_000)
        timer.tick()
        assertEquals("the original deadline no longer fires", 0, fired)
    }

    @Test
    fun extend_endOfProgram_addsThirtyMinutes() {
        timer.start(Option.END_OF_PROGRAM, programEndMs = nowMs + 45_000)
        assertTrue(timer.status.value.warning)
        timer.extend()
        assertEquals(45_000 + 30 * min, timer.status.value.remainingMs)
    }

    @Test
    fun extend_whenOff_doesNothing() {
        timer.extend()
        assertNull(timer.status.value.remainingMs)
    }

    @Test
    fun cancel_stopsTheTimer() {
        timer.start(Option.MIN_45)
        timer.cancel()
        assertEquals(Option.OFF, timer.status.value.option)
        assertNull(timer.status.value.remainingMs)
        advance(46 * min)
        timer.tick()
        assertEquals(0, fired)
    }

    @Test
    fun startingOff_cancels() {
        timer.start(Option.MIN_45)
        timer.start(Option.OFF)
        assertFalse(timer.status.value.isActive)
    }

    @Test
    fun endOfProgram_usesTheProgramEnd() {
        val end = nowMs + 22 * min
        assertTrue(timer.start(Option.END_OF_PROGRAM, programEndMs = end))
        assertEquals(Option.END_OF_PROGRAM, timer.status.value.option)
        assertEquals(22 * min, timer.status.value.remainingMs)
        advance(22 * min)
        timer.tick()
        assertEquals(1, fired)
    }

    @Test
    fun endOfProgram_unavailableWhenEndUnknownOrPast() {
        assertFalse(Option.END_OF_PROGRAM in SleepTimer.options(programEndMs = null, nowMs = nowMs))
        assertFalse(Option.END_OF_PROGRAM in SleepTimer.options(programEndMs = nowMs, nowMs = nowMs))
        assertFalse(Option.END_OF_PROGRAM in SleepTimer.options(programEndMs = nowMs - 10, nowMs = nowMs))
        assertEquals(
            listOf(
                Option.OFF, Option.MIN_15, Option.MIN_30, Option.MIN_45,
                Option.MIN_60, Option.MIN_90, Option.HOURS_2, Option.END_OF_PROGRAM,
            ),
            SleepTimer.options(programEndMs = nowMs + 60_000, nowMs = nowMs),
        )

        assertFalse(timer.start(Option.END_OF_PROGRAM, programEndMs = null))
        assertFalse(timer.status.value.isActive)
        assertFalse(timer.start(Option.END_OF_PROGRAM, programEndMs = nowMs - 1))
        assertFalse(timer.status.value.isActive)
    }

    @Test
    fun fire_callsStopExactlyOnce() {
        timer.start(Option.MIN_15)
        advance(15 * min)
        timer.tick()
        assertEquals(1, fired)
        assertFalse(timer.status.value.isActive)
        assertEquals(Option.OFF, timer.status.value.option)
        advance(5_000)
        timer.tick()
        timer.tick()
        assertEquals(1, fired)
    }

    @Test
    fun fires_whenTickIsLate() {
        timer.start(Option.MIN_15)
        advance(20 * min) // e.g. the device slept
        timer.tick()
        assertEquals(1, fired)
    }

    @Test
    fun labels() {
        assertEquals("Off", Option.OFF.label)
        assertEquals("15 minutes", Option.MIN_15.label)
        assertEquals("60 minutes", Option.MIN_60.label)
        assertEquals("90 minutes", Option.MIN_90.label)
        assertEquals("2 hours", Option.HOURS_2.label)
        assertEquals("End of this program", Option.END_OF_PROGRAM.label)
    }

    @Test
    fun format() {
        assertEquals("1:00", SleepTimer.format(60_000))
        assertEquals("rounds up", "1:00", SleepTimer.format(59_400))
        assertEquals("0:05", SleepTimer.format(5_000))
        assertEquals("0:00", SleepTimer.format(0))
        assertEquals("29:41", SleepTimer.format((29 * 60 + 41) * 1000L))
        assertEquals("2:00:00", SleepTimer.format(2 * 3_600_000L))
        assertEquals("1:01:05", SleepTimer.format((3600 + 65) * 1000L))
    }

    @Test
    fun promptCopy() {
        assertEquals("Still watching? Sleeping in 1:00", SleepTimer.promptText(60_000))
    }
}
