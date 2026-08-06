package app.bowtie.tv.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.unit.dp
import androidx.tv.material3.Button
import androidx.tv.material3.ButtonDefaults
import androidx.tv.material3.Text
import app.bowtie.core.vm.AppViewModel
import app.bowtie.tv.BowtieColors
import app.bowtie.tv.BowtieDimens
import app.bowtie.tv.BowtieType
import kotlinx.coroutines.launch

private const val CONNECT_ERROR =
    "Couldn't reach a Bowtie server there. Check the address and try again."

private const val URL_PLACEHOLDER = "https://tv.example.com or http://192.168.1.50:8400"

@Composable
fun ConnectScreen(
    appViewModel: AppViewModel,
    modifier: Modifier = Modifier,
) {
    var url by remember { mutableStateOf("") }
    var busy by remember { mutableStateOf(false) }
    var error by remember { mutableStateOf<String?>(null) }
    val scope = rememberCoroutineScope()

    fun submit() {
        if (busy) return
        val raw = url.trim()
        if (raw.isEmpty()) {
            error = CONNECT_ERROR
            return
        }
        busy = true
        error = null
        scope.launch {
            val ok = appViewModel.connect(raw)
            busy = false
            if (!ok) {
                error = CONNECT_ERROR
            }
        }
    }

    Column(
        modifier = modifier
            .fillMaxSize()
            .background(BowtieColors.bg)
            .padding(BowtieDimens.screenPadding),
        verticalArrangement = Arrangement.Center,
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        Column(
            modifier = Modifier
                .widthIn(max = 720.dp)
                .fillMaxWidth(),
            horizontalAlignment = Alignment.CenterHorizontally,
        ) {
            Text(
                text = "Bowtie",
                style = BowtieType.channelNumber,
                color = BowtieColors.amber,
            )
            Spacer(Modifier.height(8.dp))
            Text(
                text = "Connect to your server",
                style = BowtieType.body,
                color = BowtieColors.dim,
            )
            Spacer(Modifier.height(32.dp))

            TvTextField(
                value = url,
                onValueChange = {
                    url = it
                    error = null
                },
                label = "Server address",
                enabled = !busy,
                placeholder = URL_PLACEHOLDER,
                keyboardOptions = KeyboardOptions(
                    keyboardType = KeyboardType.Uri,
                    imeAction = ImeAction.Go,
                ),
                keyboardActions = KeyboardActions(onGo = { submit() }),
            )

            if (error != null) {
                Spacer(Modifier.height(12.dp))
                Text(
                    text = error!!,
                    style = BowtieType.body,
                    color = BowtieColors.alert,
                )
            }

            Spacer(Modifier.height(24.dp))

            Button(
                onClick = { submit() },
                enabled = !busy,
                modifier = Modifier.fillMaxWidth(),
                colors = ButtonDefaults.colors(
                    containerColor = BowtieColors.amber,
                    contentColor = BowtieColors.bg,
                    focusedContainerColor = BowtieColors.amber,
                    focusedContentColor = BowtieColors.bg,
                    pressedContainerColor = BowtieColors.amber,
                    pressedContentColor = BowtieColors.bg,
                    disabledContainerColor = BowtieColors.raised,
                    disabledContentColor = BowtieColors.dim,
                ),
            ) {
                Text(
                    text = if (busy) "…" else "Validate",
                    style = BowtieType.body,
                )
            }
        }
    }
}
