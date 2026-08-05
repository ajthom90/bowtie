package app.bowtie.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.unit.dp
import app.bowtie.BowtieColors
import app.bowtie.BowtieType
import app.bowtie.PlayerViewModel
import app.bowtie.core.Channel

/**
 * Task 6 stub: black screen + channel name + back → [PlayerViewModel.stop].
 * Real Media3 player is Task 7.
 */
@Composable
fun PlayerScreen(
    channel: Channel,
    playerViewModel: PlayerViewModel,
    onBack: () -> Unit,
    modifier: Modifier = Modifier,
) {
    Box(
        modifier = modifier
            .fillMaxSize()
            .background(Color.Black),
    ) {
        Column(
            modifier = Modifier
                .align(Alignment.TopStart)
                .padding(16.dp),
        ) {
            TextButton(
                onClick = {
                    playerViewModel.stop()
                    onBack()
                },
            ) {
                Text("← Back", color = BowtieColors.amber)
            }
        }

        Column(
            modifier = Modifier.align(Alignment.Center),
            horizontalAlignment = Alignment.CenterHorizontally,
        ) {
            Text(
                text = channel.guideNumber,
                style = BowtieType.channelNumber,
                color = BowtieColors.amber,
            )
            Spacer(Modifier.height(8.dp))
            Text(
                text = channel.name,
                style = BowtieType.title,
                color = BowtieColors.text,
            )
            Spacer(Modifier.height(12.dp))
            Text(
                text = "Player (coming soon)",
                style = BowtieType.mono,
                color = BowtieColors.dim,
            )
        }
    }
}
