package app.bowtie.tv.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.unit.dp
import androidx.tv.material3.Button
import androidx.tv.material3.ButtonDefaults
import androidx.tv.material3.Text
import app.bowtie.core.SleepTimer
import app.bowtie.tv.BowtieColors
import app.bowtie.tv.BowtieType
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive

/**
 * A [SleepTimer] for this player screen, ticked once a second while it is
 * shown. Survives recomposition (and zaps); gone with the screen. [onFire]
 * should stop playback the same way leaving the player does.
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

/**
 * "Sleep timer" choices for a player drawer: a heading with the time left,
 * then one button per option (D-pad focusable). [firstFocus] goes on the
 * first button when the drawer has nothing above it.
 */
@Composable
fun SleepTimerChoices(
    status: SleepTimer.Status,
    options: List<SleepTimer.Option>,
    onSelect: (SleepTimer.Option) -> Unit,
    firstFocus: FocusRequester? = null,
) {
    Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
        Text(
            text = status.remainingMs?.let { "Sleep timer · sleeping in ${SleepTimer.format(it)}" }
                ?: "Sleep timer",
            style = BowtieType.label,
            color = BowtieColors.dim,
        )
        options.forEachIndexed { index, option ->
            val selected = status.option == option
            Button(
                onClick = { onSelect(option) },
                modifier = if (index == 0 && firstFocus != null) {
                    Modifier.fillMaxWidth().focusRequester(firstFocus)
                } else {
                    Modifier.fillMaxWidth()
                },
                colors = drawerButtonColors(selected),
            ) {
                Text(
                    text = if (selected) "✓ ${option.label}" else option.label,
                    style = BowtieType.body,
                    color = if (selected) BowtieColors.amber else BowtieColors.text,
                )
            }
        }
    }
}

/**
 * "Still watching? Sleeping in 0:59 — Keep watching". The host moves focus to
 * the button ([focusRequester]) when this appears.
 */
@Composable
fun TvSleepWarning(
    remainingMs: Long,
    focusRequester: FocusRequester,
    onKeepWatching: () -> Unit,
    modifier: Modifier = Modifier,
) {
    Row(
        modifier = modifier
            .background(BowtieColors.bg.copy(alpha = 0.9f), RoundedCornerShape(12.dp))
            .padding(horizontal = 24.dp, vertical = 12.dp),
        horizontalArrangement = Arrangement.spacedBy(20.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(
            text = SleepTimer.promptText(remainingMs),
            style = BowtieType.body,
            color = BowtieColors.text,
        )
        Button(
            onClick = onKeepWatching,
            modifier = Modifier.focusRequester(focusRequester),
            colors = ButtonDefaults.colors(
                containerColor = BowtieColors.raised,
                contentColor = BowtieColors.amber,
                focusedContainerColor = BowtieColors.amber,
                focusedContentColor = BowtieColors.bg,
                pressedContainerColor = BowtieColors.amber,
                pressedContentColor = BowtieColors.bg,
                disabledContainerColor = BowtieColors.surface,
                disabledContentColor = BowtieColors.dim,
            ),
        ) {
            Text("Keep watching", style = BowtieType.body)
        }
    }
}
