package app.bowtie.ui

import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import app.bowtie.core.RecordingRule
import app.bowtie.core.vm.RecordingsViewModel
import kotlinx.coroutines.launch

@Composable
internal fun EditRuleDialog(rule: RecordingRule, viewModel: RecordingsViewModel, onClose: () -> Unit) {
    var newOnly by remember { mutableStateOf(rule.newOnly) }
    var channelId by remember { mutableStateOf(rule.channelId) }
    var keep by remember { mutableStateOf(rule.keepLatest) }
    var channels by remember { mutableStateOf(mapOf(0L to "Any channel") + if (rule.channelId == 0L) emptyMap() else mapOf(rule.channelId to rule.channelName)) }
    var busy by remember { mutableStateOf(false) }
    var error by remember { mutableStateOf<String?>(null) }
    val scope = rememberCoroutineScope()
    LaunchedEffect(rule.id) { channels = mapOf(0L to "Any channel") + viewModel.ruleChannels(rule) }
    AlertDialog(
        onDismissRequest = { if (!busy) onClose() },
        title = { Text("Edit ${rule.title}") },
        text = {
            Column(Modifier.verticalScroll(rememberScrollState())) {
                TextButton(enabled = !busy, onClick = { newOnly = !newOnly }) { Text(if (newOnly) "New episodes only" else "All episodes") }
                channels.forEach { (id, name) ->
                    TextButton(enabled = !busy, onClick = { channelId = id }) { Text(if (channelId == id) "✓ $name" else name) }
                }
                Text(if (keep == 0) "Keep all" else "Keep latest $keep")
                Row {
                    TextButton(enabled = !busy, onClick = { keep = 0 }) { Text("Keep all") }
                    TextButton(enabled = !busy, onClick = { keep = maxOf(1, keep - 1) }) { Text("−") }
                    TextButton(enabled = !busy && keep < Int.MAX_VALUE, onClick = { keep++ }) { Text("+") }
                }
                Text("The keep limit applies when new episodes finish recording. Saving deletes no recorded episodes.")
                error?.let { Text(it) }
            }
        },
        confirmButton = {
            TextButton(enabled = !busy, onClick = {
                busy = true
                scope.launch {
                    if (viewModel.updateRule(rule, newOnly, channelId, keep)) onClose()
                    else error = viewModel.state.value.message
                    busy = false
                }
            }) { Text(if (busy) "Saving…" else "Save") }
        },
        dismissButton = { TextButton(enabled = !busy, onClick = onClose) { Text("Cancel") } },
    )
}
