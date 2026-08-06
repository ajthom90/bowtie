package app.bowtie.tv.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.dp
import androidx.tv.material3.Text
import app.bowtie.core.SessionInfoMeta
import app.bowtie.tv.BowtieColors
import app.bowtie.tv.BowtieDimens
import app.bowtie.tv.BowtieType

/**
 * Stats-for-nerds readout (mono). Session meta from create-session;
 * live bitrate + dropped frames from Media3 via [app.bowtie.core.player.PlayerEngine].
 */
@Composable
fun TvStatsOverlay(
    sessionMeta: SessionInfoMeta?,
    bitrateBps: Int?,
    droppedFrames: Long?,
    modifier: Modifier = Modifier,
) {
    Column(
        modifier = modifier
            .clip(RoundedCornerShape(BowtieDimens.cornerRadius))
            .background(BowtieColors.surface.copy(alpha = 0.92f))
            .padding(horizontal = 16.dp, vertical = 12.dp)
            .semantics { contentDescription = "Stream statistics" },
        verticalArrangement = Arrangement.spacedBy(6.dp),
    ) {
        StatsRow(key = "codec", value = field(sessionMeta?.videoCodec))
        StatsRow(key = "profile", value = field(sessionMeta?.profile))
        StatsRow(key = "backend", value = field(sessionMeta?.backend))
        StatsRow(key = "bitrate", value = formatBitrate(bitrateBps))
        StatsRow(
            key = "dropped",
            value = droppedFrames?.toString() ?: "—",
        )
    }
}

@Composable
private fun StatsRow(key: String, value: String) {
    Row(
        modifier = Modifier.fillMaxWidth(),
        horizontalArrangement = Arrangement.SpaceBetween,
    ) {
        Text(
            text = key,
            style = BowtieType.mono,
            color = BowtieColors.dim,
            modifier = Modifier.padding(end = 20.dp),
        )
        Text(
            text = value,
            style = BowtieType.mono,
            color = BowtieColors.amber,
        )
    }
}

private fun field(value: String?): String =
    if (value.isNullOrBlank()) "—" else value

private fun formatBitrate(bitrateBps: Int?): String {
    if (bitrateBps == null || bitrateBps <= 0) return "—"
    return when {
        bitrateBps >= 1_000_000 ->
            String.format("%.1f Mbps", bitrateBps / 1_000_000.0)
        bitrateBps >= 1_000 ->
            String.format("%.0f kbps", bitrateBps / 1_000.0)
        else -> "$bitrateBps bps"
    }
}
