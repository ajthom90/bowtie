package app.bowtie.tv.ui

import android.view.KeyEvent

/**
 * Key mapping for a focused channel-rail item.
 *
 * KEYCODE_MENU (the Fire TV ☰ key) toggles the favorite once per press; its
 * repeats and key-up are swallowed so a held ☰ doesn't flip it back and forth.
 * A long-press of DPAD_CENTER is the rail Surface's own `onLongClick`, so it
 * passes through here like every other key.
 */
object RailKeys {
    enum class Outcome { ToggleFavorite, Consume, PassThrough }

    fun onKey(keyCode: Int, action: Int, repeatCount: Int): Outcome {
        if (keyCode != KeyEvent.KEYCODE_MENU) return Outcome.PassThrough
        return if (action == KeyEvent.ACTION_DOWN && repeatCount == 0) {
            Outcome.ToggleFavorite
        } else {
            Outcome.Consume
        }
    }
}
