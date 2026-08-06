package app.bowtie.tv

import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.viewModels
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.ui.Modifier
import app.bowtie.core.vm.AppViewModel

/**
 * Fire TV entry point — Compose-for-TV root over shared ViewModels.
 */
class MainActivity : ComponentActivity() {

    private val appViewModel: AppViewModel by viewModels {
        AppViewModelFactory(application)
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContent {
            TvTheme {
                TvRoot(
                    appViewModel = appViewModel,
                    modifier = Modifier.fillMaxSize(),
                )
            }
        }
    }
}
