package app.bowtie.tv.ui

import android.app.Activity
import android.view.ViewGroup
import android.view.WindowManager
import androidx.activity.compose.BackHandler
import androidx.compose.foundation.background
import androidx.compose.foundation.focusable
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableLongStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.input.key.onPreviewKeyEvent
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.media3.common.C
import androidx.media3.common.PlaybackException
import androidx.media3.common.Player
import androidx.media3.common.util.UnstableApi
import androidx.media3.ui.PlayerView
import androidx.tv.material3.Text
import app.bowtie.core.RecordingLogic
import app.bowtie.core.player.VodPlayer
import app.bowtie.core.vm.RecordingsViewModel
import app.bowtie.tv.BowtieColors
import app.bowtie.tv.BowtieType
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import okhttp3.HttpUrl

/** How long the progress bar stays up after a key press while playing. */
private const val INFO_HIDE_MS = 4_000L

/**
 * Recording playback for TV: HLS VOD with DPAD seeking ([VodKeys]), a
 * progress bar shown on any key and while paused, and the resume position
 * saved every 15 s, on background, at the end and on exit.
 */
@OptIn(UnstableApi::class)
@Composable
fun TvRecordingPlayerScreen(
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
    val focusRequester = remember { FocusRequester() }
    var error by remember { mutableStateOf<String?>(null) }
    var isPlaying by remember { mutableStateOf(true) }
    var positionMs by remember { mutableLongStateOf(startAtSec * 1000L) }
    var durationMs by remember { mutableLongStateOf(start.durationSec * 1000L) }
    var infoNonce by remember { mutableIntStateOf(0) }
    var infoVisible by remember { mutableStateOf(true) }
    var lastSavedMs by remember { mutableLongStateOf(-1L) }
    var retrying by remember { mutableStateOf(false) }
    val scope = rememberCoroutineScope()

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
                    is RecordingsViewModel.Retry.Failed -> error = "${r.message} Press OK to try again."
                }
            } finally {
                retrying = false
            }
        }
    }

    fun save() {
        if (player.playbackState == Player.STATE_IDLE && player.currentPosition == 0L) return
        val pos = player.currentPosition
        if (pos == lastSavedMs) return
        lastSavedMs = pos
        viewModel.savePosition(recordingId, pos)
    }

    fun showInfo() {
        infoVisible = true
        infoNonce++
    }

    LaunchedEffect(player) {
        VodPlayer.load(player, start.playlistUrl, server, startAtSec)
        focusRequester.requestFocus()
    }

    DisposableEffect(player) {
        val listener = object : Player.Listener {
            override fun onPlayerError(e: PlaybackException) {
                error = "This recording couldn't be played. Press OK to try again."
            }

            override fun onIsPlayingChanged(playing: Boolean) {
                isPlaying = playing
                if (!playing) showInfo()
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

    // Progress readout + periodic position save.
    LaunchedEffect(player) {
        var sinceSaveMs = 0L
        while (isActive) {
            delay(500)
            positionMs = player.currentPosition
            player.duration.takeIf { it != C.TIME_UNSET && it > 0 }?.let { durationMs = it }
            sinceSaveMs += 500
            if (sinceSaveMs >= RecordingLogic.POSITION_SAVE_INTERVAL_MS) {
                sinceSaveMs = 0
                if (player.isPlaying) save()
            }
        }
    }

    LaunchedEffect(infoNonce, isPlaying) {
        if (!isPlaying) return@LaunchedEffect
        delay(INFO_HIDE_MS)
        infoVisible = false
    }

    DisposableEffect(activity) {
        val window = activity?.window
        window?.addFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON)
        onDispose { window?.clearFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON) }
    }

    BackHandler { onBack() }

    fun apply(action: VodKeys.Action) {
        when (action) {
            VodKeys.Action.PlayPause -> {
                if (error != null || retrying) {
                    retry()
                } else if (player.isPlaying) {
                    player.pause()
                } else {
                    if (player.playbackState == Player.STATE_ENDED) player.seekTo(0)
                    player.play()
                }
            }
            VodKeys.Action.Play -> player.play()
            VodKeys.Action.Pause -> player.pause()
            is VodKeys.Action.SeekBy -> {
                val max = durationMs.takeIf { it > 0 } ?: Long.MAX_VALUE
                val target = (player.currentPosition + action.ms).coerceIn(0L, max)
                player.seekTo(target)
                positionMs = target
            }
            VodKeys.Action.ShowInfo -> Unit
            VodKeys.Action.Back -> onBack()
        }
        showInfo()
    }

    Box(
        modifier = modifier
            .fillMaxSize()
            .background(Color.Black)
            .focusRequester(focusRequester)
            .focusable()
            .onPreviewKeyEvent { event ->
                val native = event.nativeKeyEvent
                val result = VodKeys.onKey(native.keyCode, native.action, native.repeatCount)
                result.action?.let { apply(it) }
                result.handled
            },
    ) {
        AndroidView(
            factory = { ctx ->
                PlayerView(ctx).apply {
                    useController = false
                    keepScreenOn = true
                    isFocusable = false
                    isFocusableInTouchMode = false
                    descendantFocusability = ViewGroup.FOCUS_BLOCK_DESCENDANTS
                    layoutParams = ViewGroup.LayoutParams(
                        ViewGroup.LayoutParams.MATCH_PARENT,
                        ViewGroup.LayoutParams.MATCH_PARENT,
                    )
                    this.player = player
                }
            },
            modifier = Modifier.fillMaxSize(),
        )

        if (infoVisible || error != null) {
            Column(
                modifier = Modifier
                    .align(Alignment.BottomStart)
                    .fillMaxWidth()
                    .background(
                        Brush.verticalGradient(listOf(Color.Transparent, Color.Black.copy(alpha = 0.85f))),
                    )
                    .padding(horizontal = 48.dp, vertical = 32.dp),
            ) {
                Text(start.recording.title, style = BowtieType.title, color = BowtieColors.text)
                Text(start.recording.channelName, style = BowtieType.label, color = BowtieColors.dim)
                Spacer(Modifier.height(16.dp))
                ProgressBar(fraction = if (durationMs > 0) positionMs.toFloat() / durationMs else 0f)
                Spacer(Modifier.height(8.dp))
                Row {
                    Text(
                        text = (if (isPlaying) "▶  " else "❚❚  ") +
                            RecordingLogic.formatClock((positionMs / 1000).toInt()),
                        style = BowtieType.mono,
                        color = BowtieColors.text,
                        modifier = Modifier.weight(1f),
                    )
                    Text(
                        text = "◀ 10 s · 30 s ▶   " + RecordingLogic.formatClock((durationMs / 1000).toInt()),
                        style = BowtieType.mono,
                        color = BowtieColors.dim,
                    )
                }
                error?.let {
                    Spacer(Modifier.height(12.dp))
                    Text(it, style = BowtieType.body, color = BowtieColors.alert)
                }
            }
        }
    }
}

@Composable
private fun ProgressBar(fraction: Float) {
    Box(
        modifier = Modifier
            .fillMaxWidth()
            .height(6.dp)
            .clip(RoundedCornerShape(50))
            .background(BowtieColors.raised),
    ) {
        Box(
            modifier = Modifier
                .fillMaxWidth(fraction.coerceIn(0f, 1f))
                .height(6.dp)
                .clip(RoundedCornerShape(50))
                .background(BowtieColors.amber),
        )
    }
}
