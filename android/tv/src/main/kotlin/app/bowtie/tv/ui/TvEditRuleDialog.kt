package app.bowtie.tv.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.unit.dp
import androidx.compose.ui.window.Dialog
import androidx.tv.material3.Button
import androidx.tv.material3.Text
import app.bowtie.tv.BowtieColors
import app.bowtie.tv.BowtieDimens
import app.bowtie.core.RecordingRule
import app.bowtie.core.vm.RecordingsViewModel
import kotlinx.coroutines.launch

@Composable
internal fun TvEditRuleDialog(rule: RecordingRule, viewModel: RecordingsViewModel, onClose: () -> Unit) {
    var newOnly by remember { mutableStateOf(rule.newOnly) }
    var channelId by remember { mutableStateOf(rule.channelId) }
    var keep by remember { mutableStateOf(rule.keepLatest) }
    var channels by remember { mutableStateOf(mapOf(0L to "Any channel") + if (rule.channelId == 0L) emptyMap() else mapOf(rule.channelId to rule.channelName)) }
    var busy by remember { mutableStateOf(false) }
    var error by remember { mutableStateOf<String?>(null) }
    val scope = rememberCoroutineScope()
    val first = remember { FocusRequester() }
    LaunchedEffect(rule.id) { channels = mapOf(0L to "Any channel") + viewModel.ruleChannels(rule) }
    Dialog(onDismissRequest = { if (!busy) onClose() }) {
        LaunchedEffect(Unit) { first.requestFocus() }
        Column(Modifier.widthIn(max = 640.dp).background(BowtieColors.surface, RoundedCornerShape(BowtieDimens.cornerRadius)).verticalScroll(rememberScrollState()).padding(32.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
            Text("Edit ${rule.title}")
            Button(enabled = !busy, modifier = Modifier.focusRequester(first), onClick = { newOnly = !newOnly }) { Text(if (newOnly) "New episodes only" else "All episodes") }
            Button(enabled = !busy, onClick = {
                val ids = channels.keys.toList()
                channelId = ids[(ids.indexOf(channelId) + 1) % ids.size]
            }) { Text(channels[channelId] ?: rule.channelName) }
            Text(if (keep == 0) "Keep all" else "Keep latest $keep")
            Row(horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                Button(enabled = !busy, onClick = { keep = 0 }) { Text("Keep all") }
                Button(enabled = !busy, onClick = { keep = maxOf(1, keep - 1) }) { Text("Fewer") }
                Button(enabled = !busy && keep < Int.MAX_VALUE, onClick = { keep++ }) { Text("More") }
            }
            Text("The keep limit applies when new episodes finish recording. Saving deletes no recorded episodes.")
            error?.let { Text(it) }
            Row(horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                Button(enabled = !busy, onClick = onClose) { Text("Cancel") }
                Button(enabled = !busy, onClick = {
                    busy = true
                    scope.launch {
                        if (viewModel.updateRule(rule, newOnly, channelId, keep)) onClose()
                        else error = viewModel.state.value.message
                        busy = false
                    }
                }) { Text(if (busy) "Saving…" else "Save") }
            }
        }
    }
}
