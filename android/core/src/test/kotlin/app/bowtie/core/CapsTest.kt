package app.bowtie.core

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.RuntimeEnvironment
import org.robolectric.annotation.Config

/**
 * Pure [Caps.detect] tests plus [Caps.current] seam wiring via an injected probe.
 * [Caps.audioPassthroughProbe] itself is an untested platform wrapper
 * (honest framing: Robolectric MediaCodecList / AudioManager shadows are weak).
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [28])
class CapsTest {

    @Test
    fun alwaysIncludesH264AndAac() {
        val caps = Caps.detect(emptyList(), ac3Passthrough = false, displayHeight = 1080)
        assertEquals(listOf("h264"), caps.videoCodecs)
        assertEquals(listOf("aac"), caps.audioCodecs)
        assertEquals("", caps.profile)
    }

    @Test
    fun addsHevcWhenHardwareDecoderPresent() {
        val caps = Caps.detect(
            hardwareDecoders = listOf("video/avc", "video/hevc"),
            ac3Passthrough = false,
            displayHeight = 1080,
        )
        assertTrue(caps.videoCodecs.contains("h264"))
        assertTrue(caps.videoCodecs.contains("hevc"))
    }

    @Test
    fun omitsHevcWhenNoHardwareDecoder() {
        val caps = Caps.detect(
            hardwareDecoders = listOf("video/avc", "video/x-vnd.on2.vp9"),
            ac3Passthrough = false,
            displayHeight = 720,
        )
        assertEquals(listOf("h264"), caps.videoCodecs)
    }

    @Test
    fun addsAc3AndEac3WhenPassthrough() {
        val caps = Caps.detect(
            hardwareDecoders = emptyList(),
            ac3Passthrough = true,
            displayHeight = 1080,
        )
        assertEquals(listOf("aac", "ac3", "eac3"), caps.audioCodecs)
    }

    @Test
    fun addsAc3AndEac3WhenDecoderInList() {
        val caps = Caps.detect(
            hardwareDecoders = listOf("audio/ac3", "audio/eac3"),
            ac3Passthrough = false,
            displayHeight = 1080,
        )
        assertTrue(caps.audioCodecs.contains("aac"))
        assertTrue(caps.audioCodecs.contains("ac3"))
        assertTrue(caps.audioCodecs.contains("eac3"))
    }

    @Test
    fun omitsAc3WhenNoPassthroughAndNoDecoder() {
        val caps = Caps.detect(
            hardwareDecoders = listOf("video/avc", "audio/mp4a-latm"),
            ac3Passthrough = false,
            displayHeight = 1080,
        )
        assertEquals(listOf("aac"), caps.audioCodecs)
        assertFalse(caps.audioCodecs.contains("ac3"))
    }

    @Test
    fun maxHeightUsesDisplayHeightWhenBelowCap() {
        val caps = Caps.detect(emptyList(), ac3Passthrough = false, displayHeight = 720)
        assertEquals(720, caps.maxHeight)
    }

    @Test
    fun maxHeightCappedAt1080InV1() {
        val caps = Caps.detect(emptyList(), ac3Passthrough = false, displayHeight = 2160)
        assertEquals(1080, caps.maxHeight)
    }

    @Test
    fun maxHeightZeroWhenDisplayNonPositive() {
        assertEquals(0, Caps.detect(emptyList(), false, 0).maxHeight)
        assertEquals(0, Caps.detect(emptyList(), false, -100).maxHeight)
    }

    @Test
    fun profileIsEmptyForAuto() {
        val caps = Caps.detect(
            hardwareDecoders = listOf("video/hevc"),
            ac3Passthrough = true,
            displayHeight = 1080,
        )
        assertEquals("", caps.profile)
    }

    // ── current() seam: probe wiring (platform probe untested) ──────────────

    @Test
    fun current_probeTrue_includesAc3AndEac3() {
        val context = RuntimeEnvironment.getApplication()
        val caps = Caps.current(context) { true }
        assertTrue(caps.audioCodecs.contains("aac"))
        assertTrue(caps.audioCodecs.contains("ac3"))
        assertTrue(caps.audioCodecs.contains("eac3"))
    }

    @Test
    fun current_probeFalse_aacOnlyWhenNoHardwareAc3() {
        val context = RuntimeEnvironment.getApplication()
        // Injected probe=false; Robolectric typically reports no AC-3 hardware decoder,
        // so detect receives ac3Passthrough=false and yields aac-only (existing behavior).
        val caps = Caps.current(context) { false }
        assertEquals(listOf("aac"), caps.audioCodecs)
        assertFalse(caps.audioCodecs.contains("ac3"))
        assertFalse(caps.audioCodecs.contains("eac3"))
    }
}
