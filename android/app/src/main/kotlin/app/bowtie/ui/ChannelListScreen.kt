package app.bowtie.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
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
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.HorizontalDivider
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
import androidx.compose.ui.draw.clip
import androidx.compose.ui.unit.dp
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.repeatOnLifecycle
import app.bowtie.BowtieColors
import app.bowtie.BowtieDimens
import app.bowtie.BowtieType
import app.bowtie.core.vm.ChannelListViewModel
import app.bowtie.core.vm.PlayerViewModel
import app.bowtie.core.Channel
import app.bowtie.core.User
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import java.time.Instant
import kotlin.time.Duration.Companion.minutes

private const val EMPTY_COPY = "No channels yet. Ask your admin to enable some."

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ChannelListScreen(
    user: User,
    channelListViewModel: ChannelListViewModel,
    playerViewModel: PlayerViewModel,
    onOpenChannel: (Channel) -> Unit,
    onOpenSettings: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val state by channelListViewModel.state.collectAsStateWithLifecycle()
    val playingChannel by playerViewModel.currentChannel.collectAsStateWithLifecycle()
    val channelsStale by playerViewModel.channelsStale.collectAsStateWithLifecycle()
    val lifecycleOwner = LocalLifecycleOwner.current
    val scope = rememberCoroutineScope()
    var refreshing by remember { mutableStateOf(false) }

    // Foreground + 5-minute refresh while STARTED.
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

    Column(
        modifier = modifier
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
            TextButton(onClick = onOpenSettings) {
                Text("Settings", color = BowtieColors.amber)
            }
        }

        HorizontalDivider(color = BowtieColors.line)

        PullToRefreshBox(
            isRefreshing = refreshing || state is ChannelListViewModel.LoadState.Loading,
            onRefresh = {
                scope.launch {
                    refreshing = true
                    try {
                        channelListViewModel.refresh()
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
                    LazyColumn(modifier = Modifier.fillMaxSize()) {
                        items(s.rows, key = { it.id }) { row ->
                            ChannelRow(
                                row = row,
                                isPlaying = playingChannel?.id == row.channel.id,
                                onClick = { onOpenChannel(row.channel) },
                            )
                            HorizontalDivider(color = BowtieColors.line)
                        }
                    }
                }
            }
        }
    }
}

@Composable
private fun ChannelRow(
    row: ChannelListViewModel.Row,
    isPlaying: Boolean,
    onClick: () -> Unit,
) {
    val now = row.nowNext.now
    val next = row.nowNext.next
    val progress = ChannelListViewModel.programProgress(now, Instant.now())
    val numberColor = if (isPlaying) BowtieColors.amber else BowtieColors.text

    Row(
        modifier = Modifier
            .fillMaxWidth()
            .clickable(onClick = onClick)
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
            if (now != null) {
                Spacer(Modifier.height(4.dp))
                Text(
                    text = now.title,
                    style = BowtieType.body.copy(color = BowtieColors.text),
                    maxLines = 1,
                )
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
                Text(
                    text = "Next: ${next.title}",
                    style = BowtieType.label,
                    color = BowtieColors.dim,
                    maxLines = 1,
                )
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
