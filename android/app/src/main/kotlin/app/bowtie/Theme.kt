package app.bowtie

import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Typography
import androidx.compose.material3.darkColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.sp

/** Design tokens — bg #101418, surface #1A2027, raised #232B34, line #2E3843,
 *  text #F2EFE8, dim #9BA5AE, amber #F0A428, signal #5DBB63, alert #E4574B. */
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

/**
 * Typography tokens.
 * Roboto / Roboto Condensed (channel numbers) / Roboto Mono (readouts).
 * System defaults map to Roboto on stock Android; Condensed/Mono fall back
 * until font resources are added.
 */
object BowtieType {
    val channelNumber = TextStyle(
        fontFamily = FontFamily.SansSerif,
        fontWeight = FontWeight.Bold,
        fontSize = 20.sp,
        letterSpacing = 0.5.sp,
        color = BowtieColors.text,
    )

    val mono = TextStyle(
        fontFamily = FontFamily.Monospace,
        fontWeight = FontWeight.Normal,
        fontSize = 13.sp,
        letterSpacing = 0.sp,
        color = BowtieColors.dim,
    )
}

private val BowtieColorScheme = darkColorScheme(
    primary = BowtieColors.amber,
    onPrimary = BowtieColors.bg,
    secondary = BowtieColors.signal,
    onSecondary = BowtieColors.bg,
    background = BowtieColors.bg,
    onBackground = BowtieColors.text,
    surface = BowtieColors.surface,
    onSurface = BowtieColors.text,
    error = BowtieColors.alert,
    onError = BowtieColors.text,
    outline = BowtieColors.line,
)

@Composable
fun BowtieTheme(content: @Composable () -> Unit) {
    MaterialTheme(
        colorScheme = BowtieColorScheme,
        typography = Typography(
            bodyLarge = TextStyle(
                fontFamily = FontFamily.SansSerif,
                fontWeight = FontWeight.Normal,
                fontSize = 16.sp,
                color = BowtieColors.text,
            ),
            titleLarge = TextStyle(
                fontFamily = FontFamily.SansSerif,
                fontWeight = FontWeight.SemiBold,
                fontSize = 22.sp,
                color = BowtieColors.text,
            ),
            labelMedium = BowtieType.mono,
        ),
        content = content,
    )
}
