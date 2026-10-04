package app.bowtie.tv.ui

import android.view.KeyEvent
import org.junit.Assert.assertEquals
import org.junit.Test

class ContinueCardKeysTest {

    @Test
    fun menuPress_opensTheOptions_neverRemoves() {
        // One ☰ press must not drop a card: it opens the same options as holding OK.
        assertEquals(
            ContinueCardKeys.Outcome.OpenOptions,
            ContinueCardKeys.onKey(KeyEvent.KEYCODE_MENU, KeyEvent.ACTION_DOWN, repeatCount = 0),
        )
        assertEquals(
            setOf(ContinueCardKeys.Outcome.OpenOptions, ContinueCardKeys.Outcome.Consume, ContinueCardKeys.Outcome.PassThrough),
            ContinueCardKeys.Outcome.entries.toSet(),
        )
    }

    @Test
    fun heldMenu_andItsKeyUp_areSwallowed() {
        assertEquals(
            ContinueCardKeys.Outcome.Consume,
            ContinueCardKeys.onKey(KeyEvent.KEYCODE_MENU, KeyEvent.ACTION_DOWN, repeatCount = 1),
        )
        assertEquals(
            ContinueCardKeys.Outcome.Consume,
            ContinueCardKeys.onKey(KeyEvent.KEYCODE_MENU, KeyEvent.ACTION_UP, repeatCount = 0),
        )
    }

    @Test
    fun otherKeys_passThrough() {
        for (key in listOf(KeyEvent.KEYCODE_DPAD_CENTER, KeyEvent.KEYCODE_DPAD_LEFT, KeyEvent.KEYCODE_BACK)) {
            assertEquals(
                ContinueCardKeys.Outcome.PassThrough,
                ContinueCardKeys.onKey(key, KeyEvent.ACTION_DOWN, repeatCount = 0),
            )
        }
    }
}
