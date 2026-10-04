package app.bowtie.ui

import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.background
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.hapticfeedback.HapticFeedbackType
import androidx.compose.ui.platform.LocalHapticFeedback
import androidx.compose.ui.semantics.CustomAccessibilityAction
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.customActions
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import app.bowtie.BowtieColors
import app.bowtie.BowtieDimens
import app.bowtie.BowtieType
import app.bowtie.core.ContinueWatching
import app.bowtie.core.Recording

internal const val CONTINUE_WATCHING_TITLE = "Continue watching"
internal const val REMOVE_FROM_CONTINUE_WATCHING = "Remove from Continue watching"

/**
 * "Continue watching": part-watched recordings, most recent first. A tap
 * resumes; a long-press offers "Remove from Continue watching". Callers show
 * it only when [items] isn't empty.
 */
@Composable
fun ContinueWatchingRow(
    items: List<Recording>,
    onResume: (Recording) -> Unit,
    onRemove: (Recording) -> Unit,
    modifier: Modifier = Modifier,
) {
    Column(modifier = modifier.padding(vertical = 10.dp)) {
        Text(
            text = CONTINUE_WATCHING_TITLE.uppercase(),
            style = BowtieType.label,
            color = BowtieColors.dim,
            modifier = Modifier.padding(horizontal = BowtieDimens.screenPadding),
        )
        Spacer(Modifier.height(8.dp))
        LazyRow(
            horizontalArrangement = Arrangement.spacedBy(8.dp),
            contentPadding = PaddingValues(horizontal = BowtieDimens.screenPadding),
        ) {
            items(items, key = { it.id }) { r ->
                ContinueWatchingCard(r, onResume = { onResume(r) }, onRemove = { onRemove(r) })
            }
        }
    }
}

@OptIn(ExperimentalFoundationApi::class)
@Composable
private fun ContinueWatchingCard(
    r: Recording,
    onResume: () -> Unit,
    onRemove: () -> Unit,
) {
    val haptics = LocalHapticFeedback.current
    var menuOpen by remember { mutableStateOf(false) }
    val secondLine = r.subtitle.ifEmpty { r.channelName }
    val left = ContinueWatching.remainingText(r)

    Box {
        Column(
            modifier = Modifier
                .width(176.dp)
                .clip(RoundedCornerShape(BowtieDimens.cornerRadius))
                .background(BowtieColors.surface)
                .combinedClickable(
                    onClickLabel = "Resume",
                    onClick = onResume,
                    onLongClickLabel = REMOVE_FROM_CONTINUE_WATCHING,
                    onLongClick = {
                        haptics.performHapticFeedback(HapticFeedbackType.LongPress)
                        menuOpen = true
                    },
                )
                .semantics(mergeDescendants = true) {
                    contentDescription = listOf(r.title, secondLine, left)
                        .filter { it.isNotEmpty() }
                        .joinToString(", ")
                    customActions = listOf(
                        CustomAccessibilityAction(REMOVE_FROM_CONTINUE_WATCHING) {
                            onRemove()
                            true
                        },
                    )
                }
                .padding(horizontal = 12.dp, vertical = 10.dp),
        ) {
            Text(
                text = r.title.ifEmpty { "Untitled" },
                style = BowtieType.body,
                color = BowtieColors.text,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            if (secondLine.isNotEmpty()) {
                Text(
                    text = secondLine,
                    style = BowtieType.label,
                    color = BowtieColors.dim,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
            }
            Spacer(Modifier.height(8.dp))
            WatchedBar(ContinueWatching.progress(r))
            Spacer(Modifier.height(6.dp))
            Text(
                text = left,
                style = BowtieType.label,
                color = BowtieColors.amber,
                maxLines = 1,
            )
        }
        DropdownMenu(expanded = menuOpen, onDismissRequest = { menuOpen = false }) {
            DropdownMenuItem(
                text = { Text("Resume") },
                onClick = {
                    menuOpen = false
                    onResume()
                },
            )
            DropdownMenuItem(
                text = { Text(REMOVE_FROM_CONTINUE_WATCHING) },
                onClick = {
                    menuOpen = false
                    onRemove()
                },
            )
        }
    }
}

/** Amber fill on a charcoal track: how much has been watched. */
@Composable
private fun WatchedBar(progress: Float) {
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
