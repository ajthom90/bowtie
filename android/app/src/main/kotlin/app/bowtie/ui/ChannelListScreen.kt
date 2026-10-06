package app.bowtie.ui

import android.widget.Toast
import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.combinedClickable
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
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FilterChip
import androidx.compose.material3.FilterChipDefaults
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.IconButton
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.pulltorefresh.PullToRefreshBox
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.alpha
import androidx.compose.ui.draw.clip
import androidx.compose.ui.hapticfeedback.HapticFeedbackType
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalHapticFeedback
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.semantics.stateDescription
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.repeatOnLifecycle
import app.bowtie.BowtieColors
import app.bowtie.BowtieDimens
import app.bowtie.BowtieType
import app.bowtie.core.vm.ChannelListViewModel
import app.bowtie.core.vm.ContinueWatchingViewModel
import app.bowtie.core.vm.PlayerViewModel
import app.bowtie.core.vm.SeriesResult
import app.bowtie.core.BowtieError
import app.bowtie.core.Channel
import app.bowtie.core.GuideFilter
import app.bowtie.core.GuideProgram
import app.bowtie.core.RecentChannel
import app.bowtie.core.Recording
import app.bowtie.core.RecordingLogic
import app.bowtie.core.TunersBusyCopy
import app.bowtie.core.User
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import java.time.Instant

private const val EMPTY_COPY = "No channels yet. Ask your admin to enable some."

@OptIn(ExperimentalMaterial3Api::class, ExperimentalFoundationApi::class)
@Composable
fun ChannelListScreen(
    user: User,
    channelListViewModel: ChannelListViewModel,
    playerViewModel: PlayerViewModel,
    onOpenChannel: (Channel) -> Unit,
    onOpenSettings: () -> Unit,
    onOpenRecordings: () -> Unit,
    onOpenSearch: () -> Unit,
    continueWatching: ContinueWatchingViewModel,
    /** Continue watching: plays the recording; returns why it can't, or null. */
    onResumeRecording: suspend (Recording) -> String?,
    modifier: Modifier = Modifier,
    /** Waits for the recording player's last position save (back from it). */
    awaitRecordingSaves: suspend () -> Unit = {},
) {
    val snackbar = remember { SnackbarHostState() }
    /** Channel whose long-press menu (favorite + record) is open; read live from [state]. */
    var actionSheetId by remember { mutableStateOf<Long?>(null) }
    var conflict by remember { mutableStateOf<PendingConflict?>(null) }
    val state by channelListViewModel.state.collectAsStateWithLifecycle()
    val playingChannel by playerViewModel.currentChannel.collectAsStateWithLifecycle()
    val channelsStale by playerViewModel.channelsStale.collectAsStateWithLifecycle()
    val recents by channelListViewModel.recents.collectAsStateWithLifecycle()
    val message by channelListViewModel.message.collectAsStateWithLifecycle()
    val filter by channelListViewModel.filter.collectAsStateWithLifecycle()
    val continueItems by continueWatching.items.collectAsStateWithLifecycle()
    val continueMessage by continueWatching.message.collectAsStateWithLifecycle()
    val context = LocalContext.current
    val lifecycleOwner = LocalLifecycleOwner.current
    val scope = rememberCoroutineScope()
    var refreshing by remember { mutableStateOf(false) }
    val listState = rememberLazyListState()
    // A new chip starts the list from the top (not wherever the old one was scrolled).
    val selectFilter: (GuideFilter) -> Unit = { f ->
        if (f != filter) {
            channelListViewModel.setFilter(f)
            scope.launch { listState.scrollToItem(0) }
        }
    }

    // Foreground + every 30 s while STARTED: which channels can be started
    // (tuners taken / freed), and the full 5-minute reload when it's due.
    LaunchedEffect(channelListViewModel, lifecycleOwner) {
        lifecycleOwner.lifecycle.repeatOnLifecycle(Lifecycle.State.STARTED) {
            channelListViewModel.recheck()
            while (isActive) {
                delay(ChannelListViewModel.RECHECK_INTERVAL.toMillis())
                channelListViewModel.recheck()
            }
        }
    }

    // Back from the player (a route change, not ON_START): the Recent row may have grown.
    LaunchedEffect(channelListViewModel) {
        channelListViewModel.refreshRecents()
    }

    // Shown (incl. back from a recording or Recordings) and every return to the
    // foreground (ON_RESUME): wait for the player's last position save, then
    // reload the row; a recording moves to the front, or leaves when finished.
    LaunchedEffect(continueWatching, lifecycleOwner) {
        lifecycleOwner.lifecycle.repeatOnLifecycle(Lifecycle.State.RESUMED) {
            awaitRecordingSaves()
            continueWatching.refresh()
        }
    }

    // A remove the server refused.
    LaunchedEffect(continueMessage) {
        val text = continueMessage ?: return@LaunchedEffect
        Toast.makeText(context, text, Toast.LENGTH_SHORT).show()
        continueWatching.clearMessage()
    }

    fun resume(r: Recording) {
        scope.launch {
            val error = onResumeRecording(r) ?: return@launch
            Toast.makeText(context, error, Toast.LENGTH_SHORT).show()
        }
    }

    // A favorite toggle the server refused (already reverted).
    LaunchedEffect(message) {
        val text = message ?: return@LaunchedEffect
        Toast.makeText(context, text, Toast.LENGTH_SHORT).show()
        channelListViewModel.consumeMessage()
    }

    // 404 / channelsStale from the player → force reload.
    LaunchedEffect(channelsStale) {
        if (channelsStale) {
            channelListViewModel.refresh()
            playerViewModel.clearChannelsStale()
        }
    }

    fun record(channelId: Long, program: GuideProgram, force: Boolean) {
        scope.launch {
            when (val result = channelListViewModel.record(channelId, program, force)) {
                is ChannelListViewModel.ScheduleResult.Scheduled -> {
                    val line = "Set to record: ${program.title}"
                    snackbar.showSnackbar(result.warning?.let { "$line. $it" } ?: line)
                }
                is ChannelListViewModel.ScheduleResult.Conflict ->
                    conflict = PendingConflict(channelId, program, result.error)
                is ChannelListViewModel.ScheduleResult.Failed -> snackbar.showSnackbar(result.message)
            }
        }
    }

    fun recordSeries(channelId: Long, program: GuideProgram) {
        scope.launch {
            when (val result = channelListViewModel.recordSeries(channelId, program)) {
                is SeriesResult.Scheduled -> snackbar.showSnackbar("${program.title}: ${result.message}")
                is SeriesResult.Failed -> snackbar.showSnackbar(result.message)
            }
        }
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
                    .padding(horizontal = BowtieDimens.screenPadding, vertical = 12.dp),
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
                }
                Row {
                    TextButton(onClick = onOpenSearch) {
                        Text("Search", color = BowtieColors.amber)
                    }
                    TextButton(onClick = onOpenRecordings) {
                        Text("Recordings", color = BowtieColors.amber)
                    }
                    TextButton(onClick = onOpenSettings) {
                        Text("Settings", color = BowtieColors.amber)
                    }
                }
            }

            HorizontalDivider(color = BowtieColors.line)

            if (state is ChannelListViewModel.LoadState.Loaded) {
                GuideFilterChips(selected = filter, onSelect = selectFilter)
                HorizontalDivider(color = BowtieColors.line)
            }

            PullToRefreshBox(
                isRefreshing = refreshing || state is ChannelListViewModel.LoadState.Loading,
                onRefresh = {
                    scope.launch {
                        refreshing = true
                        try {
                            channelListViewModel.refresh()
                            continueWatching.refresh()
                        } finally {
                            refreshing = false
                        }
                    }
                },
                modifier = Modifier.fillMaxSize(),
            ) {
                when (val s = state) {
                    is ChannelListViewModel.LoadState.Loading -> {
                        Box(
                            modifier = Modifier.fillMaxSize(),
                            contentAlignment = Alignment.Center,
                        ) {
                            CircularProgressIndicator(color = BowtieColors.amber)
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
                            TextButton(onClick = {
                                scope.launch { channelListViewModel.refresh() }
                            }) {
                                Text("Try again", color = BowtieColors.amber)
                            }
                        }
                    }
                    is ChannelListViewModel.LoadState.Loaded -> {
                        // Worked out when the rows or the chip change, not on every
                        // recomposition. "Now" is fixed with them; the 5-minute
                        // reload bounds how stale it gets.
                        val filtered = remember(s.rows, filter) {
                            channelListViewModel.filtered(s.rows, filter, Instant.now())
                        }
                        val visible = filtered.rows
                        val favorites = remember(filtered) { filtered.favorites }
                        val others = remember(filtered) { filtered.others }
                        // Recent leaves out channels nobody can start right now, too.
                        val shownRecents = remember(recents, s.rows) {
                            channelListViewModel.visibleRecents(recents)
                        }
                        val showStars = s.favoritesSupported
                        val channelRow: @Composable (ChannelListViewModel.Row) -> Unit = { row ->
                            ChannelRow(
                                row = row,
                                isPlaying = playingChannel?.id == row.channel.id,
                                showStar = showStars,
                                highlight = filtered.highlight(row),
                                onClick = { onOpenChannel(row.channel) },
                                onToggleFavorite = { channelListViewModel.toggleFavorite(row.id) },
                                onLongClick = { actionSheetId = row.id },
                            )
                            HorizontalDivider(color = BowtieColors.line)
                        }
                        LazyColumn(state = listState, modifier = Modifier.fillMaxSize()) {
                            if (filtered.tunersBusy && !filtered.noneWatchable) {
                                item(key = "tuners-busy-note") {
                                    TunersBusyNote(TunersBusyCopy.LIST_NOTE)
                                    HorizontalDivider(color = BowtieColors.line)
                                }
                            }
                            if (continueItems.isNotEmpty()) {
                                item(key = "continue-row") {
                                    ContinueWatchingRow(
                                        items = continueItems,
                                        onResume = ::resume,
                                        onRemove = { r -> scope.launch { continueWatching.remove(r) } },
                                    )
                                    HorizontalDivider(color = BowtieColors.line)
                                }
                            }
                            if (showStars && shownRecents.isNotEmpty()) {
                                item(key = "recent-row") {
                                    RecentRow(
                                        recents = shownRecents,
                                        onOpen = { onOpenChannel(channelListViewModel.channelFor(it)) },
                                    )
                                    HorizontalDivider(color = BowtieColors.line)
                                }
                            }
                            if (favorites.isNotEmpty()) {
                                stickyHeader(key = "header-favorites") {
                                    SectionHeader("Favorites")
                                }
                                items(favorites, key = { it.id }) { channelRow(it) }
                                if (others.isNotEmpty()) {
                                    stickyHeader(key = "header-channels") {
                                        SectionHeader("Channels")
                                    }
                                }
                            }
                            items(others, key = { it.id }) { channelRow(it) }
                            if (filtered.noneWatchable) {
                                item(key = "none-watchable") {
                                    NoneWatchable()
                                }
                            } else if (visible.isEmpty() && filter != GuideFilter.ALL) {
                                item(key = "filter-empty") {
                                    FilterEmpty(filter = filter, onShowAll = {
                                        selectFilter(GuideFilter.ALL)
                                    })
                                }
                            }
                        }
                    }
                }
            }
        }
        SnackbarHost(snackbar, modifier = Modifier.align(Alignment.BottomCenter))
    }

    val loaded = state as? ChannelListViewModel.LoadState.Loaded
    val sheetRow = actionSheetId?.let { id -> loaded?.rows?.firstOrNull { it.id == id } }
    if (actionSheetId != null && sheetRow == null) {
        // The channel left the list (refresh/empty/failed): close the menu.
        LaunchedEffect(actionSheetId) { actionSheetId = null }
    }
    sheetRow?.let { row ->
        ModalBottomSheet(
            onDismissRequest = { actionSheetId = null },
            containerColor = BowtieColors.surface,
        ) {
            ChannelActionSheet(
                row = row,
                showFavorite = loaded?.favoritesSupported == true,
                onWatch = {
                    actionSheetId = null
                    onOpenChannel(row.channel)
                },
                onToggleFavorite = {
                    actionSheetId = null
                    channelListViewModel.toggleFavorite(row.id)
                },
                onRecord = { program ->
                    actionSheetId = null
                    record(row.channel.id, program, force = false)
                },
                onRecordSeries = { program ->
                    actionSheetId = null
                    recordSeries(row.channel.id, program)
                },
            )
        }
    }

    conflict?.let { c ->
        AlertDialog(
            onDismissRequest = { conflict = null },
            title = { Text("Not enough tuners") },
            text = { Text(RecordingLogic.conflictMessage(c.error)) },
            confirmButton = {
                TextButton(onClick = {
                    conflict = null
                    record(c.channelId, c.program, force = true)
                }) { Text("Record anyway", color = BowtieColors.amber) }
            },
            dismissButton = {
                TextButton(onClick = { conflict = null }) {
                    Text("Don't record", color = BowtieColors.text)
                }
            },
            containerColor = BowtieColors.surface,
        )
    }
}

/** A 409 waiting on "Record anyway". */
private data class PendingConflict(
    val channelId: Long,
    val program: GuideProgram,
    val error: BowtieError.RecordingConflict,
)

/**
 * Long-press menu on a channel: watch, add/remove favorite (when the server
 * supports favorites), and record what's on now or next.
 */
@Composable
private fun ChannelActionSheet(
    row: ChannelListViewModel.Row,
    showFavorite: Boolean,
    onWatch: () -> Unit,
    onToggleFavorite: () -> Unit,
    onRecord: (GuideProgram) -> Unit,
    onRecordSeries: (GuideProgram) -> Unit,
) {
    Column(modifier = Modifier.padding(bottom = 24.dp)) {
        Text(
            text = "${row.channel.guideNumber} ${row.channel.name}",
            style = BowtieType.title,
            color = BowtieColors.text,
            modifier = Modifier.padding(horizontal = BowtieDimens.screenPadding, vertical = 8.dp),
        )
        SheetItem(text = "Watch", onClick = onWatch)
        if (showFavorite) {
            SheetItem(
                text = if (row.isFavorite) "Remove from favorites" else "Add to favorites",
                onClick = onToggleFavorite,
            )
        }
        val programs = listOfNotNull(
            row.nowNext.now?.let { "On now" to it },
            row.nowNext.next?.let { "Next" to it },
        )
        if (programs.isEmpty()) {
            Text(
                text = "No guide data, so there's nothing to record.",
                style = BowtieType.label,
                color = BowtieColors.dim,
                modifier = Modifier.padding(horizontal = BowtieDimens.screenPadding, vertical = 12.dp),
            )
        }
        programs.forEach { (whenLabel, program) ->
            val window = RecordingLogic.formatWhen(program.start, program.stop)
            if (program.recording != null) {
                SheetItem(
                    text = "Set to record: ${program.title}",
                    detail = "$whenLabel · $window · manage it in Recordings",
                    onClick = null,
                )
            } else {
                SheetItem(
                    text = "Record \"${program.title}\"",
                    detail = "$whenLabel · $window",
                    onClick = { onRecord(program) },
                )
            }
        }
        // One per show (now and next are often the same show); offered even if this airing is set.
        programs.map { it.second }.distinctBy { it.title }.forEach { program ->
            SheetItem(
                text = "Record series \"${program.title}\"",
                detail = "Every new episode on ${row.channel.guideNumber} ${row.channel.name}",
                onClick = { onRecordSeries(program) },
            )
        }
    }
}

@Composable
private fun SheetItem(text: String, detail: String? = null, onClick: (() -> Unit)?) {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .then(if (onClick != null) Modifier.clickable(onClick = onClick) else Modifier)
            .padding(horizontal = BowtieDimens.screenPadding, vertical = 12.dp),
    ) {
        Text(
            text = text,
            style = BowtieType.body,
            color = if (onClick != null) BowtieColors.text else BowtieColors.dim,
        )
        if (detail != null) {
            Text(detail, style = BowtieType.label, color = BowtieColors.dim)
        }
    }
}

/** Pinned section label ("Favorites", "Channels"); opaque so rows scroll under it. */
/** Category chips: All · Sports · Movies · News · Kids · New (toggle semantics). */
@Composable
private fun GuideFilterChips(selected: GuideFilter, onSelect: (GuideFilter) -> Unit) {
    LazyRow(
        horizontalArrangement = Arrangement.spacedBy(8.dp),
        contentPadding = PaddingValues(horizontal = BowtieDimens.screenPadding, vertical = 4.dp),
        modifier = Modifier.semantics { contentDescription = "Show programs" },
    ) {
        items(GuideFilter.entries, key = { it.name }) { f ->
            FilterChip(
                selected = f == selected,
                onClick = { onSelect(f) },
                label = { Text(f.label, style = BowtieType.label) },
                colors = FilterChipDefaults.filterChipColors(
                    containerColor = BowtieColors.bg,
                    labelColor = BowtieColors.dim,
                    selectedContainerColor = BowtieColors.amber,
                    selectedLabelColor = BowtieColors.bg,
                ),
                border = FilterChipDefaults.filterChipBorder(
                    enabled = true,
                    selected = f == selected,
                    borderColor = BowtieColors.line,
                    selectedBorderColor = BowtieColors.amber,
                ),
            )
        }
    }
}

/** "No sports on in this time window" with a way back. */
@Composable
private fun FilterEmpty(filter: GuideFilter, onShowAll: () -> Unit) {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .padding(BowtieDimens.screenPadding),
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        Spacer(Modifier.height(32.dp))
        Text(text = filter.emptyCopy, style = BowtieType.body, color = BowtieColors.dim)
        Spacer(Modifier.height(12.dp))
        TextButton(onClick = onShowAll) {
            Text("Show all channels", color = BowtieColors.amber)
        }
    }
}

/** Above the list while busy channels are hidden; announced when it appears. */
@Composable
private fun TunersBusyNote(text: String) {
    Text(
        text = text,
        style = BowtieType.label,
        color = BowtieColors.amber,
        modifier = Modifier
            .fillMaxWidth()
            .semantics { liveRegion = LiveRegionMode.Polite }
            .padding(horizontal = BowtieDimens.screenPadding, vertical = 10.dp),
    )
}

/** No channel can be started right now; the 30 s re-check brings them back. */
@Composable
private fun NoneWatchable() {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .padding(BowtieDimens.screenPadding),
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        Spacer(Modifier.height(32.dp))
        Text(
            text = TunersBusyCopy.NONE_WATCHABLE,
            style = BowtieType.body,
            color = BowtieColors.dim,
            modifier = Modifier.semantics { liveRegion = LiveRegionMode.Polite },
        )
    }
}

@Composable
private fun SectionHeader(title: String) {
    Text(
        text = title.uppercase(),
        style = BowtieType.label,
        color = BowtieColors.dim,
        modifier = Modifier
            .fillMaxWidth()
            .background(BowtieColors.bg)
            .padding(horizontal = BowtieDimens.screenPadding, vertical = 8.dp),
    )
}

/** Horizontal strip of recently watched channels; a tap plays the channel. */
@Composable
private fun RecentRow(
    recents: List<RecentChannel>,
    onOpen: (RecentChannel) -> Unit,
) {
    Column(modifier = Modifier.padding(vertical = 10.dp)) {
        Text(
            text = "RECENT",
            style = BowtieType.label,
            color = BowtieColors.dim,
            modifier = Modifier.padding(horizontal = BowtieDimens.screenPadding),
        )
        Spacer(Modifier.height(8.dp))
        LazyRow(
            horizontalArrangement = Arrangement.spacedBy(8.dp),
            contentPadding = PaddingValues(horizontal = BowtieDimens.screenPadding),
        ) {
            items(recents, key = { it.channelId }) { recent ->
                Column(
                    modifier = Modifier
                        .width(112.dp)
                        .clip(RoundedCornerShape(BowtieDimens.cornerRadius))
                        .background(BowtieColors.surface)
                        .clickable(onClickLabel = "Play ${recent.name}") { onOpen(recent) }
                        .padding(horizontal = 12.dp, vertical = 10.dp),
                ) {
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

/** Trailing ☆/★ toggle. */
@Composable
private fun FavoriteStar(isFavorite: Boolean, onToggle: () -> Unit) {
    IconButton(
        onClick = onToggle,
        modifier = Modifier.semantics {
            contentDescription = if (isFavorite) "Remove from favorites" else "Add to favorites"
            stateDescription = if (isFavorite) "Favorite" else "Not favorite"
        },
    ) {
        Text(
            text = if (isFavorite) "★" else "☆",
            style = BowtieType.title,
            color = if (isFavorite) BowtieColors.amber else BowtieColors.dim,
        )
    }
}

@OptIn(ExperimentalFoundationApi::class)
@Composable
private fun ChannelRow(
    row: ChannelListViewModel.Row,
    isPlaying: Boolean,
    showStar: Boolean,
    highlight: GuideFilter.RowHighlight,
    onClick: () -> Unit,
    onToggleFavorite: () -> Unit,
    onLongClick: () -> Unit,
) {
    val haptics = LocalHapticFeedback.current
    val now = row.nowNext.now
    val next = row.nowNext.next
    val progress = ChannelListViewModel.programProgress(now, Instant.now())
    val numberColor = if (isPlaying) BowtieColors.amber else BowtieColors.text

    Row(
        modifier = Modifier
            .fillMaxWidth()
            .combinedClickable(
                onClick = onClick,
                // One menu for both features: favorite/unfavorite and record.
                onLongClickLabel = if (showStar) "Favorite or record" else "Record",
                onLongClick = {
                    haptics.performHapticFeedback(HapticFeedbackType.LongPress)
                    onLongClick()
                },
            )
            // Last tune got no signal: dim (still tappable; signal may return).
            .alpha(if (row.channel.hasNoSignal) 0.55f else 1f)
            .padding(
                horizontal = BowtieDimens.screenPadding,
                vertical = BowtieDimens.rowPadding,
            ),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(
            text = row.channel.guideNumber,
            style = BowtieType.channelNumber,
            color = numberColor,
            modifier = Modifier.width(72.dp),
        )
        Spacer(Modifier.width(12.dp))
        Column(modifier = Modifier.weight(1f)) {
            Text(
                text = row.channel.name,
                style = BowtieType.body,
                color = BowtieColors.text,
            )
            if (row.channel.hasNoSignal) {
                Text(
                    text = "NO SIGNAL",
                    style = BowtieType.label,
                    color = BowtieColors.alert,
                )
            }
            if (now != null) {
                Spacer(Modifier.height(4.dp))
                Row(
                    verticalAlignment = Alignment.CenterVertically,
                    modifier = Modifier.alpha(if (highlight.nowMatches) 1f else 0.4f),
                ) {
                    RecordMark(now)
                    Text(
                        text = now.title,
                        style = BowtieType.body.copy(color = BowtieColors.text),
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis,
                        modifier = Modifier.weight(1f, fill = false),
                    )
                    LockMark(now)
                }
                Spacer(Modifier.height(6.dp))
                ProgressCapsule(progress = progress)
            } else {
                Spacer(Modifier.height(4.dp))
                Text(
                    text = "No guide data",
                    style = BowtieType.label,
                    color = BowtieColors.dim,
                )
            }
            if (next != null) {
                Spacer(Modifier.height(4.dp))
                Row(
                    verticalAlignment = Alignment.CenterVertically,
                    modifier = Modifier.alpha(if (highlight.nextMatches) 1f else 0.4f),
                ) {
                    RecordMark(next)
                    Text(
                        text = "Next: ${next.title}",
                        style = BowtieType.label,
                        color = BowtieColors.dim,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis,
                        modifier = Modifier.weight(1f, fill = false),
                    )
                    LockMark(next)
                }
            }
            highlight.later?.let { later ->
                Spacer(Modifier.height(4.dp))
                Text(
                    text = GuideFilter.laterLine(later),
                    style = BowtieType.label,
                    color = BowtieColors.amber,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
            }
        }
        if (showStar) {
            FavoriteStar(isFavorite = row.isFavorite, onToggle = onToggleFavorite)
        }
    }
}

/** Red dot before a program that's set to record. */
@Composable
private fun RecordMark(program: GuideProgram) {
    if (program.recording == null) return
    Text(
        text = "● ",
        style = BowtieType.label,
        color = BowtieColors.alert,
        modifier = Modifier.semantics { contentDescription = "Set to record" },
    )
}

/** Lock and rating after a program parental controls block for this user. */
@Composable
internal fun LockMark(program: GuideProgram) {
    val label = RecordingLogic.lockLabel(program) ?: return
    Text(
        text = "  $label",
        style = BowtieType.label,
        color = BowtieColors.amber,
        maxLines = 1,
        modifier = Modifier.semantics { contentDescription = "Blocked by parental controls, $label" },
    )
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
