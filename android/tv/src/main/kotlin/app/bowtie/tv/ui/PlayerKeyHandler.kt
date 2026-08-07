package app.bowtie.tv.ui

import android.view.KeyEvent

/**
 * Stateful DPAD key handler for [TvPlayerScreen].
 *
 * - DPAD_CENTER short-press (&lt; [longPressThresholdMs]): play/pause
 * - DPAD_CENTER long-press (≥ threshold) or KEYCODE_MENU: open transport/quality drawer
 * - DPAD_UP / DPAD_DOWN: channel zap
 * - DPAD_LEFT / DPAD_RIGHT: seek ∓30s while controls (drawer) are hidden (spec D)
 * - BACK: leave player
 *
 * Long-press is a **state machine** (KeyDown timestamp → KeyUp duration), not a
 * pure KeyEvent→Action map. Inject [nowMs] for unit tests.
 *
 * Use with Compose **onPreviewKeyEvent** (capture phase) so PlayerView never
 * steals the event.
 */
class PlayerKeyHandler(
    private val longPressThresholdMs: Long = LONG_PRESS_MS,
    private val nowMs: () -> Long = System::currentTimeMillis,
) {
    sealed class Action {
        data object PlayPause : Action()
        data object OpenDrawer : Action()
        data object ZapUp : Action()
        data object ZapDown : Action()
        data object SeekBack30 : Action()
        data object SeekForward30 : Action()
        data object Back : Action()
    }

    /** Timestamp of the current DPAD_CENTER down, or null when not held. */
    private var centerDownAtMs: Long? = null

    /**
     * Process a key event.
     *
     * @return [Result.handled] true when the event is consumed; [Result.action]
     *   non-null when the host should perform a side effect.
     */
    fun onKey(
        keyCode: Int,
        action: Int,
        isLongPress: Boolean = false,
        repeatCount: Int = 0,
    ): Result {
        when (keyCode) {
            KeyEvent.KEYCODE_DPAD_CENTER,
            KeyEvent.KEYCODE_ENTER,
            -> return handleCenter(action, isLongPress, repeatCount)

            KeyEvent.KEYCODE_MENU -> {
                if (action == KeyEvent.ACTION_DOWN && repeatCount == 0) {
                    centerDownAtMs = null
                    return Result(handled = true, action = Action.OpenDrawer)
                }
                return Result(handled = true, action = null)
            }

            KeyEvent.KEYCODE_DPAD_UP -> {
                if (action == KeyEvent.ACTION_DOWN && repeatCount == 0) {
                    return Result(handled = true, action = Action.ZapUp)
                }
                return Result(handled = true, action = null)
            }

            KeyEvent.KEYCODE_DPAD_DOWN -> {
                if (action == KeyEvent.ACTION_DOWN && repeatCount == 0) {
                    return Result(handled = true, action = Action.ZapDown)
                }
                return Result(handled = true, action = null)
            }

            KeyEvent.KEYCODE_DPAD_LEFT -> {
                if (action == KeyEvent.ACTION_DOWN && repeatCount == 0) {
                    return Result(handled = true, action = Action.SeekBack30)
                }
                return Result(handled = true, action = null)
            }

            KeyEvent.KEYCODE_DPAD_RIGHT -> {
                if (action == KeyEvent.ACTION_DOWN && repeatCount == 0) {
                    return Result(handled = true, action = Action.SeekForward30)
                }
                return Result(handled = true, action = null)
            }

            KeyEvent.KEYCODE_BACK -> {
                if (action == KeyEvent.ACTION_DOWN && repeatCount == 0) {
                    centerDownAtMs = null
                    return Result(handled = true, action = Action.Back)
                }
                return Result(handled = true, action = null)
            }
        }
        return Result(handled = false, action = null)
    }

    /** Clear in-progress center press (e.g. when drawer opens from elsewhere). */
    fun reset() {
        centerDownAtMs = null
    }

    private fun handleCenter(
        action: Int,
        isLongPress: Boolean,
        repeatCount: Int,
    ): Result {
        when (action) {
            KeyEvent.ACTION_DOWN -> {
                if (centerDownAtMs == null) {
                    centerDownAtMs = nowMs()
                }
                // Platform long-press flag or repeat after threshold → drawer early.
                if (isLongPress ||
                    (repeatCount > 0 && heldForLongPress())
                ) {
                    centerDownAtMs = null
                    return Result(handled = true, action = Action.OpenDrawer)
                }
                return Result(handled = true, action = null)
            }
            KeyEvent.ACTION_UP -> {
                val downAt = centerDownAtMs
                centerDownAtMs = null
                if (downAt == null) {
                    return Result(handled = true, action = null)
                }
                val held = nowMs() - downAt
                val act = if (held >= longPressThresholdMs) {
                    Action.OpenDrawer
                } else {
                    Action.PlayPause
                }
                return Result(handled = true, action = act)
            }
            else -> return Result(handled = true, action = null)
        }
    }

    private fun heldForLongPress(): Boolean {
        val downAt = centerDownAtMs ?: return false
        return nowMs() - downAt >= longPressThresholdMs
    }

    data class Result(
        val handled: Boolean,
        val action: Action?,
    )

    companion object {
        const val LONG_PRESS_MS = 700L
    }
}
