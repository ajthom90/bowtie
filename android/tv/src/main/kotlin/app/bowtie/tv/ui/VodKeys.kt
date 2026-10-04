package app.bowtie.tv.ui

import android.view.KeyEvent
import app.bowtie.core.player.VodPlayer

/**
 * Remote keys for recording playback. Unlike live TV ([PlayerKeyHandler]),
 * up/down don't zap: they show the progress bar. Left/right seek and repeat
 * while held, so holding scrubs.
 */
object VodKeys {
    sealed class Action {
        data object PlayPause : Action()
        data object Play : Action()
        data object Pause : Action()
        data class SeekBy(val ms: Long) : Action()
        data object ShowInfo : Action()
        data object Back : Action()
    }

    data class Result(val handled: Boolean, val action: Action?)

    fun onKey(keyCode: Int, action: Int, repeatCount: Int): Result {
        val mapped: Action = when (keyCode) {
            KeyEvent.KEYCODE_DPAD_CENTER,
            KeyEvent.KEYCODE_ENTER,
            KeyEvent.KEYCODE_NUMPAD_ENTER,
            KeyEvent.KEYCODE_MEDIA_PLAY_PAUSE,
            -> Action.PlayPause
            KeyEvent.KEYCODE_MEDIA_PLAY -> Action.Play
            KeyEvent.KEYCODE_MEDIA_PAUSE -> Action.Pause
            KeyEvent.KEYCODE_DPAD_LEFT,
            KeyEvent.KEYCODE_MEDIA_REWIND,
            -> Action.SeekBy(-VodPlayer.SEEK_BACK_MS)
            KeyEvent.KEYCODE_DPAD_RIGHT,
            KeyEvent.KEYCODE_MEDIA_FAST_FORWARD,
            -> Action.SeekBy(VodPlayer.SEEK_FORWARD_MS)
            KeyEvent.KEYCODE_DPAD_UP,
            KeyEvent.KEYCODE_DPAD_DOWN,
            KeyEvent.KEYCODE_MENU,
            KeyEvent.KEYCODE_INFO,
            -> Action.ShowInfo
            KeyEvent.KEYCODE_BACK -> Action.Back
            else -> return Result(handled = false, action = null)
        }
        if (action != KeyEvent.ACTION_DOWN) return Result(handled = true, action = null)
        // Only seeking repeats while a key is held.
        if (repeatCount > 0 && mapped !is Action.SeekBy) return Result(handled = true, action = null)
        return Result(handled = true, action = mapped)
    }
}
