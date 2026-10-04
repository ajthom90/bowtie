package app.bowtie.tv.ui

import android.widget.Toast
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.runtime.withFrameNanos
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.alpha
import androidx.compose.ui.draw.clip
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.input.key.onPreviewKeyEvent
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.repeatOnLifecycle
import androidx.tv.material3.Button
import androidx.tv.material3.ButtonDefaults
import androidx.tv.material3.ClickableSurfaceDefaults
import androidx.tv.material3.Surface
import androidx.tv.material3.Text
import app.bowtie.core.Channel
import app.bowtie.core.GuideProgram
import app.bowtie.core.RecentChannel
import app.bowtie.core.RecordingLogic
import app.bowtie.core.User
import app.bowtie.core.vm.ChannelListViewModel
import app.bowtie.core.vm.PlayerViewModel
import app.bowtie.tv.BowtieColors
import app.bowtie.tv.BowtieDimens
import app.bowtie.tv.BowtieType
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import java.time.Instant
import kotlin.time.Duration.Companion.minutes

private const val EMPTY_COPY = "No channels yet. Ask your admin to enable some."

@Composable
fun ChannelRailScreen(
    user: User,
    channelListViewModel: ChannelListViewModel,
    playerViewModel: PlayerViewModel,
    onOpenChannel: (Channel) -> Unit,
    onOpenSettings: () -> Unit,
    onOpenRecordings: () -> Unit,
    modifier: Modifier = Modifier,
) {
    var panel by remember { mutableStateOf<Panel?>(null) }
    var notice by remember { mutableStateOf<String?>(null) }
    val state by channelListViewModel.state.collectAsStateWithLifecycle()
    val playingChannel by playerViewModel.currentChannel.collectAsStateWithLifecycle()
    val channelsStale by playerViewModel.channelsStale.collectAsStateWithLifecycle()
    val recents by channelListViewModel.recents.collectAsStateWithLifecycle()
    val message by channelListViewModel.message.collectAsStateWithLifecycle()
    val context = LocalContext.current
    val lifecycleOwner = LocalLifecycleOwner.current
    val scope = rememberCoroutineScope()

    // Back from the player (a route change, not ON_START): the Recent row may have grown.
    LaunchedEffect(channelListViewModel) {
        channelListViewModel.refreshRecents()
    }

    // A favorite toggle the server refused (already reverted).
    LaunchedEffect(message) {
        val text = message ?: return@LaunchedEffect
        Toast.makeText(context, text, Toast.LENGTH_SHORT).show()
        channelListViewModel.consumeMessage()
    }

    // Foreground + 5-minute refresh while STARTED (identical to phone).
    LaunchedEffect(channelListViewModel, lifecycleOwner) {
        lifecycleOwner.lifecycle.repeatOnLifecycle(Lifecycle.State.STARTED) {
            channelListViewModel.refreshIfStale()
            while (isActive) {
                delay(5.minutes)
                channelListViewModel.refreshIfStale()
            }
        }
    }

    // 404 / channelsStale from the player → force reload.
    LaunchedEffect(channelsStale) {
        if (channelsStale) {
            channelListViewModel.refresh()
            playerViewModel.clearChannelsStale()
        }
    }

    LaunchedEffect(notice) {
        if (notice == null) return@LaunchedEffect
        delay(5_000)
        notice = null
    }

    fun record(channelId: Long, program: GuideProgram, force: Boolean) {
        scope.launch {
            when (val result = channelListViewModel.record(channelId, program, force)) {
                is ChannelListViewModel.ScheduleResult.Scheduled -> {
                    val line = "Set to record: ${program.title}"
                    notice = result.warning?.let { "$line. $it" } ?: line
                }
                is ChannelListViewModel.ScheduleResult.Conflict -> {
                    panel = Panel(
                        title = "Not enough tuners",
                        body = RecordingLogic.conflictMessage(result.error),
                        choices = listOf(
                            "Don't record" to { panel = null },
                            "Record anyway" to {
                                panel = null
                                record(channelId, program, force = true)
                            },
                        ),
                    )
                }
                is ChannelListViewModel.ScheduleResult.Failed -> notice = result.message
            }
        }
    }

    /**
     * Hold OK on a channel: record what's on now or next, star / unstar it
     * (when [onToggleFavorite] is given), or watch it.
     */
    fun openChannelMenu(row: ChannelListViewModel.Row, onToggleFavorite: (() -> Unit)?) {
        val programs = listOfNotNull(
            row.nowNext.now?.let { "On now" to it },
            row.nowNext.next?.let { "Next" to it },
        ).filter { it.second.recording == null }
        val recordChoices = programs.map { (whenLabel, program) ->
            "Record \"${program.title}\" ($whenLabel)" to {
                panel = null
                record(row.channel.id, program, force = false)
            }
        }
        val favoriteChoice = onToggleFavorite?.let { toggle ->
            (if (row.isFavorite) "Remove from favorites" else "Add to favorites") to {
                panel = null
                toggle()
            }
        }
        val choices = recordChoices + listOfNotNull(favoriteChoice) + ("Watch" to {
            panel = null
            onOpenChannel(row.channel)
        }) + ("Close" to { panel = null })
        panel = Panel(
            title = "${row.channel.guideNumber} ${row.channel.name}",
            body = when {
                row.nowNext.now == null && row.nowNext.next == null ->
                    "No guide data, so there's nothing to record."
                programs.isEmpty() -> "Already set to record. Manage it in Recordings."
                else -> null
            },
            choices = choices,
        )
    }

    Box(modifier = modifier.fillMaxSize()) {
        Column(
            modifier = Modifier
                .fillMaxSize()
                .background(BowtieColors.bg),
        ) {
            Row(
                modifier = Modifier
                    .fillMaxWidth()
                    .padding(horizontal = BowtieDimens.screenPadding, vertical = 20.dp),
                horizontalArrangement = Arrangement.SpaceBetween,
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Column {
                    Text(
                        text = "Channels",
                        style = BowtieType.title,
                        color = BowtieColors.text,
                    )
                    Text(
                        text = user.username,
                        style = BowtieType.label,
                        color = BowtieColors.dim,
                    )
                    val loaded = state as? ChannelListViewModel.LoadState.Loaded
                    if (loaded != null) {
                        Text(
                            text = if (loaded.favoritesSupported) {
                                "Hold OK to record or star · ☰ stars a channel"
                            } else {
                                "Hold OK to record"
                            },
                            style = BowtieType.label,
                            color = BowtieColors.dim,
                        )
                    }
                }
                Row(horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                    Button(
                        onClick = onOpenRecordings,
                        colors = tvButtonColors(),
                    ) {
                        Text(
                            text = "Recordings",
                            style = BowtieType.body,
                            color = BowtieColors.amber,
                        )
                    }
                    Button(
                        onClick = onOpenSettings,
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
                        Text(
                            text = "Settings",
                            style = BowtieType.body,
                            color = BowtieColors.amber,
                        )
                    }
                }
            }

            Box(
                modifier = Modifier
                    .fillMaxWidth()
                    .height(1.dp)
                    .background(BowtieColors.line),
            )

            when (val s = state) {
                is ChannelListViewModel.LoadState.Loading -> {
                    Box(
                        modifier = Modifier.fillMaxSize(),
                        contentAlignment = Alignment.Center,
                    ) {
                        Text(
                            text = "Loading…",
                            style = BowtieType.body,
                            color = BowtieColors.dim,
                        )
                    }
                }
                is ChannelListViewModel.LoadState.Empty -> {
                    Box(
                        modifier = Modifier.fillMaxSize(),
                        contentAlignment = Alignment.Center,
                    ) {
                        Text(
                            text = EMPTY_COPY,
                            style = BowtieType.body,
                            color = BowtieColors.dim,
                            modifier = Modifier.padding(BowtieDimens.screenPadding),
                        )
                    }
                }
                is ChannelListViewModel.LoadState.Failed -> {
                    Column(
                        modifier = Modifier
                            .fillMaxSize()
                            .padding(BowtieDimens.screenPadding),
                        verticalArrangement = Arrangement.Center,
                        horizontalAlignment = Alignment.CenterHorizontally,
                    ) {
                        Text(
                            text = s.message,
                            style = BowtieType.body,
                            color = BowtieColors.alert,
                        )
                        Spacer(Modifier.height(16.dp))
                        Button(
                            onClick = {
                                scope.launch { channelListViewModel.refresh() }
                            },
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
                            Text(
                                text = "Try again",
                                style = BowtieType.body,
                                color = BowtieColors.amber,
                            )
                        }
                    }
                }
                is ChannelListViewModel.LoadState.Loaded -> {
                    val supported = s.favoritesSupported
                    val listState = rememberLazyListState()
                    // A toggled row moves (favorites first); keep it on screen and focused.
                    var refocusId by remember { mutableStateOf<Long?>(null) }
                    LaunchedEffect(s.rows, refocusId) {
                        val id = refocusId ?: return@LaunchedEffect
                        val index = s.rows.indexOfFirst { it.id == id }
                        if (index < 0) {
                            refocusId = null
                        } else if (listState.layoutInfo.visibleItemsInfo.none { it.key == id }) {
                            listState.scrollToItem(index)
                        }
                    }

                    if (supported && recents.isNotEmpty()) {
                        RecentRail(
                            recents = recents,
                            onOpen = { onOpenChannel(channelListViewModel.channelFor(it)) },
                        )
                    }
                    LazyColumn(
                        state = listState,
                        modifier = Modifier
                            .fillMaxSize()
                            .padding(horizontal = BowtieDimens.screenPadding, vertical = 12.dp),
                        verticalArrangement = Arrangement.spacedBy(8.dp),
                    ) {
                        items(s.rows, key = { it.id }) { row ->
                            val focusRequester = remember { FocusRequester() }
                            LaunchedEffect(refocusId == row.id) {
                                if (refocusId != row.id) return@LaunchedEffect
                                withFrameNanos { }
                                runCatching { focusRequester.requestFocus() }
                                refocusId = null
                            }
                            val toggle: (() -> Unit)? = if (supported) {
                                {
                                    refocusId = row.id
                                    channelListViewModel.toggleFavorite(row.id)
                                }
                            } else {
                                null
                            }
                            ChannelRailRow(
                                row = row,
                                isPlaying = playingChannel?.id == row.channel.id,
                                showStar = supported,
                                onClick = { onOpenChannel(row.channel) },
                                onLongClick = { openChannelMenu(row, toggle) },
                                onToggleFavorite = toggle,
                                modifier = Modifier.focusRequester(focusRequester),
                            )
                        }
                    }
                }
            }
        }

        notice?.let { msg ->
            Text(
                text = msg,
                style = BowtieType.body,
                color = BowtieColors.text,
                modifier = Modifier
                    .align(Alignment.BottomCenter)
                    .padding(32.dp)
                    .background(BowtieColors.raised, RoundedCornerShape(8.dp))
                    .padding(horizontal = 20.dp, vertical = 12.dp),
            )
        }
        panel?.let { ChoicePanel(it, onDismiss = { panel = null }) }
    }
}

/** Recently watched channels above the rail; DPAD up from the rail reaches it. */
@Composable
private fun RecentRail(
    recents: List<RecentChannel>,
    onOpen: (RecentChannel) -> Unit,
) {
    Column(modifier = Modifier.padding(top = 16.dp)) {
        Text(
            text = "RECENT",
            style = BowtieType.label,
            color = BowtieColors.dim,
            modifier = Modifier.padding(horizontal = BowtieDimens.screenPadding),
        )
        Spacer(Modifier.height(8.dp))
        LazyRow(
            horizontalArrangement = Arrangement.spacedBy(12.dp),
            contentPadding = PaddingValues(horizontal = BowtieDimens.screenPadding, vertical = 4.dp),
        ) {
            items(recents, key = { it.channelId }) { recent ->
                Surface(
                    onClick = { onOpen(recent) },
                    modifier = Modifier.width(180.dp),
                    colors = railSurfaceColors(),
                    shape = ClickableSurfaceDefaults.shape(
                        shape = RoundedCornerShape(BowtieDimens.cornerRadius),
                    ),
                ) {
                    Column(modifier = Modifier.padding(horizontal = 16.dp, vertical = 12.dp)) {
                        Text(
                            text = recent.guideNumber,
                            style = BowtieType.channelNumber,
                            color = BowtieColors.text,
                            maxLines = 1,
                        )
                        Text(
                            text = recent.name,
                            style = BowtieType.label,
                            color = BowtieColors.dim,
                            maxLines = 1,
                            overflow = TextOverflow.Ellipsis,
                        )
                    }
                }
            }
        }
    }
}


@Composable
private fun railSurfaceColors() = ClickableSurfaceDefaults.colors(
    containerColor = BowtieColors.surface,
    contentColor = BowtieColors.text,
    focusedContainerColor = BowtieColors.raised,
    focusedContentColor = BowtieColors.text,
    pressedContainerColor = BowtieColors.raised,
    pressedContentColor = BowtieColors.text,
    disabledContainerColor = BowtieColors.surface,
    disabledContentColor = BowtieColors.dim,
)

/**
 * One rail item. Long-press OK (Surface `onLongClick`) opens [onLongClick]'s
 * menu (record, and favorite when supported). With [onToggleFavorite], the
 * ☰ key ([RailKeys]) stars / unstars it directly.
 */
@Composable
private fun ChannelRailRow(
    row: ChannelListViewModel.Row,
    isPlaying: Boolean,
    showStar: Boolean,
    onClick: () -> Unit,
    onLongClick: () -> Unit,
    onToggleFavorite: (() -> Unit)?,
    modifier: Modifier = Modifier,
) {
    val now = row.nowNext.now
    val next = row.nowNext.next
    val progress = ChannelListViewModel.programProgress(now, Instant.now())
    val numberColor = if (isPlaying) BowtieColors.amber else BowtieColors.text

    Surface(
        onClick = onClick,
        // Hold OK: one menu with record and favorite options.
        onLongClick = onLongClick,
        modifier = modifier
            .fillMaxWidth()
            .onPreviewKeyEvent { event ->
                val toggle = onToggleFavorite ?: return@onPreviewKeyEvent false
                val native = event.nativeKeyEvent
                when (RailKeys.onKey(native.keyCode, native.action, native.repeatCount)) {
                    RailKeys.Outcome.ToggleFavorite -> {
                        toggle()
                        true
                    }
                    RailKeys.Outcome.Consume -> true
                    RailKeys.Outcome.PassThrough -> false
                }
            },
        colors = ClickableSurfaceDefaults.colors(
            containerColor = BowtieColors.surface,
            contentColor = BowtieColors.text,
            focusedContainerColor = BowtieColors.raised,
            focusedContentColor = BowtieColors.text,
            pressedContainerColor = BowtieColors.raised,
            pressedContentColor = BowtieColors.text,
            disabledContainerColor = BowtieColors.surface,
            disabledContentColor = BowtieColors.dim,
        ),
        shape = ClickableSurfaceDefaults.shape(
            shape = RoundedCornerShape(BowtieDimens.cornerRadius),
        ),
    ) {
        Row(
            modifier = Modifier
                .fillMaxWidth()
                // Last tune got no signal: dim (still selectable; signal may return).
                .alpha(if (row.channel.hasNoSignal) 0.55f else 1f)
                .padding(
                    horizontal = BowtieDimens.rowPadding,
                    vertical = BowtieDimens.rowPadding,
                ),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Text(
                text = row.channel.guideNumber,
                style = BowtieType.channelNumber,
                color = numberColor,
                modifier = Modifier.width(96.dp),
            )
            Spacer(Modifier.width(20.dp))
            Column(modifier = Modifier.weight(1f)) {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Text(
                        text = row.channel.name,
                        style = BowtieType.body,
                        color = BowtieColors.text,
                    )
                    if (showStar && row.isFavorite) {
                        Spacer(Modifier.width(12.dp))
                        Text(
                            text = "★",
                            style = BowtieType.body,
                            color = BowtieColors.amber,
                        )
                    }
                }
                if (row.channel.hasNoSignal) {
                    Text(
                        text = "NO SIGNAL",
                        style = BowtieType.label,
                        color = BowtieColors.alert,
                    )
                }
                if (now != null) {
                    Spacer(Modifier.height(6.dp))
                    Text(
                        text = (if (now.recording != null) "● " else "") + now.title +
                            (RecordingLogic.lockLabel(now)?.let { "   $it" } ?: ""),
                        style = BowtieType.body,
                        color = BowtieColors.text,
                        maxLines = 1,
                    )
                    Spacer(Modifier.height(8.dp))
                    ProgressCapsule(progress = progress)
                } else {
                    Spacer(Modifier.height(6.dp))
                    Text(
                        text = "No guide data",
                        style = BowtieType.label,
                        color = BowtieColors.dim,
                    )
                }
                if (next != null) {
                    Spacer(Modifier.height(6.dp))
                    Text(
                        text = "Next: " + (if (next.recording != null) "● " else "") + next.title +
                            (RecordingLogic.lockLabel(next)?.let { "   $it" } ?: ""),
                        style = BowtieType.label,
                        color = BowtieColors.dim,
                        maxLines = 1,
                    )
                }
            }
        }
    }
}

/** Amber-fill progress track for the airing program. */
@Composable
private fun ProgressCapsule(progress: Float) {
    Box(
        modifier = Modifier
            .fillMaxWidth()
            .height(BowtieDimens.progressHeight)
            .clip(RoundedCornerShape(50))
            .background(BowtieColors.raised),
    ) {
        Box(
            modifier = Modifier
                .fillMaxWidth(progress.coerceIn(0f, 1f))
                .height(BowtieDimens.progressHeight)
                .clip(RoundedCornerShape(50))
                .background(BowtieColors.amber),
        )
    }
}
