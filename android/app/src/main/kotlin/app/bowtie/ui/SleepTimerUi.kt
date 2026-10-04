package app.bowtie.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import app.bowtie.BowtieColors
import app.bowtie.BowtieType
import app.bowtie.core.SleepTimer
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive

/**
 * A [SleepTimer] for this player screen, ticked once a second while it is
 * shown. Survives recomposition (and channel changes); gone with the screen.
 * [onFire] should stop playback the same way leaving the player does.
 */
@Composable
fun rememberSleepTimer(onFire: () -> Unit): SleepTimer {
    val onFireLatest = rememberUpdatedState(onFire)
    val timer = remember { SleepTimer(onFire = { onFireLatest.value() }) }
    LaunchedEffect(timer) {
        while (isActive) {
            timer.tick()
            delay(1_000L)
        }
    }
    return timer
}

/** Control chip label: "Sleep", or the time left. */
fun sleepChipLabel(status: SleepTimer.Status): String =
    status.remainingMs?.let { "Sleep ${SleepTimer.format(it)}" } ?: "Sleep"

/** Sleep timer choices (bottom sheet, like Quality). */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun SleepTimerSheet(
    timer: SleepTimer,
    /** When the program now on the channel ends; null hides End of this program. */
    programEndMs: Long?,
    onDismiss: () -> Unit,
) {
    val status by timer.status.collectAsStateWithLifecycle()
    val options = remember(programEndMs) {
        SleepTimer.options(programEndMs, System.currentTimeMillis())
    }
    ModalBottomSheet(
        onDismissRequest = onDismiss,
        sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true),
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
                text = "Sleep timer",
                style = BowtieType.title,
                color = BowtieColors.text,
                modifier = Modifier.padding(bottom = 4.dp),
            )
            Text(
                text = status.remainingMs?.let { "Sleeping in ${SleepTimer.format(it)}" }
                    ?: "Stop playing after a while",
                style = BowtieType.label,
                color = BowtieColors.dim,
                modifier = Modifier.padding(bottom = 12.dp),
            )
            options.forEach { option ->
                val selected = status.option == option
                TextButton(
                    onClick = {
                        timer.start(option, programEndMs)
                        onDismiss()
                    },
                    modifier = Modifier.fillMaxWidth(),
                ) {
                    Text(
                        text = if (selected) "✓ ${option.label}" else option.label,
                        style = BowtieType.body,
                        color = if (selected) BowtieColors.amber else BowtieColors.text,
                        modifier = Modifier.fillMaxWidth(),
                    )
                }
            }
        }
    }
}

/** "Still watching? Sleeping in 0:59 — Keep watching". */
@Composable
fun SleepWarning(
    remainingMs: Long,
    onKeepWatching: () -> Unit,
    modifier: Modifier = Modifier,
) {
    Row(
        modifier = modifier
            .background(BowtieColors.bg.copy(alpha = 0.88f), RoundedCornerShape(8.dp))
            .padding(start = 16.dp, end = 4.dp, top = 4.dp, bottom = 4.dp),
        horizontalArrangement = Arrangement.spacedBy(8.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(
            text = SleepTimer.promptText(remainingMs),
            style = BowtieType.body,
            color = BowtieColors.text,
        )
        TextButton(onClick = onKeepWatching) {
            Text("Keep watching", color = BowtieColors.amber)
        }
    }
}
