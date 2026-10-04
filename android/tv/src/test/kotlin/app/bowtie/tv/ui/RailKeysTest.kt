package app.bowtie.tv.ui

import android.view.KeyEvent
import org.junit.Assert.assertEquals
import org.junit.Test

class RailKeysTest {

    @Test
    fun menuDownActsOnce() {
        assertEquals(
            RailKeys.Outcome.MenuPress,
            RailKeys.onKey(KeyEvent.KEYCODE_MENU, KeyEvent.ACTION_DOWN, repeatCount = 0),
        )
    }

    @Test
    fun heldMenuRepeatsAndMenuUpAreSwallowed() {
        assertEquals(
            RailKeys.Outcome.Consume,
            RailKeys.onKey(KeyEvent.KEYCODE_MENU, KeyEvent.ACTION_DOWN, repeatCount = 3),
        )
        assertEquals(
            RailKeys.Outcome.Consume,
            RailKeys.onKey(KeyEvent.KEYCODE_MENU, KeyEvent.ACTION_UP, repeatCount = 0),
        )
    }

    @Test
    fun otherKeysPassThrough() {
        // DPAD_CENTER long-press is the Surface's onLongClick; the rail must not steal it.
        for (code in listOf(
            KeyEvent.KEYCODE_DPAD_CENTER,
            KeyEvent.KEYCODE_ENTER,
            KeyEvent.KEYCODE_DPAD_UP,
            KeyEvent.KEYCODE_DPAD_DOWN,
            KeyEvent.KEYCODE_BACK,
        )) {
            assertEquals(
                RailKeys.Outcome.PassThrough,
                RailKeys.onKey(code, KeyEvent.ACTION_DOWN, repeatCount = 0),
            )
        }
    }
}
