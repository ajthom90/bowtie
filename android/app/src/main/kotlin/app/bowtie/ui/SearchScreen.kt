package app.bowtie.ui

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
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
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
import androidx.compose.ui.platform.LocalSoftwareKeyboardController
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import app.bowtie.BowtieColors
import app.bowtie.BowtieDimens
import app.bowtie.BowtieType
import app.bowtie.core.BowtieError
import app.bowtie.core.Channel
import app.bowtie.core.GuideSearchResult
import app.bowtie.core.RecordingLogic
import app.bowtie.core.vm.ChannelListViewModel
import app.bowtie.core.vm.SearchViewModel
import app.bowtie.core.vm.SeriesResult
import kotlinx.coroutines.launch
import java.time.Instant

/**
 * Search the guide by title or episode. Each airing shows its episode,
 * channel and time (and a lock with the rating when parental controls block
 * it), with Watch while it's on, Record and Record series.
 */
@Composable
fun SearchScreen(
    viewModel: SearchViewModel,
    onWatch: (channel: Channel, title: String) -> Unit,
    onBack: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val scope = rememberCoroutineScope()
    val snackbar = remember { SnackbarHostState() }
    val focus = remember { FocusRequester() }
    val keyboard = LocalSoftwareKeyboardController.current
    var conflict by remember { mutableStateOf<Pair<GuideSearchResult, BowtieError.RecordingConflict>?>(null) }

    BackHandler { onBack() }

    LaunchedEffect(Unit) {
        if (state.query.isEmpty()) runCatching { focus.requestFocus() }
    }

    fun record(r: GuideSearchResult, force: Boolean) {
        scope.launch {
            when (val result = viewModel.record(r, force)) {
                is ChannelListViewModel.ScheduleResult.Scheduled -> {
                    val line = "Set to record: ${r.title}"
                    snackbar.showSnackbar(result.warning?.let { "$line. $it" } ?: line)
                }
                is ChannelListViewModel.ScheduleResult.Conflict -> conflict = r to result.error
                is ChannelListViewModel.ScheduleResult.Failed -> snackbar.showSnackbar(result.message)
            }
        }
    }

    fun recordSeries(r: GuideSearchResult) {
        scope.launch {
            when (val result = viewModel.recordSeries(r)) {
                is SeriesResult.Scheduled -> snackbar.showSnackbar("${r.title}: ${result.message}")
                is SeriesResult.Failed -> snackbar.showSnackbar(result.message)
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
                    text = "Search",
                    style = BowtieType.title,
                    color = BowtieColors.text,
                    modifier = Modifier.padding(start = 4.dp),
                )
            }
            OutlinedTextField(
                value = state.query,
                onValueChange = viewModel::onQueryChange,
                modifier = Modifier
                    .fillMaxWidth()
                    .padding(horizontal = BowtieDimens.screenPadding)
                    .focusRequester(focus),
                singleLine = true,
                placeholder = { Text("Show or episode") },
                keyboardOptions = KeyboardOptions(imeAction = ImeAction.Search),
                keyboardActions = KeyboardActions(onSearch = {
                    keyboard?.hide()
                    scope.launch { viewModel.search() }
                }),
                shape = RoundedCornerShape(BowtieDimens.cornerRadius),
                colors = bowtieTextFieldColors(),
            )
            Spacer(Modifier.height(8.dp))
            HorizontalDivider(color = BowtieColors.line)

            when (val results = state.results) {
                is SearchViewModel.Results.Idle -> Message("Find a show by its title or an episode's name.")
                is SearchViewModel.Results.Loading -> Box(
                    Modifier.fillMaxSize(),
                    contentAlignment = Alignment.Center,
                ) { CircularProgressIndicator(color = BowtieColors.amber) }
                is SearchViewModel.Results.Failed -> Message(results.message, BowtieColors.alert)
                is SearchViewModel.Results.Loaded -> {
                    if (results.items.isEmpty()) {
                        Message("Nothing on the guide matches \"${state.query.trim()}\".")
                    } else {
                        LazyColumn(Modifier.fillMaxSize()) {
                            items(results.items, key = { "${it.channelId}-${it.start}" }) { r ->
                                val onNow = viewModel.isOnNow(r)
                                SearchRow(
                                    result = r,
                                    onNow = onNow,
                                    onWatch = if (onNow) {
                                        { onWatch(viewModel.channelFor(r), r.title) }
                                    } else {
                                        null
                                    },
                                    onRecord = { record(r, force = false) },
                                    onRecordSeries = { recordSeries(r) },
                                )
                                HorizontalDivider(color = BowtieColors.line)
                            }
                        }
                    }
                }
            }
        }
        SnackbarHost(snackbar, modifier = Modifier.align(Alignment.BottomCenter))
    }

    conflict?.let { (r, error) ->
        AlertDialog(
            onDismissRequest = { conflict = null },
            title = { Text("Not enough tuners") },
            text = { Text(RecordingLogic.conflictMessage(error)) },
            confirmButton = {
                TextButton(onClick = {
                    conflict = null
                    record(r, force = true)
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

@Composable
private fun Message(text: String, color: androidx.compose.ui.graphics.Color = BowtieColors.dim) {
    Text(
        text = text,
        style = BowtieType.body,
        color = color,
        modifier = Modifier.padding(BowtieDimens.screenPadding),
    )
}

@Composable
private fun SearchRow(
    result: GuideSearchResult,
    onNow: Boolean,
    onWatch: (() -> Unit)?,
    onRecord: () -> Unit,
    onRecordSeries: () -> Unit,
) {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = BowtieDimens.screenPadding, vertical = 12.dp),
    ) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            if (result.recording != null) {
                Text("● ", style = BowtieType.label, color = BowtieColors.alert)
            }
            Text(
                text = result.title,
                style = BowtieType.body,
                color = BowtieColors.text,
                maxLines = 2,
                overflow = TextOverflow.Ellipsis,
                modifier = Modifier.weight(1f, fill = false),
            )
            if (onNow) {
                Spacer(Modifier.width(8.dp))
                Text(
                    text = "ON NOW",
                    style = BowtieType.label.copy(fontSize = BowtieType.label.fontSize * 0.8f),
                    color = BowtieColors.alert,
                    modifier = Modifier
                        .background(BowtieColors.raised, RoundedCornerShape(4.dp))
                        .padding(horizontal = 6.dp, vertical = 2.dp),
                )
            }
        }
        if (result.subtitle.isNotEmpty()) {
            Text(result.subtitle, style = BowtieType.label, color = BowtieColors.dim, maxLines = 1)
        }
        RecordingLogic.lockLabel(result)?.let {
            Text(it, style = BowtieType.label, color = BowtieColors.amber)
        }
        Spacer(Modifier.height(4.dp))
        Text(
            text = "${result.guideNumber} ${result.channelName} · " +
                RecordingLogic.formatWhen(result.start, result.stop, Instant.now()),
            style = BowtieType.label,
            color = BowtieColors.dim,
        )
        Row(horizontalArrangement = Arrangement.spacedBy(4.dp)) {
            if (onWatch != null) {
                TextButton(onClick = onWatch) { Text("Watch", color = BowtieColors.amber) }
            }
            if (result.recording == null) {
                TextButton(onClick = onRecord) { Text("Record", color = BowtieColors.amber) }
            } else {
                TextButton(onClick = {}, enabled = false) { Text("Set to record", color = BowtieColors.dim) }
            }
            TextButton(onClick = onRecordSeries) { Text("Record series", color = BowtieColors.amber) }
        }
    }
}
