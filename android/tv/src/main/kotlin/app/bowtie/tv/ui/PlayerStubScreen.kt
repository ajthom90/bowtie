package app.bowtie.tv.ui

import androidx.activity.compose.BackHandler
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.tv.material3.Text
import app.bowtie.core.Channel
import app.bowtie.core.vm.PlayerViewModel
import app.bowtie.tv.BowtieColors
import app.bowtie.tv.BowtieDimens
import app.bowtie.tv.BowtieType

/**
 * Task 3 player route stub: black screen + channel name.
 * BACK stops the session and returns to the rail (real player lands in Task 4).
 */
@Composable
fun PlayerStubScreen(
    channel: Channel,
    playerViewModel: PlayerViewModel,
    onBack: () -> Unit,
    modifier: Modifier = Modifier,
) {
    BackHandler {
        playerViewModel.stop()
        onBack()
    }

    Box(
        modifier = modifier
            .fillMaxSize()
            .background(Color.Black)
            .padding(BowtieDimens.screenPadding),
        contentAlignment = Alignment.Center,
    ) {
        Text(
            text = channel.name,
            style = BowtieType.title,
            color = BowtieColors.text,
        )
    }
}
