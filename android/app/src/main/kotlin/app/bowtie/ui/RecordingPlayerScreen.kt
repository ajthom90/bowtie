package app.bowtie.ui

import android.app.Activity
import android.view.ViewGroup
import android.view.WindowManager
import androidx.activity.compose.BackHandler
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.media3.common.PlaybackException
import androidx.media3.common.Player
import androidx.media3.common.util.UnstableApi
import androidx.media3.ui.PlayerView
import app.bowtie.BowtieColors
import app.bowtie.BowtieType
import app.bowtie.core.Commercial
import app.bowtie.core.CommercialSkipper
import app.bowtie.core.RecordingLogic
import app.bowtie.core.player.AutoSkipAdsStore
import app.bowtie.core.player.VodPlayer
import app.bowtie.core.vm.RecordingsViewModel
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import okhttp3.HttpUrl

/** How often the player checks for a commercial break. */
private const val COMMERCIAL_CHECK_MS = 500L

/**
 * Plays a recording as HLS VOD with Media3's seekable controller, starting at
 * [startAtSec]. Saves the resume position every 15 s, when backgrounded, at the
 * end, and on exit. Inside a detected commercial break it offers Skip ad, and
 * skips each break once by itself when Skip ads automatically is on.
 */
@OptIn(UnstableApi::class)
@Composable
fun RecordingPlayerScreen(
    start: RecordingsViewModel.PlayStart,
    startAtSec: Int,
    server: HttpUrl,
    viewModel: RecordingsViewModel,
    onBack: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val context = LocalContext.current
    val activity = context as? Activity
    val lifecycleOwner = LocalLifecycleOwner.current
    val recordingId = start.recording.id
    val player = remember { VodPlayer.create(context) }
    var error by remember { mutableStateOf<String?>(null) }
    var chromeVisible by remember { mutableStateOf(true) }
    var lastSavedMs by remember { mutableStateOf(-1L) }
    var retrying by remember { mutableStateOf(false) }
    val scope = rememberCoroutineScope()
    // Per screen, not per load: a retry (fresh playlist) doesn't re-skip breaks.
    val skipper = remember { CommercialSkipper(start.recording.commercials) }
    val autoSkipAds = remember { AutoSkipAdsStore(context) }
    var activeAd by remember { mutableStateOf<Commercial?>(null) }
    var skippedNonce by remember { mutableIntStateOf(0) }
    var showSkipped by remember { mutableStateOf(false) }

    /** Seek to a break's end (exact: Media3's default seek). */
    fun seekPastAd(targetSec: Double) {
        activeAd = null
        player.seekTo((targetSec * 1000).toLong())
    }

    /** Ask for a fresh playlist (tokens expire) and pick up where playback stopped. */
    fun retry() {
        if (retrying) return
        val atSec = RecordingLogic.retryStartSec(player.currentPosition, startAtSec)
        retrying = true
        error = null
        scope.launch {
            try {
                when (val r = viewModel.retryPlayback(start.recording)) {
                    is RecordingsViewModel.Retry.Ready -> VodPlayer.load(player, r.playlistUrl, server, atSec)
                    is RecordingsViewModel.Retry.Failed -> error = r.message
                }
            } finally {
                retrying = false
            }
        }
    }

    fun save() {
        // Before the first frame the position is 0: don't wipe a saved resume point.
        if (player.playbackState == Player.STATE_IDLE && player.currentPosition == 0L) return
        val pos = player.currentPosition
        if (pos == lastSavedMs) return
        lastSavedMs = pos
        viewModel.savePosition(recordingId, pos)
    }

    LaunchedEffect(player) {
        VodPlayer.load(player, start.playlistUrl, server, startAtSec)
    }

    DisposableEffect(player) {
        val listener = object : Player.Listener {
            override fun onPlayerError(e: PlaybackException) {
                error = "This recording couldn't be played."
            }

            override fun onPlaybackStateChanged(state: Int) {
                if (state == Player.STATE_ENDED) save()
            }
        }
        player.addListener(listener)
        onDispose {
            player.removeListener(listener)
            save()
            player.release()
        }
    }

    // Save and pause when the app goes to the background.
    DisposableEffect(lifecycleOwner, player) {
        val observer = LifecycleEventObserver { _, event ->
            if (event == Lifecycle.Event.ON_STOP) {
                save()
                player.pause()
            }
        }
        lifecycleOwner.lifecycle.addObserver(observer)
        onDispose { lifecycleOwner.lifecycle.removeObserver(observer) }
    }

    LaunchedEffect(player) {
        while (isActive) {
            delay(RecordingLogic.POSITION_SAVE_INTERVAL_MS)
            if (player.isPlaying) save()
        }
    }

    // Commercial breaks: Skip ad while inside one; auto-skip (only while
    // playing, so scrubbing while paused never jumps) once per break.
    LaunchedEffect(player) {
        if (skipper.segments.isEmpty()) return@LaunchedEffect
        while (isActive) {
            delay(COMMERCIAL_CHECK_MS)
            val atSec = player.currentPosition / 1000.0
            if (autoSkipAds.enabled && player.isPlaying) {
                val target = skipper.autoSkipTarget(atSec)
                if (target != null) {
                    seekPastAd(target)
                    skippedNonce++
                    continue
                }
            }
            activeAd = skipper.active(atSec)
        }
    }

    LaunchedEffect(skippedNonce) {
        if (skippedNonce == 0) return@LaunchedEffect
        showSkipped = true
        delay(SKIPPED_AD_TOAST_MS)
        showSkipped = false
    }

    DisposableEffect(activity) {
        val window = activity?.window
        window?.addFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON)
        onDispose { window?.clearFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON) }
    }

    BackHandler { onBack() }

    // Sleep timer: fires the same leave as Back (saves the position on dispose).
    val sleepTimer = rememberSleepTimer { onBack() }
    val sleepStatus by sleepTimer.status.collectAsStateWithLifecycle()
    var showSleepSheet by remember { mutableStateOf(false) }

    Box(modifier = modifier.fillMaxSize().background(Color.Black)) {
        AndroidView(
            factory = { ctx ->
                PlayerView(ctx).apply {
                    useController = true
                    setShowFastForwardButton(true)
                    setShowRewindButton(true)
                    setShowNextButton(false)
                    setShowPreviousButton(false)
                    setControllerVisibilityListener(
                        PlayerView.ControllerVisibilityListener { v ->
                            chromeVisible = v == android.view.View.VISIBLE
                        },
                    )
                    keepScreenOn = true
                    layoutParams = ViewGroup.LayoutParams(
                        ViewGroup.LayoutParams.MATCH_PARENT,
                        ViewGroup.LayoutParams.MATCH_PARENT,
                    )
                    this.player = player
                }
            },
            modifier = Modifier.fillMaxSize(),
        )

        if (chromeVisible || error != null) {
            Column(
                modifier = Modifier
                    .fillMaxWidth()
                    .align(Alignment.TopStart)
                    .background(BowtieColors.bg.copy(alpha = 0.6f))
                    .padding(horizontal = 8.dp, vertical = 8.dp),
            ) {
                Row {
                    TextButton(onClick = onBack) {
                        Text("‹ Recordings", color = BowtieColors.amber)
                    }
                    TextButton(onClick = { showSleepSheet = true }) {
                        Text(sleepChipLabel(sleepStatus), color = BowtieColors.amber)
                    }
                }
                Text(
                    text = start.recording.title,
                    style = BowtieType.title,
                    color = BowtieColors.text,
                    modifier = Modifier.padding(horizontal = 12.dp),
                )
                Text(
                    text = start.recording.channelName,
                    style = BowtieType.label,
                    color = BowtieColors.dim,
                    modifier = Modifier.padding(horizontal = 12.dp),
                )
            }
        }

        // Not tied to the controller's visibility: stays up for the whole break.
        if (showSkipped) {
            SkippedAdToast(
                modifier = Modifier
                    .align(Alignment.BottomEnd)
                    .padding(bottom = 96.dp, end = 16.dp),
            )
        } else if (activeAd != null && error == null) {
            SkipAdButton(
                onSkip = { skipper.skip(player.currentPosition / 1000.0)?.let { seekPastAd(it) } },
                modifier = Modifier
                    .align(Alignment.BottomEnd)
                    .padding(bottom = 96.dp, end = 16.dp),
            )
        }

        if (sleepStatus.warning) {
            SleepWarning(
                remainingMs = sleepStatus.remainingMs ?: 0L,
                onKeepWatching = { sleepTimer.extend() },
                modifier = Modifier
                    .align(Alignment.BottomCenter)
                    .padding(bottom = 96.dp, start = 16.dp, end = 16.dp),
            )
        }

        error?.let { msg ->
            Column(
                modifier = Modifier.align(Alignment.Center),
                horizontalAlignment = Alignment.CenterHorizontally,
            ) {
                Text(msg, style = BowtieType.body, color = BowtieColors.alert)
                Spacer(Modifier.height(12.dp))
                TextButton(onClick = { retry() }, enabled = !retrying) {
                    Text("Try again", color = BowtieColors.amber)
                }
            }
        }
    }

    if (showSleepSheet) {
        SleepTimerSheet(
            timer = sleepTimer,
            programEndMs = null,
            onDismiss = { showSleepSheet = false },
        )
    }
}
