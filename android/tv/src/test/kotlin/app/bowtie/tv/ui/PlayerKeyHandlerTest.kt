package app.bowtie.tv.ui

import android.view.KeyEvent
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * Stateful long-press machine with an injected clock (A1).
 * Sequences: down/up timing, MENU, zap, BACK.
 */
class PlayerKeyHandlerTest {

    private var clock = 0L
    private fun handler(thresholdMs: Long = 700L) =
        PlayerKeyHandler(longPressThresholdMs = thresholdMs, nowMs = { clock })

    @Test
    fun centerShortPress_playPause() {
        val h = handler()
        clock = 1_000L
        val down = h.onKey(KeyEvent.KEYCODE_DPAD_CENTER, KeyEvent.ACTION_DOWN)
        assertTrue(down.handled)
        assertNull(down.action)

        clock = 1_400L // 400ms < 700
        val up = h.onKey(KeyEvent.KEYCODE_DPAD_CENTER, KeyEvent.ACTION_UP)
        assertTrue(up.handled)
        assertEquals(PlayerKeyHandler.Action.PlayPause, up.action)
    }

    @Test
    fun centerLongPress_openDrawer() {
        val h = handler()
        clock = 1_000L
        h.onKey(KeyEvent.KEYCODE_DPAD_CENTER, KeyEvent.ACTION_DOWN)

        clock = 1_700L // exactly 700ms
        val up = h.onKey(KeyEvent.KEYCODE_DPAD_CENTER, KeyEvent.ACTION_UP)
        assertEquals(PlayerKeyHandler.Action.OpenDrawer, up.action)
    }

    @Test
    fun centerLongPress_overThreshold_openDrawer() {
        val h = handler()
        clock = 0L
        h.onKey(KeyEvent.KEYCODE_DPAD_CENTER, KeyEvent.ACTION_DOWN)
        clock = 1_200L
        val up = h.onKey(KeyEvent.KEYCODE_DPAD_CENTER, KeyEvent.ACTION_UP)
        assertEquals(PlayerKeyHandler.Action.OpenDrawer, up.action)
    }

    @Test
    fun centerLongPressFlag_openDrawerEarly() {
        val h = handler()
        clock = 0L
        h.onKey(KeyEvent.KEYCODE_DPAD_CENTER, KeyEvent.ACTION_DOWN)
        clock = 500L
        val longDown = h.onKey(
            keyCode = KeyEvent.KEYCODE_DPAD_CENTER,
            action = KeyEvent.ACTION_DOWN,
            isLongPress = true,
        )
        assertEquals(PlayerKeyHandler.Action.OpenDrawer, longDown.action)
    }

    @Test
    fun centerRepeatAfterThreshold_openDrawer() {
        val h = handler()
        clock = 0L
        h.onKey(KeyEvent.KEYCODE_DPAD_CENTER, KeyEvent.ACTION_DOWN)
        clock = 800L
        val repeat = h.onKey(
            keyCode = KeyEvent.KEYCODE_DPAD_CENTER,
            action = KeyEvent.ACTION_DOWN,
            repeatCount = 1,
        )
        assertEquals(PlayerKeyHandler.Action.OpenDrawer, repeat.action)
    }

    @Test
    fun menuKeyDown_openDrawer() {
        val h = handler()
        val r = h.onKey(KeyEvent.KEYCODE_MENU, KeyEvent.ACTION_DOWN)
        assertTrue(r.handled)
        assertEquals(PlayerKeyHandler.Action.OpenDrawer, r.action)
    }

    @Test
    fun dpadUp_zapUp() {
        val h = handler()
        val r = h.onKey(KeyEvent.KEYCODE_DPAD_UP, KeyEvent.ACTION_DOWN)
        assertEquals(PlayerKeyHandler.Action.ZapUp, r.action)
    }

    @Test
    fun dpadDown_zapDown() {
        val h = handler()
        val r = h.onKey(KeyEvent.KEYCODE_DPAD_DOWN, KeyEvent.ACTION_DOWN)
        assertEquals(PlayerKeyHandler.Action.ZapDown, r.action)
    }

    @Test
    fun back_backAction() {
        val h = handler()
        val r = h.onKey(KeyEvent.KEYCODE_BACK, KeyEvent.ACTION_DOWN)
        assertEquals(PlayerKeyHandler.Action.Back, r.action)
    }

    @Test
    fun enterShortPress_playPause() {
        val h = handler()
        clock = 10L
        h.onKey(KeyEvent.KEYCODE_ENTER, KeyEvent.ACTION_DOWN)
        clock = 100L
        val up = h.onKey(KeyEvent.KEYCODE_ENTER, KeyEvent.ACTION_UP)
        assertEquals(PlayerKeyHandler.Action.PlayPause, up.action)
    }

    @Test
    fun unknownKey_notHandled() {
        val h = handler()
        val r = h.onKey(KeyEvent.KEYCODE_A, KeyEvent.ACTION_DOWN)
        assertFalse(r.handled)
        assertNull(r.action)
    }

    @Test
    fun keyUpWithoutDown_noAction() {
        val h = handler()
        val up = h.onKey(KeyEvent.KEYCODE_DPAD_CENTER, KeyEvent.ACTION_UP)
        assertTrue(up.handled)
        assertNull(up.action)
    }

    @Test
    fun zapIgnoresRepeats() {
        val h = handler()
        val first = h.onKey(KeyEvent.KEYCODE_DPAD_UP, KeyEvent.ACTION_DOWN, repeatCount = 0)
        assertEquals(PlayerKeyHandler.Action.ZapUp, first.action)
        val repeat = h.onKey(KeyEvent.KEYCODE_DPAD_UP, KeyEvent.ACTION_DOWN, repeatCount = 1)
        assertTrue(repeat.handled)
        assertNull(repeat.action)
    }

    @Test
    fun resetClearsCenterHold() {
        val h = handler()
        clock = 0L
        h.onKey(KeyEvent.KEYCODE_DPAD_CENTER, KeyEvent.ACTION_DOWN)
        h.reset()
        clock = 50L
        val up = h.onKey(KeyEvent.KEYCODE_DPAD_CENTER, KeyEvent.ACTION_UP)
        assertNull(up.action)
    }
}
