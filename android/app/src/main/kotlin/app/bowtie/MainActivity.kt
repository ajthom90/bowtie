package app.bowtie

import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.tooling.preview.Preview
import androidx.compose.ui.unit.sp

class MainActivity : ComponentActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()
        setContent {
            BowtieTheme {
                HelloScreen()
            }
        }
    }
}

@Composable
fun HelloScreen() {
    Box(
        modifier = Modifier
            .fillMaxSize()
            .background(BowtieColors.bg),
        contentAlignment = Alignment.Center,
    ) {
        Text(
            text = "Bowtie",
            color = BowtieColors.text,
            fontSize = 28.sp,
        )
    }
}

@Preview
@Composable
private fun HelloScreenPreview() {
    BowtieTheme {
        HelloScreen()
    }
}
