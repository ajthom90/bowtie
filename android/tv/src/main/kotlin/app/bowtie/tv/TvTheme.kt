package app.bowtie.tv

import androidx.compose.runtime.Composable
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.darkColorScheme

/**
 * Design tokens — same as phone (:app Theme.kt):
 * bg #101418, surface #1A2027, raised #232B34, line #2E3843,
 * text #F2EFE8, dim #9BA5AE, amber #F0A428, signal #5DBB63, alert #E4574B.
 */
object BowtieColors {
    val bg = Color(0xFF101418)
    val surface = Color(0xFF1A2027)
    val raised = Color(0xFF232B34)
    val line = Color(0xFF2E3843)
    val text = Color(0xFFF2EFE8)
    val dim = Color(0xFF9BA5AE)
    val amber = Color(0xFFF0A428)
    val signal = Color(0xFF5DBB63)
    val alert = Color(0xFFE4574B)
}

object BowtieType {
    /** Condensed guide numbers — ~40sp / weight 700 for TV rail. */
    val channelNumber = TextStyle(
        fontFamily = FontFamily.SansSerif,
        fontWeight = FontWeight.Bold,
        fontSize = 40.sp,
        letterSpacing = 0.5.sp,
        color = BowtieColors.text,
        lineHeight = 44.sp,
    )

    val mono = TextStyle(
        fontFamily = FontFamily.Monospace,
        fontWeight = FontWeight.Normal,
        fontSize = 14.sp,
        letterSpacing = 0.sp,
        color = BowtieColors.dim,
    )

    val body = TextStyle(
        fontFamily = FontFamily.SansSerif,
        fontWeight = FontWeight.Normal,
        fontSize = 18.sp,
        color = BowtieColors.text,
    )

    val label = TextStyle(
        fontFamily = FontFamily.SansSerif,
        fontWeight = FontWeight.Medium,
        fontSize = 16.sp,
        color = BowtieColors.dim,
    )

    val title = TextStyle(
        fontFamily = FontFamily.SansSerif,
        fontWeight = FontWeight.SemiBold,
        fontSize = 28.sp,
        color = BowtieColors.text,
    )
}

object BowtieDimens {
    val cornerRadius = 12.dp
    val fieldPadding = 16.dp
    val screenPadding = 48.dp
    val rowPadding = 20.dp
    val progressHeight = 4.dp
}

private val BowtieTvColorScheme = darkColorScheme(
    primary = BowtieColors.amber,
    onPrimary = BowtieColors.bg,
    secondary = BowtieColors.signal,
    onSecondary = BowtieColors.bg,
    tertiary = BowtieColors.amber,
    onTertiary = BowtieColors.bg,
    background = BowtieColors.bg,
    onBackground = BowtieColors.text,
    surface = BowtieColors.surface,
    onSurface = BowtieColors.text,
    surfaceVariant = BowtieColors.raised,
    onSurfaceVariant = BowtieColors.dim,
    error = BowtieColors.alert,
    onError = BowtieColors.text,
    border = BowtieColors.line,
)

@Composable
fun TvTheme(content: @Composable () -> Unit) {
    MaterialTheme(
        colorScheme = BowtieTvColorScheme,
        content = content,
    )
}
