package app.bowtie.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.dp
import app.bowtie.BowtieColors
import app.bowtie.BowtieType

/** How long "Skipped ad" stays up after an automatic skip. */
const val SKIPPED_AD_TOAST_MS = 1_500L

/** "Skip ad" over the recording player while inside a commercial break. */
@Composable
fun SkipAdButton(onSkip: () -> Unit, modifier: Modifier = Modifier) {
    Button(
        onClick = onSkip,
        modifier = modifier,
        shape = RoundedCornerShape(8.dp),
        colors = ButtonDefaults.buttonColors(
            containerColor = BowtieColors.amber,
            contentColor = BowtieColors.bg,
        ),
    ) {
        Text("Skip ad  ▶▶", style = BowtieType.label, color = BowtieColors.bg)
    }
}

/** "Skipped ad", shown briefly after an automatic skip. */
@Composable
fun SkippedAdToast(modifier: Modifier = Modifier) {
    Text(
        text = "Skipped ad",
        style = BowtieType.label,
        color = BowtieColors.text,
        modifier = modifier
            .semantics { liveRegion = LiveRegionMode.Polite }
            .background(BowtieColors.bg.copy(alpha = 0.88f), RoundedCornerShape(8.dp))
            .padding(horizontal = 16.dp, vertical = 10.dp),
    )
}
