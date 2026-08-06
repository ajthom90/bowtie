package app.bowtie.tv

import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.tv.material3.Text

/**
 * Fire TV entry point — Task 2 scaffold hello screen on design tokens.
 * Full navigation lands in Task 3.
 */
class MainActivity : ComponentActivity() {

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContent {
            TvTheme {
                HelloScreen()
            }
        }
    }
}

@Composable
private fun HelloScreen() {
    Box(
        modifier = Modifier
            .fillMaxSize()
            .background(BowtieColors.bg)
            .padding(BowtieDimens.screenPadding),
        contentAlignment = Alignment.Center,
    ) {
        Text(
            text = "Bowtie",
            style = BowtieType.title,
            color = BowtieColors.amber,
        )
    }
}
