package app.bowtie.tv.ui

import android.view.KeyEvent
import app.bowtie.tv.ui.VodKeys.Action
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/** DPAD / media keys for recording playback (VOD: no zapping, seek instead). */
class VodKeysTest {

    private fun down(code: Int, repeat: Int = 0) = VodKeys.onKey(code, KeyEvent.ACTION_DOWN, repeat)
    private fun up(code: Int) = VodKeys.onKey(code, KeyEvent.ACTION_UP, 0)

    @Test
    fun centerAndPlayPauseKeys_toggle() {
        for (code in listOf(
            KeyEvent.KEYCODE_DPAD_CENTER,
            KeyEvent.KEYCODE_ENTER,
            KeyEvent.KEYCODE_MEDIA_PLAY_PAUSE,
        )) {
            assertEquals(Action.PlayPause, down(code).action)
            // Holding doesn't toggle again.
            assertNull(down(code, repeat = 1).action)
            assertTrue(down(code, repeat = 1).handled)
        }
    }

    @Test
    fun mediaPlayAndPause_areExplicit() {
        assertEquals(Action.Play, down(KeyEvent.KEYCODE_MEDIA_PLAY).action)
        assertEquals(Action.Pause, down(KeyEvent.KEYCODE_MEDIA_PAUSE).action)
    }

    @Test
    fun leftRight_seekBack10_forward30_andRepeatWhileHeld() {
        assertEquals(Action.SeekBy(-10_000), down(KeyEvent.KEYCODE_DPAD_LEFT).action)
        assertEquals(Action.SeekBy(-10_000), down(KeyEvent.KEYCODE_DPAD_LEFT, repeat = 3).action)
        assertEquals(Action.SeekBy(30_000), down(KeyEvent.KEYCODE_DPAD_RIGHT).action)
        assertEquals(Action.SeekBy(30_000), down(KeyEvent.KEYCODE_MEDIA_FAST_FORWARD).action)
        assertEquals(Action.SeekBy(-10_000), down(KeyEvent.KEYCODE_MEDIA_REWIND).action)
    }

    @Test
    fun up_showsInfo_notZap() {
        assertEquals(Action.ShowInfo, down(KeyEvent.KEYCODE_DPAD_UP).action)
        assertEquals(Action.ShowInfo, down(KeyEvent.KEYCODE_INFO).action)
    }

    @Test
    fun downAndMenu_openTheMenu_once() {
        // Down too: Google TV remotes have no Menu key.
        assertEquals(Action.OpenMenu, down(KeyEvent.KEYCODE_DPAD_DOWN).action)
        assertEquals(Action.OpenMenu, down(KeyEvent.KEYCODE_MENU).action)
        assertNull(down(KeyEvent.KEYCODE_DPAD_DOWN, repeat = 1).action)
    }

    @Test
    fun back_leaves_once() {
        assertEquals(Action.Back, down(KeyEvent.KEYCODE_BACK).action)
        assertNull(down(KeyEvent.KEYCODE_BACK, repeat = 1).action)
    }

    @Test
    fun keyUp_isConsumedWithoutAction() {
        val r = up(KeyEvent.KEYCODE_DPAD_LEFT)
        assertTrue(r.handled)
        assertNull(r.action)
    }

    @Test
    fun unknownKeys_passThrough() {
        val r = down(KeyEvent.KEYCODE_VOLUME_UP)
        assertFalse(r.handled)
        assertNull(r.action)
    }
}
