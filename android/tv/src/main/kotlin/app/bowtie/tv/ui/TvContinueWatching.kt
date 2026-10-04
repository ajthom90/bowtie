package app.bowtie.tv.ui

import androidx.compose.foundation.background
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
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.remember
import androidx.compose.runtime.withFrameNanos
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.input.key.onPreviewKeyEvent
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.tv.material3.ClickableSurfaceDefaults
import androidx.tv.material3.Surface
import androidx.tv.material3.Text
import app.bowtie.core.ContinueWatching
import app.bowtie.core.Recording
import app.bowtie.tv.BowtieColors
import app.bowtie.tv.BowtieDimens
import app.bowtie.tv.BowtieType

internal const val CONTINUE_WATCHING_TITLE = "Continue watching"
internal const val REMOVE_FROM_CONTINUE_WATCHING = "Remove from Continue watching"

/** Where focus goes when a Continue watching card is removed. */
internal object ContinueFocus {
    /** The next card, else the previous one; null when none are left (the row goes). */
    fun afterRemoval(ids: List<Long>, removedId: Long): Long? {
        val i = ids.indexOf(removedId)
        if (i < 0) return null
        return ids.getOrNull(i + 1) ?: ids.getOrNull(i - 1)
    }
}

/**
 * "Continue watching" cards at the top of the home screen. OK resumes; hold
 * OK for options ([onLongPress]); ☰ removes a card ([onMenu]). DPAD up/down
 * moves to the rows above and below it. [refocusId] asks for that card to take
 * focus (after its neighbor was removed); [onRefocused] clears the request.
 */
@Composable
internal fun ContinueRail(
    items: List<Recording>,
    refocusId: Long?,
    onRefocused: () -> Unit,
    onResume: (Recording) -> Unit,
    onLongPress: (Recording) -> Unit,
    onMenu: (Recording) -> Unit,
) {
    Column(modifier = Modifier.padding(top = 12.dp)) {
        Text(
            text = CONTINUE_WATCHING_TITLE.uppercase(),
            style = BowtieType.label,
            color = BowtieColors.dim,
            modifier = Modifier.padding(horizontal = BowtieDimens.screenPadding),
        )
        Spacer(Modifier.height(8.dp))
        LazyRow(
            horizontalArrangement = Arrangement.spacedBy(12.dp),
            contentPadding = PaddingValues(horizontal = BowtieDimens.screenPadding, vertical = 4.dp),
        ) {
            items(items, key = { it.id }) { r ->
                val focusRequester = remember { FocusRequester() }
                LaunchedEffect(refocusId == r.id) {
                    if (refocusId != r.id) return@LaunchedEffect
                    withFrameNanos { }
                    runCatching { focusRequester.requestFocus() }
                    onRefocused()
                }
                ContinueCard(
                    r = r,
                    onClick = { onResume(r) },
                    onLongClick = { onLongPress(r) },
                    onMenu = { onMenu(r) },
                    modifier = Modifier.focusRequester(focusRequester),
                )
            }
        }
    }
}

@Composable
private fun ContinueCard(
    r: Recording,
    onClick: () -> Unit,
    onLongClick: () -> Unit,
    onMenu: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val secondLine = r.subtitle.ifEmpty { r.channelName }
    val left = ContinueWatching.remainingText(r)
    Surface(
        onClick = onClick,
        onLongClick = onLongClick,
        modifier = modifier
            .width(280.dp)
            .onPreviewKeyEvent { event ->
                val native = event.nativeKeyEvent
                when (RailKeys.onKey(native.keyCode, native.action, native.repeatCount)) {
                    RailKeys.Outcome.MenuPress -> {
                        onMenu()
                        true
                    }
                    RailKeys.Outcome.Consume -> true
                    RailKeys.Outcome.PassThrough -> false
                }
            }
            .semantics {
                contentDescription = listOf(r.title, secondLine, left)
                    .filter { it.isNotEmpty() }
                    .joinToString(", ")
            },
        colors = ClickableSurfaceDefaults.colors(
            containerColor = BowtieColors.surface,
            contentColor = BowtieColors.text,
            focusedContainerColor = BowtieColors.raised,
            focusedContentColor = BowtieColors.text,
            pressedContainerColor = BowtieColors.raised,
            pressedContentColor = BowtieColors.text,
            disabledContainerColor = BowtieColors.surface,
            disabledContentColor = BowtieColors.dim,
        ),
        shape = ClickableSurfaceDefaults.shape(
            shape = RoundedCornerShape(BowtieDimens.cornerRadius),
        ),
    ) {
        Column(modifier = Modifier.padding(horizontal = 16.dp, vertical = 12.dp)) {
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
            .background(BowtieColors.line),
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
