package app.bowtie.tv.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import androidx.tv.material3.Button
import androidx.tv.material3.Text
import app.bowtie.tv.BowtieColors
import app.bowtie.tv.BowtieType

/**
 * "Skip ads automatically" (per device) for the recording player's drawer and
 * Settings: a heading and one D-pad button that toggles it.
 */
@Composable
fun AutoSkipAdsButton(on: Boolean, onToggle: () -> Unit, modifier: Modifier = Modifier) {
    Column(modifier = modifier, verticalArrangement = Arrangement.spacedBy(8.dp)) {
        Text(
            text = "Commercials",
            style = BowtieType.label,
            color = BowtieColors.dim,
        )
        Button(
            onClick = onToggle,
            modifier = Modifier.fillMaxWidth(),
            colors = drawerButtonColors(on),
        ) {
            Text(
                text = if (on) "Skip ads automatically: On" else "Skip ads automatically: Off",
                style = BowtieType.body,
                color = if (on) BowtieColors.amber else BowtieColors.text,
            )
        }
    }
}
