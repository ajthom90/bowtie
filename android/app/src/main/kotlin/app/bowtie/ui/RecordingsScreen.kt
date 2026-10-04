package app.bowtie.ui

import androidx.activity.compose.BackHandler
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.PrimaryTabRow
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.Tab
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
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.unit.dp
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.repeatOnLifecycle
import app.bowtie.BowtieColors
import app.bowtie.BowtieDimens
import app.bowtie.BowtieType
import app.bowtie.core.Recording
import app.bowtie.core.RecordingLogic
import app.bowtie.core.RecordingLogic.Action
import app.bowtie.core.RecordingRule
import app.bowtie.core.RecordingLogic.Tab as RecTab
import app.bowtie.core.vm.RecordingsViewModel
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import java.time.Instant

/** While shown, reload so states (recording → processing → ready) move along. */
private const val AUTO_REFRESH_MS = 30_000L

/**
 * DVR recordings: Upcoming / Recorded / Missed tabs. Tapping a ready recording
 * plays it (asking "Resume / Start over" when there's a saved position);
 * Stop, Keep and Cancel/Delete appear when the caller can manage the row.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun RecordingsScreen(
    viewModel: RecordingsViewModel,
    onPlay: (start: RecordingsViewModel.PlayStart, startAtSec: Int) -> Unit,
    onBack: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val scope = rememberCoroutineScope()
    val lifecycleOwner = LocalLifecycleOwner.current
    val snackbar = remember { SnackbarHostState() }
    var refreshing by remember { mutableStateOf(false) }
    var pendingResume by remember { mutableStateOf<RecordingsViewModel.PlayStart?>(null) }
    var confirmDelete by remember { mutableStateOf<Recording?>(null) }
    var confirmStopShow by remember { mutableStateOf<RecordingRule?>(null) }

    BackHandler { onBack() }

    LaunchedEffect(viewModel, lifecycleOwner) {
        lifecycleOwner.lifecycle.repeatOnLifecycle(Lifecycle.State.STARTED) {
            viewModel.refresh()
            while (isActive) {
                delay(AUTO_REFRESH_MS)
                viewModel.refresh()
            }
        }
    }

    LaunchedEffect(state.message) {
        val msg = state.message ?: return@LaunchedEffect
        snackbar.showSnackbar(msg)
        viewModel.clearMessage()
    }

    fun play(r: Recording) {
        scope.launch {
            val start = viewModel.play(r) ?: return@launch
            if (start.offerResume) pendingResume = start else onPlay(start, 0)
        }
    }

    fun perform(r: Recording, action: Action) {
        when (action) {
            Action.Play -> play(r)
            Action.Stop -> scope.launch { viewModel.stop(r) }
            Action.Keep -> scope.launch { viewModel.setKept(r, keep = !r.isProtected) }
            Action.Delete -> {
                // Deleting files is permanent; cancelling a schedule is not.
                if (RecordingLogic.deleteNeedsConfirm(r)) {
                    confirmDelete = r
                } else {
                    scope.launch { viewModel.delete(r) }
                }
            }
        }
    }

    Box(modifier = modifier.fillMaxSize().background(BowtieColors.bg)) {
        Column(modifier = Modifier.fillMaxSize()) {
            Row(
                modifier = Modifier
                    .fillMaxWidth()
                    .padding(horizontal = 8.dp, vertical = 12.dp),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                TextButton(onClick = onBack) {
                    Text("‹ Channels", color = BowtieColors.amber)
                }
                Text(
                    text = "Recordings",
                    style = BowtieType.title,
                    color = BowtieColors.text,
                    modifier = Modifier.padding(start = 4.dp),
                )
            }

            val tabs = RecTab.entries
            PrimaryTabRow(
                selectedTabIndex = tabs.indexOf(state.tab),
                containerColor = BowtieColors.bg,
                contentColor = BowtieColors.text,
                // Default indicator uses the theme primary (amber).
                divider = { HorizontalDivider(color = BowtieColors.line) },
            ) {
                tabs.forEach { tab ->
                    Tab(
                        selected = tab == state.tab,
                        onClick = { scope.launch { viewModel.selectTab(tab) } },
                        text = {
                            Text(
                                text = tab.title,
                                color = if (tab == state.tab) BowtieColors.amber else BowtieColors.dim,
                            )
                        },
                    )
                }
            }

            PullToRefreshBox(
                isRefreshing = refreshing,
                onRefresh = {
                    scope.launch {
                        refreshing = true
                        try {
                            viewModel.refresh()
                        } finally {
                            refreshing = false
                        }
                    }
                },
                modifier = Modifier.fillMaxSize(),
            ) {
                when (val load = state.load) {
                    is RecordingsViewModel.Load.Loading -> Centered {
                        CircularProgressIndicator(color = BowtieColors.amber)
                    }
                    is RecordingsViewModel.Load.Failed -> Centered {
                        Column(horizontalAlignment = Alignment.CenterHorizontally) {
                            Text(load.message, style = BowtieType.body, color = BowtieColors.alert)
                            Spacer(Modifier.height(16.dp))
                            TextButton(onClick = { scope.launch { viewModel.refresh() } }) {
                                Text("Try again", color = BowtieColors.amber)
                            }
                        }
                    }
                    is RecordingsViewModel.Load.Shows -> {
                        LazyColumn(Modifier.fillMaxSize()) {
                            if (load.rules.isEmpty()) {
                                item {
                                    Text(
                                        text = emptyCopy(state.tab),
                                        style = BowtieType.body,
                                        color = BowtieColors.dim,
                                        modifier = Modifier.padding(BowtieDimens.screenPadding),
                                    )
                                }
                            }
                            items(load.rules, key = { it.id }) { rule ->
                                RuleRow(rule = rule, onStop = { confirmStopShow = rule })
                                HorizontalDivider(color = BowtieColors.line)
                            }
                        }
                    }
                    is RecordingsViewModel.Load.Loaded -> {
                        if (load.items.isEmpty()) {
                            // LazyColumn so pull-to-refresh still works on an empty tab.
                            LazyColumn(Modifier.fillMaxSize()) {
                                item {
                                    Text(
                                        text = emptyCopy(state.tab),
                                        style = BowtieType.body,
                                        color = BowtieColors.dim,
                                        modifier = Modifier.padding(BowtieDimens.screenPadding),
                                    )
                                }
                            }
                        } else {
                            LazyColumn(Modifier.fillMaxSize()) {
                                items(load.items, key = { it.id }) { r ->
                                    RecordingRow(
                                        recording = r,
                                        onClick = { if (Action.Play in RecordingLogic.actions(r)) play(r) },
                                        onAction = { perform(r, it) },
                                    )
                                    HorizontalDivider(color = BowtieColors.line)
                                }
                            }
                        }
                    }
                }
            }
        }

        SnackbarHost(snackbar, modifier = Modifier.align(Alignment.BottomCenter))
    }

    pendingResume?.let { start ->
        val at = RecordingLogic.formatClock(start.resumeAtSec)
        AlertDialog(
            onDismissRequest = { pendingResume = null },
            title = { Text(start.recording.title) },
            text = { Text("You stopped at $at.") },
            confirmButton = {
                TextButton(onClick = {
                    pendingResume = null
                    onPlay(start, start.resumeAtSec)
                }) { Text("Resume from $at", color = BowtieColors.amber) }
            },
            dismissButton = {
                TextButton(onClick = {
                    pendingResume = null
                    onPlay(start, 0)
                }) { Text("Start over", color = BowtieColors.text) }
            },
            containerColor = BowtieColors.surface,
        )
    }

    confirmStopShow?.let { rule ->
        AlertDialog(
            onDismissRequest = { confirmStopShow = null },
            title = { Text("Stop recording \"${rule.title}\"?") },
            text = { Text("Upcoming episodes are cancelled. Recorded episodes stay.") },
            confirmButton = {
                TextButton(onClick = {
                    confirmStopShow = null
                    scope.launch { viewModel.stopShow(rule) }
                }) { Text("Stop recording", color = BowtieColors.alert) }
            },
            dismissButton = {
                TextButton(onClick = { confirmStopShow = null }) {
                    Text("Keep recording", color = BowtieColors.text)
                }
            },
            containerColor = BowtieColors.surface,
        )
    }

    confirmDelete?.let { r ->
        AlertDialog(
            onDismissRequest = { confirmDelete = null },
            title = { Text("Delete \"${r.title}\"?") },
            text = { Text("The recording is removed for everyone. This can't be undone.") },
            confirmButton = {
                TextButton(onClick = {
                    confirmDelete = null
                    scope.launch { viewModel.delete(r) }
                }) { Text("Delete", color = BowtieColors.alert) }
            },
            dismissButton = {
                TextButton(onClick = { confirmDelete = null }) {
                    Text("Keep it", color = BowtieColors.text)
                }
            },
            containerColor = BowtieColors.surface,
        )
    }
}

private fun emptyCopy(tab: RecTab): String = when (tab) {
    RecTab.Upcoming -> "Nothing set to record. Press and hold a channel to record what's on."
    RecTab.Recorded -> "No recordings yet."
    RecTab.Missed -> "No missed recordings."
    RecTab.Shows -> "No shows set to record. Press and hold a channel and choose Record series."
}

/** A series rule: the show, where and which episodes, and "Stop recording this show" for its owner. */
@Composable
private fun RuleRow(rule: RecordingRule, onStop: () -> Unit) {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = BowtieDimens.screenPadding, vertical = 12.dp),
    ) {
        Text(rule.title, style = BowtieType.body, color = BowtieColors.text, maxLines = 2)
        Spacer(Modifier.height(4.dp))
        Text(RecordingLogic.ruleDetail(rule), style = BowtieType.label, color = BowtieColors.dim)
        if (rule.canManage) {
            TextButton(onClick = onStop) {
                Text("Stop recording this show", color = BowtieColors.alert)
            }
        }
    }
}

@Composable
private fun Centered(content: @Composable () -> Unit) {
    Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) { content() }
}

@OptIn(androidx.compose.foundation.layout.ExperimentalLayoutApi::class)
@Composable
private fun RecordingRow(
    recording: Recording,
    onClick: () -> Unit,
    onAction: (Action) -> Unit,
) {
    val actions = RecordingLogic.actions(recording)
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .clickable(enabled = Action.Play in actions, onClick = onClick)
            .padding(horizontal = BowtieDimens.screenPadding, vertical = 12.dp),
    ) {
        Text(recording.title, style = BowtieType.body, color = BowtieColors.text, maxLines = 2)
        if (recording.subtitle.isNotEmpty()) {
            Text(recording.subtitle, style = BowtieType.label, color = BowtieColors.dim, maxLines = 1)
        }
        RecordingLogic.lockLabel(recording)?.let {
            Text(it, style = BowtieType.label, color = BowtieColors.amber)
        }
        Spacer(Modifier.height(4.dp))
        Text(
            text = "${recording.channelName} · ${RecordingLogic.formatWhen(recording.start, recording.stop, Instant.now())}",
            style = BowtieType.label,
            color = BowtieColors.dim,
        )
        RecordingLogic.detailLine(recording)?.let {
            Text(it, style = BowtieType.label, color = BowtieColors.dim)
        }
        RecordingLogic.statusLine(recording)?.let {
            val color = if (recording.state == Recording.FAILED) BowtieColors.alert else BowtieColors.amber
            Text(it, style = BowtieType.label, color = color)
        }

        val badges = RecordingLogic.badges(recording)
        if (badges.isNotEmpty()) {
            Spacer(Modifier.height(6.dp))
            FlowRow(horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                badges.forEach { BadgeChip(it) }
            }
        }

        if (actions.isNotEmpty()) {
            Row(horizontalArrangement = Arrangement.spacedBy(4.dp)) {
                actions.forEach { action ->
                    TextButton(onClick = { onAction(action) }) {
                        Text(
                            text = RecordingLogic.actionLabel(recording, action),
                            color = if (action == Action.Delete) BowtieColors.alert else BowtieColors.amber,
                        )
                    }
                }
            }
        }
    }
}

@Composable
private fun BadgeChip(badge: RecordingLogic.Badge) {
    val color: Color = when (badge.tone) {
        RecordingLogic.Tone.Neutral -> BowtieColors.dim
        RecordingLogic.Tone.Live -> BowtieColors.alert
        RecordingLogic.Tone.Warn -> BowtieColors.amber
        RecordingLogic.Tone.Alert -> BowtieColors.alert
        RecordingLogic.Tone.Good -> BowtieColors.signal
    }
    Text(
        text = badge.text.uppercase(),
        style = BowtieType.label.copy(fontSize = BowtieType.label.fontSize * 0.8f),
        color = color,
        modifier = Modifier
            .background(BowtieColors.raised, RoundedCornerShape(4.dp))
            .padding(horizontal = 6.dp, vertical = 2.dp),
    )
}
