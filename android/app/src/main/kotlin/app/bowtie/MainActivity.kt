package app.bowtie

import android.app.PictureInPictureParams
import android.content.res.Configuration
import android.os.Build
import android.os.Bundle
import android.util.Rational
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.activity.viewModels
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.ui.Modifier

/**
 * Host for Activity-level PiP coordination used by [app.bowtie.ui.PlayerScreen].
 * Entering PiP must NOT stop the session; closing the PiP window does.
 */
interface PipHost {
    fun setWantsPip(wants: Boolean)
    fun setOnPipClosed(handler: (() -> Unit)?)
}

class MainActivity : ComponentActivity(), PipHost {

    private val appViewModel: AppViewModel by viewModels {
        AppViewModelFactory(application)
    }

    private var wantsPip: Boolean = false
    private var onPipClosed: (() -> Unit)? = null

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()
        setContent {
            BowtieTheme {
                BowtieRoot(
                    appViewModel = appViewModel,
                    modifier = Modifier.fillMaxSize(),
                )
            }
        }
    }

    override fun setWantsPip(wants: Boolean) {
        wantsPip = wants
    }

    override fun setOnPipClosed(handler: (() -> Unit)?) {
        onPipClosed = handler
    }

    /**
     * Home / recents while playing → enter PiP (session stays alive).
     */
    @Deprecated("Deprecated in Java")
    override fun onUserLeaveHint() {
        super.onUserLeaveHint()
        enterPipIfWanted()
    }

    override fun onPictureInPictureModeChanged(
        isInPictureInPictureMode: Boolean,
        newConfig: Configuration,
    ) {
        super.onPictureInPictureModeChanged(isInPictureInPictureMode, newConfig)
        // Expand-from-PiP: isInPictureInPictureMode=false while still started — keep session.
        // Close-PiP is handled in onStop (activity stops while still reported in PiP).
    }

    /**
     * Closing the PiP window stops the activity while [isInPictureInPictureMode] is still true.
     * Expanding back to full-screen does not call [onStop].
     */
    override fun onStop() {
        super.onStop()
        if (isInPictureInPictureMode) {
            wantsPip = false
            onPipClosed?.invoke()
        }
    }

    private fun enterPipIfWanted() {
        if (!wantsPip || isInPictureInPictureMode) return
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.O) return
        try {
            val params = PictureInPictureParams.Builder()
                .setAspectRatio(Rational(16, 9))
                .build()
            enterPictureInPictureMode(params)
        } catch (_: IllegalStateException) {
            // Device/activity may not support PiP in this state.
        }
    }
}
