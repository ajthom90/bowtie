package app.bowtie.core

import app.bowtie.core.player.TrackPrefs
import app.bowtie.core.player.audioLabel
import org.junit.Test
import org.junit.Assert.assertEquals

class TrackPrefsTest {
    @Test fun roundTrip() {
        val p = TrackPrefs(audioLanguage = "es", captionsOn = true)
        assertEquals(p, TrackPrefs.decode(p.encode()))
        assertEquals(TrackPrefs(), TrackPrefs.decode(TrackPrefs().encode()))
    }

    @Test fun decodeGarbageIsEmpty() {
        assertEquals(TrackPrefs(), TrackPrefs.decode(null))
        assertEquals(TrackPrefs(), TrackPrefs.decode("{not json"))
    }

    @Test fun labels() {
        assertEquals("Español", audioLabel("es", "Español", 1))
        assertEquals("es", audioLabel("es", null, 1))
        assertEquals("Audio 2", audioLabel(null, null, 1))
    }
}

class NextAudioTest {
    private val opts = listOf(
        app.bowtie.core.player.AudioOption("0:0", "en", "English"),
        app.bowtie.core.player.AudioOption("1:0", "es", "Español"),
    )

    @Test fun cyclesAndWraps() {
        assertEquals("1:0", app.bowtie.core.player.nextAudio(opts, "0:0")?.id)
        assertEquals("0:0", app.bowtie.core.player.nextAudio(opts, "1:0")?.id)
        assertEquals("0:0", app.bowtie.core.player.nextAudio(opts, null)?.id)
    }

    @Test fun singleOptionHasNoNext() {
        assertEquals(null, app.bowtie.core.player.nextAudio(opts.take(1), "0:0"))
    }

    @Test fun twoTracksInOneLanguageAreBothReachable() {
        val same = listOf(
            app.bowtie.core.player.AudioOption("0:0", "en", "English"),
            app.bowtie.core.player.AudioOption("1:0", "en", "Described video"),
        )
        assertEquals("1:0", app.bowtie.core.player.nextAudio(same, "0:0")?.id)
    }
}
