package app.bowtie.ui

import android.app.Activity
import android.view.ViewGroup
import android.view.WindowManager
import androidx.activity.compose.BackHandler
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableLongStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.media3.common.MediaItem
import androidx.media3.common.PlaybackException
import androidx.media3.common.Player
import androidx.media3.common.util.UnstableApi
import androidx.media3.datasource.DefaultHttpDataSource
import androidx.media3.datasource.HttpDataSource
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.exoplayer.analytics.AnalyticsListener
import androidx.media3.exoplayer.hls.HlsMediaSource
import androidx.media3.exoplayer.source.BehindLiveWindowException
import androidx.media3.ui.PlayerView
import app.bowtie.BowtieColors
import app.bowtie.BowtieDimens
import app.bowtie.BowtieType
import app.bowtie.PipHost
import app.bowtie.core.Channel
import app.bowtie.core.vm.PlayerViewModel
import app.bowtie.core.CreatedSession
import app.bowtie.core.GuideLogic
import app.bowtie.core.ServerUrl
import app.bowtie.core.SessionInfoMeta
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import okhttp3.HttpUrl

private const val OVERLAY_HIDE_MS = 3_000L
private val NETWORK_BACKOFF_MS = longArrayOf(1_000L, 2_000L, 4_000L)
private const val PLAYBACK_FAILED_COPY =
    "Playback failed. Try again or pick a lower quality."

/**
 * Media3 HLS player: default (unauthenticated) data source, overlay controls,
 * quality sheet, stats, keep-screen-on. PiP is coordinated via [PipHost].
 *
 * Global constraint: NEVER share the Bearer OkHttp client with HlsMediaSource.
 */
@OptIn(ExperimentalMaterial3Api::class, UnstableApi::class)
@Composable
fun PlayerScreen(
    channel: Channel,
    playerViewModel: PlayerViewModel,
    server: HttpUrl,
    maxQuality: String,
    nowTitle: String?,
    onBack: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val context = LocalContext.current
    val activity = context as? Activity
    val state by playerViewModel.state.collectAsStateWithLifecycle()
    val scope = rememberCoroutineScope()

    var overlayVisible by remember { mutableStateOf(true) }
    var showStats by remember { mutableStateOf(false) }
    var showQualitySheet by remember { mutableStateOf(false) }
    var bitrateBps by remember { mutableStateOf<Int?>(null) }
    var droppedFrames by remember { mutableLongStateOf(0L) }
    var sessionMeta by remember { mutableStateOf<SessionInfoMeta?>(null) }
    var activeViewerId by remember { mutableStateOf<String?>(null) }

    val onBackLatest = rememberUpdatedState(onBack)
    val viewModelLatest = rememberUpdatedState(playerViewModel)

    // Default HttpDataSource — no auth interceptor (stream auth is ?token=).
    val player = remember {
        val httpFactory = DefaultHttpDataSource.Factory()
            .setUserAgent("BowtieAndroid/0.1")
            .setAllowCrossProtocolRedirects(true)
        val hlsFactory = HlsMediaSource.Factory(httpFactory)
        ExoPlayer.Builder(context)
            .setMediaSourceFactory(hlsFactory)
            .build()
            .apply {
                playWhenReady = true
                // Live edge by default for HLS.
                setHandleAudioBecomingNoisy(true)
            }
    }

    var networkRetryJob by remember { mutableStateOf<Job?>(null) }
    var networkAttempt by remember { mutableStateOf(0) }

    fun leave() {
        networkRetryJob?.cancel()
        playerViewModel.stop()
        onBack()
    }

    BackHandler { leave() }

    // Keep screen on while this screen is shown.
    DisposableEffect(activity) {
        val window = activity?.window
        window?.addFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON)
        onDispose {
            window?.clearFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON)
        }
    }

    // PiP host: want PiP while we have an active playback session.
    val wantsPip = state is PlayerViewModel.State.Playing ||
        state is PlayerViewModel.State.Stalled ||
        state is PlayerViewModel.State.Starting
    DisposableEffect(wantsPip, activity) {
        val host = activity as? PipHost
        host?.setWantsPip(wantsPip)
        host?.setOnPipClosed {
            viewModelLatest.value.stop()
            onBackLatest.value()
        }
        onDispose {
            host?.setWantsPip(false)
            host?.setOnPipClosed(null)
        }
    }

    // Wire Media3 listener once.
    DisposableEffect(player) {
        val analytics = object : AnalyticsListener {
            override fun onDroppedVideoFrames(
                eventTime: AnalyticsListener.EventTime,
                droppedFramesCount: Int,
                elapsedMs: Long,
            ) {
                droppedFrames += droppedFramesCount.toLong()
            }

            override fun onVideoSizeChanged(
                eventTime: AnalyticsListener.EventTime,
                videoSize: androidx.media3.common.VideoSize,
            ) {
                bitrateBps = player.videoFormat?.bitrate?.takeIf { it > 0 }
            }
        }
        val listener = object : Player.Listener {
            override fun onPlayerError(error: PlaybackException) {
                handlePlayerError(
                    error = error,
                    player = player,
                    viewModel = playerViewModel,
                    networkAttempt = networkAttempt,
                    onNetworkAttempt = { networkAttempt = it },
                    networkRetryJob = networkRetryJob,
                    onNetworkRetryJob = { networkRetryJob = it },
                    scope = scope,
                )
            }

            override fun onPlaybackStateChanged(playbackState: Int) {
                if (playbackState == Player.STATE_READY) {
                    networkAttempt = 0
                    networkRetryJob?.cancel()
                    networkRetryJob = null
                    bitrateBps = player.videoFormat?.bitrate?.takeIf { it > 0 }
                    if (viewModelLatest.value.state.value is PlayerViewModel.State.Stalled) {
                        viewModelLatest.value.onPlaybackRecovered()
                    }
                } else if (playbackState == Player.STATE_BUFFERING) {
                    // Prolonged rebuffer is handled via onPlayerError / live-window.
                }
            }

            override fun onTracksChanged(tracks: androidx.media3.common.Tracks) {
                bitrateBps = player.videoFormat?.bitrate?.takeIf { it > 0 }
            }
        }
        player.addListener(listener)
        player.addAnalyticsListener(analytics)
        onDispose {
            player.removeListener(listener)
            player.removeAnalyticsListener(analytics)
            networkRetryJob?.cancel()
            player.release()
        }
    }

    // Bind session → media when Playing with a new viewer/session.
    LaunchedEffect(state) {
        when (val s = state) {
            is PlayerViewModel.State.Playing -> {
                val session = s.s
                if (session.viewerId != activeViewerId) {
                    activeViewerId = session.viewerId
                    sessionMeta = session.session
                    droppedFrames = 0L
                    networkAttempt = 0
                    networkRetryJob?.cancel()
                    loadSession(player, server, session)
                } else {
                    sessionMeta = session.session
                }
            }
            is PlayerViewModel.State.Starting -> {
                // Keep last frames until new source attaches.
            }
            is PlayerViewModel.State.Idle -> {
                activeViewerId = null
                player.stop()
                player.clearMediaItems()
            }
            is PlayerViewModel.State.Failed,
            is PlayerViewModel.State.TunersBusy,
            -> {
                // Stop decoder but keep state for UI; session may still exist until retry/stop.
                player.stop()
            }
            is PlayerViewModel.State.Stalled -> {
                // Player layer handles seek retry.
            }
        }
    }

    // Auto-hide overlay after 3s of idle (while playing).
    LaunchedEffect(overlayVisible, state, showQualitySheet) {
        if (!overlayVisible || showQualitySheet) return@LaunchedEffect
        if (state !is PlayerViewModel.State.Playing) return@LaunchedEffect
        delay(OVERLAY_HIDE_MS)
        overlayVisible = false
    }

    // Hide Media3 chrome; we draw our own overlay.
    Box(
        modifier = modifier
            .fillMaxSize()
            .background(Color.Black)
            .clickable(
                interactionSource = remember { MutableInteractionSource() },
                indication = null,
            ) {
                overlayVisible = !overlayVisible
            },
    ) {
        AndroidView(
            factory = { ctx ->
                PlayerView(ctx).apply {
                    useController = false
                    keepScreenOn = true
                    layoutParams = ViewGroup.LayoutParams(
                        ViewGroup.LayoutParams.MATCH_PARENT,
                        ViewGroup.LayoutParams.MATCH_PARENT,
                    )
                    this.player = player
                }
            },
            update = { view ->
                view.player = player
            },
            modifier = Modifier.fillMaxSize(),
        )

        // Starting / stalled spinner
        when (state) {
            is PlayerViewModel.State.Starting,
            is PlayerViewModel.State.Stalled,
            -> {
                Box(
                    modifier = Modifier.fillMaxSize(),
                    contentAlignment = Alignment.Center,
                ) {
                    Column(horizontalAlignment = Alignment.CenterHorizontally) {
                        CircularProgressIndicator(color = BowtieColors.amber)
                        Spacer(Modifier.height(12.dp))
                        Text(
                            text = if (state is PlayerViewModel.State.Stalled) {
                                "Reconnecting…"
                            } else {
                                "Starting…"
                            },
                            style = BowtieType.mono,
                            color = BowtieColors.dim,
                        )
                    }
                }
            }
            is PlayerViewModel.State.Failed -> {
                ErrorPanel(
                    title = (state as PlayerViewModel.State.Failed).message,
                    onRetry = {
                        playerViewModel.play(channel)
                    },
                    onBack = { leave() },
                )
            }
            is PlayerViewModel.State.TunersBusy -> {
                val busy = state as PlayerViewModel.State.TunersBusy
                ErrorPanel(
                    title = "All tuners are in use",
                    detail = busy.sessions.joinToString("\n") { session ->
                        val watchers = session.viewers.joinToString { it.username }
                            .ifEmpty { "—" }
                        "${session.channelName}: $watchers"
                    },
                    onRetry = { playerViewModel.play(channel) },
                    onBack = { leave() },
                )
            }
            else -> Unit
        }

        if (overlayVisible &&
            state !is PlayerViewModel.State.Failed &&
            state !is PlayerViewModel.State.TunersBusy
        ) {
            PlayerOverlay(
                channel = channel,
                nowTitle = nowTitle,
                showStats = showStats,
                sessionMeta = sessionMeta,
                bitrateBps = bitrateBps,
                droppedFrames = droppedFrames,
                selectedProfile = playerViewModel.selectedProfile,
                onBack = { leave() },
                onQuality = {
                    overlayVisible = true
                    showQualitySheet = true
                },
                onToggleStats = {
                    showStats = !showStats
                    overlayVisible = true
                },
                onInteraction = { overlayVisible = true },
                modifier = Modifier.fillMaxSize(),
            )
        }
    }

    if (showQualitySheet) {
        val sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true)
        val profiles = remember(maxQuality) {
            listOf("") + GuideLogic.allowedProfiles(maxQuality)
        }
        ModalBottomSheet(
            onDismissRequest = { showQualitySheet = false },
            sheetState = sheetState,
            containerColor = BowtieColors.surface,
            contentColor = BowtieColors.text,
        ) {
            Column(
                modifier = Modifier
                    .fillMaxWidth()
                    .padding(horizontal = 20.dp, vertical = 8.dp)
                    .padding(bottom = 24.dp),
            ) {
                Text(
                    text = "Quality",
                    style = BowtieType.title,
                    color = BowtieColors.text,
                    modifier = Modifier.padding(bottom = 12.dp),
                )
                profiles.forEach { profile ->
                    val label = if (profile.isEmpty()) "Auto" else profile.replaceFirstChar {
                        if (it.isLowerCase()) it.titlecase() else it.toString()
                    }
                    val selected = playerViewModel.selectedProfile == profile
                    TextButton(
                        onClick = {
                            showQualitySheet = false
                            if (playerViewModel.selectedProfile != profile) {
                                playerViewModel.setProfile(profile)
                            }
                        },
                        modifier = Modifier.fillMaxWidth(),
                    ) {
                        Text(
                            text = if (selected) "✓ $label" else label,
                            style = BowtieType.body,
                            color = if (selected) BowtieColors.amber else BowtieColors.text,
                            modifier = Modifier.fillMaxWidth(),
                        )
                    }
                }
            }
        }
    }
}

@UnstableApi
private fun loadSession(
    player: ExoPlayer,
    server: HttpUrl,
    session: CreatedSession,
) {
    val resolved = ServerUrl.resolve(session.playlistUrl, server)
    // MediaItem + HlsMediaSource via the player's DefaultMediaSourceFactory
    // which we configured with plain DefaultHttpDataSource (no Bearer).
    val item = MediaItem.fromUri(resolved.toString())
    player.setMediaItem(item)
    player.prepare()
    player.playWhenReady = true
}

@UnstableApi
private fun handlePlayerError(
    error: PlaybackException,
    player: ExoPlayer,
    viewModel: PlayerViewModel,
    networkAttempt: Int,
    onNetworkAttempt: (Int) -> Unit,
    networkRetryJob: Job?,
    onNetworkRetryJob: (Job?) -> Unit,
    scope: CoroutineScope,
) {
    val cause = error.cause
    val responseCode =
        (cause as? HttpDataSource.InvalidResponseCodeException)?.responseCode

    if (responseCode == 403) {
        viewModel.onPlaybackAuthError()
        return
    }

    val behindLive = cause is BehindLiveWindowException ||
        error.errorCode == PlaybackException.ERROR_CODE_BEHIND_LIVE_WINDOW

    if (behindLive) {
        viewModel.onPlaybackStalled()
        try {
            player.seekToDefaultPosition()
            player.prepare()
        } catch (_: Exception) {
            // Fall through to network retry path below if seek fails.
        }
        return
    }

    val isNetwork = error.errorCode in NETWORK_ERROR_CODES ||
        cause is HttpDataSource.HttpDataSourceException ||
        cause is java.io.IOException

    if (isNetwork) {
        viewModel.onPlaybackStalled()
        networkRetryJob?.cancel()
        val attempt = networkAttempt
        if (attempt >= NETWORK_BACKOFF_MS.size) {
            onNetworkAttempt(0)
            viewModel.onPlaybackFailed(PLAYBACK_FAILED_COPY)
            return
        }
        val delayMs = NETWORK_BACKOFF_MS[attempt]
        onNetworkAttempt(attempt + 1)
        val job = scope.launch {
            delay(delayMs)
            try {
                player.seekToDefaultPosition()
                player.prepare()
                player.playWhenReady = true
            } catch (_: Exception) {
                viewModel.onPlaybackFailed(PLAYBACK_FAILED_COPY)
            }
        }
        onNetworkRetryJob(job)
        return
    }

    // Other hard errors → fail after optional single prepare retry is not required.
    viewModel.onPlaybackFailed(error.message ?: PLAYBACK_FAILED_COPY)
}

@UnstableApi
private val NETWORK_ERROR_CODES = intArrayOf(
    PlaybackException.ERROR_CODE_IO_NETWORK_CONNECTION_FAILED,
    PlaybackException.ERROR_CODE_IO_NETWORK_CONNECTION_TIMEOUT,
    PlaybackException.ERROR_CODE_IO_BAD_HTTP_STATUS,
    PlaybackException.ERROR_CODE_TIMEOUT,
)

@Composable
private fun PlayerOverlay(
    channel: Channel,
    nowTitle: String?,
    showStats: Boolean,
    sessionMeta: SessionInfoMeta?,
    bitrateBps: Int?,
    droppedFrames: Long,
    selectedProfile: String,
    onBack: () -> Unit,
    onQuality: () -> Unit,
    onToggleStats: () -> Unit,
    onInteraction: () -> Unit,
    modifier: Modifier = Modifier,
) {
    Box(modifier = modifier) {
        // Top: channel number + now title
        Column(
            modifier = Modifier
                .align(Alignment.TopStart)
                .padding(16.dp),
        ) {
            Text(
                text = channel.guideNumber,
                style = BowtieType.channelNumber,
                color = BowtieColors.amber,
            )
            Spacer(Modifier.height(4.dp))
            Text(
                text = channel.name,
                style = BowtieType.body,
                color = BowtieColors.text,
            )
            if (!nowTitle.isNullOrBlank()) {
                Spacer(Modifier.height(2.dp))
                Text(
                    text = nowTitle,
                    style = BowtieType.label,
                    color = BowtieColors.dim,
                )
            }
        }

        if (showStats) {
            StatsOverlay(
                sessionMeta = sessionMeta,
                bitrateBps = bitrateBps,
                droppedFrames = droppedFrames,
                modifier = Modifier
                    .align(Alignment.TopEnd)
                    .padding(16.dp)
                    .width(220.dp),
            )
        }

        // Bottom controls
        Row(
            modifier = Modifier
                .align(Alignment.BottomCenter)
                .fillMaxWidth()
                .background(Color.Black.copy(alpha = 0.55f))
                .padding(horizontal = 12.dp, vertical = 10.dp)
                .clickable(
                    interactionSource = remember { MutableInteractionSource() },
                    indication = null,
                    onClick = onInteraction,
                ),
            horizontalArrangement = Arrangement.spacedBy(8.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            ControlChip(label = "Back", onClick = onBack)
            Spacer(Modifier.weight(1f))
            val qualityLabel = if (selectedProfile.isEmpty()) {
                "Quality: Auto"
            } else {
                "Quality: ${selectedProfile.replaceFirstChar {
                    if (it.isLowerCase()) it.titlecase() else it.toString()
                }}"
            }
            ControlChip(label = qualityLabel, onClick = onQuality)
            ControlChip(
                label = if (showStats) "Stats ✓" else "Stats",
                onClick = onToggleStats,
            )
        }
    }
}

@Composable
private fun ControlChip(label: String, onClick: () -> Unit) {
    Text(
        text = label,
        style = BowtieType.label.copy(color = BowtieColors.text),
        modifier = Modifier
            .clip(RoundedCornerShape(BowtieDimens.cornerRadius))
            .background(BowtieColors.raised)
            .clickable(onClick = onClick)
            .padding(horizontal = 14.dp, vertical = 10.dp),
    )
}

@Composable
private fun ErrorPanel(
    title: String,
    detail: String? = null,
    onRetry: () -> Unit,
    onBack: () -> Unit,
) {
    Box(
        modifier = Modifier
            .fillMaxSize()
            .background(Color.Black.copy(alpha = 0.85f))
            .padding(24.dp),
        contentAlignment = Alignment.Center,
    ) {
        Column(
            horizontalAlignment = Alignment.CenterHorizontally,
            modifier = Modifier
                .fillMaxWidth()
                .verticalScroll(rememberScrollState()),
        ) {
            Text(
                text = title,
                style = BowtieType.title,
                color = BowtieColors.alert,
            )
            if (!detail.isNullOrBlank()) {
                Spacer(Modifier.height(12.dp))
                Text(
                    text = detail,
                    style = BowtieType.body,
                    color = BowtieColors.dim,
                )
            }
            Spacer(Modifier.height(24.dp))
            Button(
                onClick = onRetry,
                colors = ButtonDefaults.buttonColors(
                    containerColor = BowtieColors.amber,
                    contentColor = BowtieColors.bg,
                ),
            ) {
                Text("Try again")
            }
            Spacer(Modifier.height(8.dp))
            TextButton(onClick = onBack) {
                Text("Back", color = BowtieColors.amber)
            }
        }
    }
}
