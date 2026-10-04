package app.bowtie.tv.ui

import android.view.KeyEvent

/**
 * Key mapping for a focused home-screen item (a channel row or a Continue
 * watching card).
 *
 * KEYCODE_MENU (the Fire TV ☰ key) acts once per press ([Outcome.MenuPress]:
 * star / unstar a channel, remove a Continue watching card); its repeats and
 * key-up are swallowed so a held ☰ doesn't act again and again.
 * A long-press of DPAD_CENTER is the rail Surface's own `onLongClick`, so it
 * passes through here like every other key.
 */
object RailKeys {
    enum class Outcome { MenuPress, Consume, PassThrough }

    fun onKey(keyCode: Int, action: Int, repeatCount: Int): Outcome {
        if (keyCode != KeyEvent.KEYCODE_MENU) return Outcome.PassThrough
        return if (action == KeyEvent.ACTION_DOWN && repeatCount == 0) {
            Outcome.MenuPress
        } else {
            Outcome.Consume
        }
    }
}
