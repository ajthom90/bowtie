package app.bowtie.tv.ui

import androidx.activity.compose.BackHandler
import androidx.compose.foundation.background
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
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.unit.dp
import androidx.compose.ui.window.Dialog
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.repeatOnLifecycle
import androidx.tv.material3.Button
import androidx.tv.material3.ButtonDefaults
import androidx.tv.material3.ClickableSurfaceDefaults
import androidx.tv.material3.Surface
import androidx.tv.material3.Text
import app.bowtie.core.Recording
import app.bowtie.core.RecordingLogic
import app.bowtie.core.RecordingLogic.Action
import app.bowtie.core.RecordingLogic.Tab
import app.bowtie.core.RecordingRule
import app.bowtie.core.vm.RecordingsViewModel
import app.bowtie.tv.BowtieColors
import app.bowtie.tv.BowtieDimens
import app.bowtie.tv.BowtieType
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import java.time.Instant

private const val AUTO_REFRESH_MS = 30_000L

/** A focused choice panel over the screen (actions, resume, confirm). */
internal data class Panel(
    val title: String,
    val body: String?,
    val choices: List<Pair<String, () -> Unit>>,
)

/**
 * DVR recordings for Android TV / Fire TV: tab buttons, a DPAD list of rows,
 * and a choice panel for Play / Resume, Stop, Keep and Cancel/Delete.
 * OK on a ready recording plays it; OK on anything else (or a long press)
 * opens its options.
 */
@Composable
fun TvRecordingsScreen(
    viewModel: RecordingsViewModel,
    onPlay: (start: RecordingsViewModel.PlayStart, startAtSec: Int) -> Unit,
    onBack: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val scope = rememberCoroutineScope()
    val lifecycleOwner = LocalLifecycleOwner.current
    var panel by remember { mutableStateOf<Panel?>(null) }
    val tabFocus = remember { FocusRequester() }

    LaunchedEffect(Unit) {
        runCatching { tabFocus.requestFocus() }
    }

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
        if (state.message == null) return@LaunchedEffect
        delay(5_000)
        viewModel.clearMessage()
    }

    BackHandler {
        if (panel != null) panel = null else onBack()
    }

    fun play(r: Recording) {
        scope.launch {
            val start = viewModel.play(r) ?: return@launch
            if (!start.offerResume) {
                onPlay(start, 0)
                return@launch
            }
            val at = RecordingLogic.formatClock(start.resumeAtSec)
            panel = Panel(
                title = r.title,
                body = "You stopped at $at.",
                choices = listOf(
                    "Resume from $at" to {
                        panel = null
                        onPlay(start, start.resumeAtSec)
                    },
                    "Start over" to {
                        panel = null
                        onPlay(start, 0)
                    },
                ),
            )
        }
    }

    fun confirmDelete(r: Recording) {
        panel = Panel(
            title = "Delete \"${r.title}\"?",
            body = "The recording is removed for everyone. This can't be undone.",
            choices = listOf(
                "Keep it" to { panel = null },
                "Delete" to {
                    panel = null
                    scope.launch { viewModel.delete(r) }
                },
            ),
        )
    }

    fun openShow(rule: RecordingRule) {
        val stop = if (rule.canManage) {
            listOf(
                "Stop recording this show" to {
                    panel = null
                    scope.launch { viewModel.stopShow(rule) }
                    Unit
                },
            )
        } else {
            emptyList()
        }
        panel = Panel(
            title = rule.title,
            body = listOfNotNull(
                RecordingLogic.ruleDetail(rule),
                if (rule.canManage) {
                    "Stopping cancels upcoming episodes. Recorded episodes stay."
                } else {
                    "Only the person who set it up or an admin can stop it."
                },
            ).joinToString("\n"),
            choices = stop + ("Close" to { panel = null }),
        )
    }

    fun openOptions(r: Recording) {
        val actions = RecordingLogic.actions(r)
        val choices = actions.map { action ->
            RecordingLogic.actionLabel(r, action) to {
                panel = null
                when (action) {
                    Action.Play -> play(r)
                    Action.Stop -> scope.launch { viewModel.stop(r) }
                    Action.Keep -> scope.launch { viewModel.setKept(r, keep = !r.isProtected) }
                    Action.Delete ->
                        if (RecordingLogic.deleteNeedsConfirm(r)) {
                            confirmDelete(r)
                        } else {
                            scope.launch { viewModel.delete(r) }
                        }
                }
                Unit
            }
        } + ("Close" to { panel = null })
        panel = Panel(
            title = r.title,
            body = listOfNotNull(
                "${r.channelName} · ${RecordingLogic.formatWhen(r.start, r.stop, Instant.now())}",
                RecordingLogic.statusLine(r),
                RecordingLogic.lockLabel(r)?.let { "$it · Blocked by parental controls" },
                if (actions.isEmpty()) "Only the person who scheduled it or an admin can change it." else null,
            ).joinToString("\n"),
            choices = choices,
        )
    }

    Box(modifier = modifier.fillMaxSize().background(BowtieColors.bg)) {
        Column(modifier = Modifier.fillMaxSize()) {
            Row(
                modifier = Modifier
                    .fillMaxWidth()
                    .padding(horizontal = BowtieDimens.screenPadding, vertical = 20.dp),
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(12.dp),
            ) {
                Button(onClick = onBack, colors = tvButtonColors()) {
                    Text("← Channels", style = BowtieType.body, color = BowtieColors.amber)
                }
                Text(
                    text = "Recordings",
                    style = BowtieType.title,
                    color = BowtieColors.text,
                    modifier = Modifier.padding(horizontal = 12.dp),
                )
                Spacer(Modifier.weight(1f))
                Tab.entries.forEach { tab ->
                    val selected = tab == state.tab
                    Button(
                        onClick = { scope.launch { viewModel.selectTab(tab) } },
                        colors = tvButtonColors(selected),
                        modifier = if (selected) Modifier.focusRequester(tabFocus) else Modifier,
                    ) {
                        Text(
                            text = tab.title,
                            style = BowtieType.body,
                            color = if (selected) BowtieColors.bg else BowtieColors.text,
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

            when (val load = state.load) {
                is RecordingsViewModel.Load.Loading -> CenterText("Loading…", BowtieColors.dim)
                is RecordingsViewModel.Load.Failed -> CenterText(load.message, BowtieColors.alert)
                is RecordingsViewModel.Load.Shows -> {
                    if (load.rules.isEmpty()) {
                        CenterText(emptyCopy(state.tab), BowtieColors.dim)
                    } else {
                        LazyColumn(
                            modifier = Modifier
                                .fillMaxSize()
                                .padding(horizontal = BowtieDimens.screenPadding, vertical = 12.dp),
                            verticalArrangement = Arrangement.spacedBy(8.dp),
                        ) {
                            items(load.rules, key = { it.id }) { rule ->
                                TvRuleRow(rule = rule, onClick = { openShow(rule) })
                            }
                        }
                    }
                }
                is RecordingsViewModel.Load.Loaded -> {
                    if (load.items.isEmpty()) {
                        CenterText(emptyCopy(state.tab), BowtieColors.dim)
                    } else {
                        LazyColumn(
                            modifier = Modifier
                                .fillMaxSize()
                                .padding(horizontal = BowtieDimens.screenPadding, vertical = 12.dp),
                            verticalArrangement = Arrangement.spacedBy(8.dp),
                        ) {
                            items(load.items, key = { it.id }) { r ->
                                TvRecordingRow(
                                    recording = r,
                                    onClick = {
                                        if (Action.Play in RecordingLogic.actions(r)) play(r) else openOptions(r)
                                    },
                                    onLongClick = { openOptions(r) },
                                )
                            }
                            item {
                                Text(
                                    text = "Press and hold OK for more options.",
                                    style = BowtieType.label,
                                    color = BowtieColors.dim,
                                    modifier = Modifier.padding(vertical = 12.dp),
                                )
                            }
                        }
                    }
                }
            }
        }

        state.message?.let { msg ->
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

private fun emptyCopy(tab: Tab): String = when (tab) {
    Tab.Upcoming -> "Nothing set to record. Press and hold OK on a channel to record what's on."
    Tab.Recorded -> "No recordings yet."
    Tab.Missed -> "No missed recordings."
    Tab.Shows -> "No shows set to record. Press and hold OK on a channel and choose Record series."
}

@Composable
private fun TvRuleRow(rule: RecordingRule, onClick: () -> Unit) {
    Surface(
        onClick = onClick,
        modifier = Modifier.fillMaxWidth(),
        colors = ClickableSurfaceDefaults.colors(
            containerColor = BowtieColors.surface,
            contentColor = BowtieColors.text,
            focusedContainerColor = BowtieColors.raised,
            focusedContentColor = BowtieColors.text,
            pressedContainerColor = BowtieColors.raised,
            pressedContentColor = BowtieColors.text,
        ),
        shape = ClickableSurfaceDefaults.shape(shape = RoundedCornerShape(BowtieDimens.cornerRadius)),
    ) {
        Column(modifier = Modifier.padding(BowtieDimens.rowPadding)) {
            Text(rule.title, style = BowtieType.body, color = BowtieColors.text, maxLines = 1)
            Spacer(Modifier.height(4.dp))
            Text(RecordingLogic.ruleDetail(rule), style = BowtieType.label, color = BowtieColors.dim)
        }
    }
}

@Composable
private fun CenterText(text: String, color: Color) {
    Box(Modifier.fillMaxSize().padding(BowtieDimens.screenPadding), contentAlignment = Alignment.Center) {
        Text(text = text, style = BowtieType.body, color = color)
    }
}

@Composable
private fun TvRecordingRow(
    recording: Recording,
    onClick: () -> Unit,
    onLongClick: () -> Unit,
) {
    Surface(
        onClick = onClick,
        onLongClick = onLongClick,
        modifier = Modifier.fillMaxWidth(),
        colors = ClickableSurfaceDefaults.colors(
            containerColor = BowtieColors.surface,
            contentColor = BowtieColors.text,
            focusedContainerColor = BowtieColors.raised,
            focusedContentColor = BowtieColors.text,
            pressedContainerColor = BowtieColors.raised,
            pressedContentColor = BowtieColors.text,
        ),
        shape = ClickableSurfaceDefaults.shape(shape = RoundedCornerShape(BowtieDimens.cornerRadius)),
    ) {
        Column(modifier = Modifier.padding(BowtieDimens.rowPadding)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Text(
                    text = recording.title,
                    style = BowtieType.body,
                    color = BowtieColors.text,
                    maxLines = 1,
                    modifier = Modifier.weight(1f, fill = false),
                )
                RecordingLogic.badges(recording).forEach { badge ->
                    Spacer(Modifier.width(10.dp))
                    TvBadge(badge)
                }
            }
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
        }
    }
}

@Composable
private fun TvBadge(badge: RecordingLogic.Badge) {
    val color = when (badge.tone) {
        RecordingLogic.Tone.Neutral -> BowtieColors.dim
        RecordingLogic.Tone.Live, RecordingLogic.Tone.Alert -> BowtieColors.alert
        RecordingLogic.Tone.Warn -> BowtieColors.amber
        RecordingLogic.Tone.Good -> BowtieColors.signal
    }
    Text(
        text = badge.text.uppercase(),
        style = BowtieType.mono,
        color = color,
        modifier = Modifier
            .background(BowtieColors.bg, RoundedCornerShape(4.dp))
            .padding(horizontal = 8.dp, vertical = 2.dp),
    )
}

/** A dialog window, so DPAD focus stays on its buttons; BACK dismisses. */
@Composable
internal fun ChoicePanel(panel: Panel, onDismiss: () -> Unit) {
    val first = remember(panel) { FocusRequester() }
    Dialog(onDismissRequest = onDismiss) {
        // Inside the dialog's window, after its content is composed.
        LaunchedEffect(panel) { runCatching { first.requestFocus() } }
        Column(
            modifier = Modifier
                .widthIn(max = 640.dp)
                .background(BowtieColors.surface, RoundedCornerShape(BowtieDimens.cornerRadius))
                .padding(32.dp),
            verticalArrangement = Arrangement.spacedBy(12.dp),
        ) {
            Text(panel.title, style = BowtieType.title, color = BowtieColors.text)
            panel.body?.takeIf { it.isNotEmpty() }?.let {
                Text(it, style = BowtieType.body, color = BowtieColors.dim)
            }
            Spacer(Modifier.height(8.dp))
            panel.choices.forEachIndexed { i, (label, onClick) ->
                Button(
                    onClick = onClick,
                    colors = tvButtonColors(),
                    modifier = Modifier
                        .fillMaxWidth()
                        .then(if (i == 0) Modifier.focusRequester(first) else Modifier),
                ) {
                    Text(
                        text = label,
                        style = BowtieType.body,
                        color = if (label == "Delete" || label == "Stop recording this show") {
                            BowtieColors.alert
                        } else {
                            BowtieColors.text
                        },
                    )
                }
            }
        }
    }
}

@Composable
internal fun tvButtonColors(selected: Boolean = false) = ButtonDefaults.colors(
    containerColor = if (selected) BowtieColors.amber else BowtieColors.surface,
    contentColor = if (selected) BowtieColors.bg else BowtieColors.text,
    focusedContainerColor = if (selected) BowtieColors.amber else BowtieColors.raised,
    focusedContentColor = if (selected) BowtieColors.bg else BowtieColors.text,
    pressedContainerColor = BowtieColors.raised,
    pressedContentColor = BowtieColors.text,
    disabledContainerColor = BowtieColors.surface,
    disabledContentColor = BowtieColors.dim,
)
