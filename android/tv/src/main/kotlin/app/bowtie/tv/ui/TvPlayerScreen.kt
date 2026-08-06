package app.bowtie.tv.ui

import android.view.ViewGroup
import android.view.WindowManager
import androidx.activity.compose.BackHandler
import androidx.compose.foundation.background
import androidx.compose.foundation.focusable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
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
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.input.key.onPreviewKeyEvent
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.media3.common.util.UnstableApi
import androidx.media3.ui.PlayerView
import androidx.tv.material3.Button
import androidx.tv.material3.ButtonDefaults
import androidx.tv.material3.Text
import app.bowtie.core.Channel
import app.bowtie.core.GuideLogic
import app.bowtie.core.SessionInfoMeta
import app.bowtie.core.player.PlayerEngine
import app.bowtie.core.vm.PlayerViewModel
import app.bowtie.tv.BowtieColors
import app.bowtie.tv.BowtieDimens
import app.bowtie.tv.BowtieType
import okhttp3.HttpUrl

/**
 * Fire TV player: [PlayerEngine] + focus-safe DPAD controls.
 *
 * Focus ownership (binding A1):
 * - [PlayerView] is never focusable (`isFocusable=false`, `FOCUS_BLOCK_DESCENDANTS`)
 * - Compose container owns keys via [onPreviewKeyEvent] + [FocusRequester]
 * - Focus re-requested on entry and when the engine reaches STATE_READY
 *
 * Key map (README + A1):
 * - DPAD_CENTER short = play/pause
 * - DPAD_CENTER long (≥700ms) or MENU = transport/quality drawer
 * - DPAD_UP / DPAD_DOWN = zap (debounced via [PlayerViewModel])
 * - BACK = stop + pop (or close drawer first)
 */
@OptIn(UnstableApi::class)
@Composable
fun TvPlayerScreen(
    channel: Channel,
    channels: List<Channel>,
    playerViewModel: PlayerViewModel,
    server: HttpUrl,
    maxQuality: String,
    nowTitle: String?,
    onChannelChanged: (Channel, String?) -> Unit,
    onBack: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val context = LocalContext.current
    val activity = context as? android.app.Activity
    val state by playerViewModel.state.collectAsStateWithLifecycle()
    val scope = rememberCoroutineScope()

    var showDrawer by remember { mutableStateOf(false) }
    var showStats by remember { mutableStateOf(false) }
    var bitrateBps by remember { mutableStateOf<Int?>(null) }
    var droppedFrames by remember { mutableLongStateOf(0L) }
    var sessionMeta by remember { mutableStateOf<SessionInfoMeta?>(null) }
    var activeViewerId by remember { mutableStateOf<String?>(null) }
    var displayChannel by remember(channel.id) { mutableStateOf(channel) }
    var displayNowTitle by remember(channel.id) { mutableStateOf(nowTitle) }

    val viewModelLatest = rememberUpdatedState(playerViewModel)
    val onChannelChangedLatest = rememberUpdatedState(onChannelChanged)
    val channelsLatest = rememberUpdatedState(channels)
    val setBitrate = rememberUpdatedState { bps: Int? -> bitrateBps = bps }
    val setDropped = rememberUpdatedState { total: Long -> droppedFrames = total }

    val focusRequester = remember { FocusRequester() }
    val drawerFocusRequester = remember { FocusRequester() }
    val keyHandler = remember { PlayerKeyHandler() }
    var pendingFocusRestore by remember { mutableStateOf(false) }
    val requestFocusRestore = rememberUpdatedState {
        pendingFocusRestore = true
    }

    val engine = remember {
        PlayerEngine(
            context = context,
            scope = scope,
            listener = object : PlayerEngine.Listener {
                override fun onAuthError() {
                    viewModelLatest.value.onPlaybackAuthError()
                }

                override fun onStalled() {
                    viewModelLatest.value.onPlaybackStalled()
                }

                override fun onRecovered() {
                    viewModelLatest.value.onPlaybackRecovered()
                }

                override fun onFailed(message: String) {
                    viewModelLatest.value.onPlaybackFailed(message)
                }

                override fun onReady() {
                    // Media3 surface attach steals focus — re-request Compose ownership.
                    requestFocusRestore.value.invoke()
                }

                override fun onBitrate(bps: Int?) {
                    setBitrate.value.invoke(bps)
                }

                override fun onDroppedFrames(total: Long) {
                    setDropped.value.invoke(total)
                }
            },
        )
    }

    fun leave() {
        playerViewModel.stop()
        onBack()
    }

    fun zap(delta: Int) {
        val list = channelsLatest.value
        if (list.isEmpty()) return
        val idx = list.indexOfFirst { it.id == displayChannel.id }
        if (idx < 0) return
        val nextIdx = (idx + delta).coerceIn(0, list.lastIndex)
        if (nextIdx == idx) return
        val next = list[nextIdx]
        displayChannel = next
        displayNowTitle = null
        playerViewModel.play(next)
        onChannelChangedLatest.value(next, null)
    }

    fun applyKeyAction(action: PlayerKeyHandler.Action) {
        when (action) {
            PlayerKeyHandler.Action.PlayPause -> engine.togglePlayPause()
            PlayerKeyHandler.Action.OpenDrawer -> {
                showDrawer = true
                keyHandler.reset()
            }
            PlayerKeyHandler.Action.ZapUp -> zap(-1)
            PlayerKeyHandler.Action.ZapDown -> zap(+1)
            PlayerKeyHandler.Action.Back -> {
                if (showDrawer) {
                    showDrawer = false
                } else {
                    leave()
                }
            }
        }
    }

    // Keep screen on.
    DisposableEffect(activity) {
        val window = activity?.window
        window?.addFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON)
        onDispose {
            window?.clearFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON)
        }
    }

    DisposableEffect(engine) {
        onDispose { engine.release() }
    }

    // Request focus on entry.
    LaunchedEffect(Unit) {
        focusRequester.requestFocus()
    }

    // Re-request after STATE_READY / media source change (A1).
    LaunchedEffect(pendingFocusRestore, showDrawer) {
        if (!pendingFocusRestore) return@LaunchedEffect
        pendingFocusRestore = false
        if (!showDrawer) {
            focusRequester.requestFocus()
        }
    }

    // When drawer opens, move focus into it; when closed, restore container.
    LaunchedEffect(showDrawer) {
        if (showDrawer) {
            drawerFocusRequester.requestFocus()
        } else {
            focusRequester.requestFocus()
        }
    }

    BackHandler {
        if (showDrawer) {
            showDrawer = false
        } else {
            leave()
        }
    }

    // Bind session → media.
    LaunchedEffect(state) {
        when (val s = state) {
            is PlayerViewModel.State.Playing -> {
                val session = s.s
                if (session.viewerId != activeViewerId) {
                    activeViewerId = session.viewerId
                    sessionMeta = session.session
                    droppedFrames = 0L
                    engine.loadPlaylist(session.playlistUrl, server)
                } else {
                    sessionMeta = session.session
                }
            }
            is PlayerViewModel.State.Starting -> Unit
            is PlayerViewModel.State.Idle -> {
                activeViewerId = null
                engine.stopAndClear()
            }
            is PlayerViewModel.State.Failed,
            is PlayerViewModel.State.TunersBusy,
            -> engine.stopDecoder()
            is PlayerViewModel.State.Stalled -> Unit
        }
    }

    // Sync display channel if parent route changes without zap.
    LaunchedEffect(channel.id) {
        displayChannel = channel
        displayNowTitle = nowTitle
    }

    Box(
        modifier = modifier
            .fillMaxSize()
            .background(Color.Black)
            .focusRequester(focusRequester)
            .focusable()
            .onPreviewKeyEvent { event ->
                if (showDrawer) {
                    // Let drawer children handle navigation; only intercept BACK.
                    if (event.nativeKeyEvent.keyCode == android.view.KeyEvent.KEYCODE_BACK &&
                        event.nativeKeyEvent.action == android.view.KeyEvent.ACTION_DOWN
                    ) {
                        showDrawer = false
                        return@onPreviewKeyEvent true
                    }
                    return@onPreviewKeyEvent false
                }
                val native = event.nativeKeyEvent
                val result = keyHandler.onKey(
                    keyCode = native.keyCode,
                    action = native.action,
                    isLongPress = native.isLongPress,
                    repeatCount = native.repeatCount,
                )
                result.action?.let { applyKeyAction(it) }
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
                    this.player = engine.player
                }
            },
            update = { view ->
                view.player = engine.player
                view.isFocusable = false
                view.descendantFocusability = ViewGroup.FOCUS_BLOCK_DESCENDANTS
            },
            modifier = Modifier.fillMaxSize(),
        )

        // Channel chrome (always visible while playing — 10-foot glance)
        if (state !is PlayerViewModel.State.Failed &&
            state !is PlayerViewModel.State.TunersBusy
        ) {
            Column(
                modifier = Modifier
                    .align(Alignment.TopStart)
                    .padding(BowtieDimens.screenPadding),
            ) {
                Text(
                    text = displayChannel.guideNumber,
                    style = BowtieType.channelNumber,
                    color = BowtieColors.amber,
                )
                Spacer(Modifier.height(4.dp))
                Text(
                    text = displayChannel.name,
                    style = BowtieType.body,
                    color = BowtieColors.text,
                )
                if (!displayNowTitle.isNullOrBlank()) {
                    Spacer(Modifier.height(2.dp))
                    Text(
                        text = displayNowTitle!!,
                        style = BowtieType.label,
                        color = BowtieColors.dim,
                    )
                }
            }
        }

        if (showStats &&
            state !is PlayerViewModel.State.Failed &&
            state !is PlayerViewModel.State.TunersBusy
        ) {
            TvStatsOverlay(
                sessionMeta = sessionMeta,
                bitrateBps = bitrateBps,
                droppedFrames = droppedFrames,
                modifier = Modifier
                    .align(Alignment.TopEnd)
                    .padding(BowtieDimens.screenPadding)
                    .width(260.dp),
            )
        }

        when (state) {
            is PlayerViewModel.State.Starting,
            is PlayerViewModel.State.Stalled,
            -> {
                Box(
                    modifier = Modifier.fillMaxSize(),
                    contentAlignment = Alignment.Center,
                ) {
                    Text(
                        text = if (state is PlayerViewModel.State.Stalled) {
                            "Reconnecting…"
                        } else {
                            "Starting…"
                        },
                        style = BowtieType.body,
                        color = BowtieColors.dim,
                    )
                }
            }
            is PlayerViewModel.State.Failed -> {
                TvErrorPanel(
                    title = (state as PlayerViewModel.State.Failed).message,
                    onRetry = { playerViewModel.play(displayChannel) },
                    onBack = { leave() },
                )
            }
            is PlayerViewModel.State.TunersBusy -> {
                val busy = state as PlayerViewModel.State.TunersBusy
                TvErrorPanel(
                    title = "All tuners are in use",
                    detail = busy.sessions.joinToString("\n") { session ->
                        val watchers = session.viewers.joinToString { it.username }
                            .ifEmpty { "—" }
                        "${session.channelName}: $watchers"
                    },
                    onRetry = { playerViewModel.play(displayChannel) },
                    onBack = { leave() },
                )
            }
            else -> Unit
        }

        if (showDrawer) {
            TransportDrawer(
                focusRequester = drawerFocusRequester,
                maxQuality = maxQuality,
                selectedProfile = playerViewModel.selectedProfile,
                showStats = showStats,
                onSelectProfile = { profile ->
                    showDrawer = false
                    if (playerViewModel.selectedProfile != profile) {
                        playerViewModel.setProfile(profile)
                    }
                },
                onToggleStats = { showStats = !showStats },
                onClose = { showDrawer = false },
                modifier = Modifier
                    .align(Alignment.CenterEnd)
                    .fillMaxHeight()
                    .widthIn(min = 320.dp, max = 400.dp),
            )
        }
    }
}

@Composable
private fun TransportDrawer(
    focusRequester: FocusRequester,
    maxQuality: String,
    selectedProfile: String,
    showStats: Boolean,
    onSelectProfile: (String) -> Unit,
    onToggleStats: () -> Unit,
    onClose: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val profiles = remember(maxQuality) {
        listOf("") + GuideLogic.allowedProfiles(maxQuality)
    }

    Column(
        modifier = modifier
            .background(BowtieColors.surface.copy(alpha = 0.96f))
            .padding(BowtieDimens.rowPadding)
            .verticalScroll(rememberScrollState()),
        verticalArrangement = Arrangement.spacedBy(8.dp),
    ) {
        Text(
            text = "Controls",
            style = BowtieType.title,
            color = BowtieColors.text,
            modifier = Modifier.padding(bottom = 8.dp),
        )
        Text(
            text = "Quality",
            style = BowtieType.label,
            color = BowtieColors.dim,
        )
        profiles.forEachIndexed { index, profile ->
            val label = if (profile.isEmpty()) {
                "Auto"
            } else {
                profile.replaceFirstChar {
                    if (it.isLowerCase()) it.titlecase() else it.toString()
                }
            }
            val selected = selectedProfile == profile
            Button(
                onClick = { onSelectProfile(profile) },
                modifier = if (index == 0) {
                    Modifier
                        .fillMaxWidth()
                        .focusRequester(focusRequester)
                } else {
                    Modifier.fillMaxWidth()
                },
                colors = drawerButtonColors(selected),
            ) {
                Text(
                    text = if (selected) "✓ $label" else label,
                    style = BowtieType.body,
                    color = if (selected) BowtieColors.amber else BowtieColors.text,
                )
            }
        }
        Spacer(Modifier.height(12.dp))
        Button(
            onClick = onToggleStats,
            modifier = Modifier.fillMaxWidth(),
            colors = drawerButtonColors(showStats),
        ) {
            Text(
                text = if (showStats) "Stats ✓" else "Stats",
                style = BowtieType.body,
                color = BowtieColors.text,
            )
        }
        Button(
            onClick = onClose,
            modifier = Modifier.fillMaxWidth(),
            colors = drawerButtonColors(false),
        ) {
            Text(
                text = "Close",
                style = BowtieType.body,
                color = BowtieColors.amber,
            )
        }
    }
}

@Composable
private fun drawerButtonColors(selected: Boolean) = ButtonDefaults.colors(
    containerColor = if (selected) BowtieColors.raised else BowtieColors.bg,
    contentColor = BowtieColors.text,
    focusedContainerColor = BowtieColors.raised,
    focusedContentColor = BowtieColors.amber,
    pressedContainerColor = BowtieColors.raised,
    pressedContentColor = BowtieColors.amber,
    disabledContainerColor = BowtieColors.surface,
    disabledContentColor = BowtieColors.dim,
)

@Composable
private fun TvErrorPanel(
    title: String,
    detail: String? = null,
    onRetry: () -> Unit,
    onBack: () -> Unit,
) {
    Box(
        modifier = Modifier
            .fillMaxSize()
            .background(Color.Black.copy(alpha = 0.85f))
            .padding(BowtieDimens.screenPadding),
        contentAlignment = Alignment.Center,
    ) {
        Column(
            horizontalAlignment = Alignment.CenterHorizontally,
            modifier = Modifier
                .widthIn(max = 640.dp)
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
                colors = ButtonDefaults.colors(
                    containerColor = BowtieColors.amber,
                    contentColor = BowtieColors.bg,
                    focusedContainerColor = BowtieColors.signal,
                    focusedContentColor = BowtieColors.bg,
                    pressedContainerColor = BowtieColors.amber,
                    pressedContentColor = BowtieColors.bg,
                    disabledContainerColor = BowtieColors.surface,
                    disabledContentColor = BowtieColors.dim,
                ),
            ) {
                Text("Try again", style = BowtieType.body)
            }
            Spacer(Modifier.height(12.dp))
            Button(
                onClick = onBack,
                colors = ButtonDefaults.colors(
                    containerColor = BowtieColors.surface,
                    contentColor = BowtieColors.amber,
                    focusedContainerColor = BowtieColors.raised,
                    focusedContentColor = BowtieColors.amber,
                    pressedContainerColor = BowtieColors.raised,
                    pressedContentColor = BowtieColors.amber,
                    disabledContainerColor = BowtieColors.surface,
                    disabledContentColor = BowtieColors.dim,
                ),
            ) {
                Text("Back", style = BowtieType.body, color = BowtieColors.amber)
            }
        }
    }
}

