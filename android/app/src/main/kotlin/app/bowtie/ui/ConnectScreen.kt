package app.bowtie.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.OutlinedTextFieldDefaults
import androidx.compose.material3.Text
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
import app.bowtie.BowtieColors
import app.bowtie.core.vm.AppViewModel
import app.bowtie.BowtieDimens
import app.bowtie.BowtieType
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

        OutlinedTextField(
            value = url,
            onValueChange = {
                url = it
                error = null
            },
            modifier = Modifier.fillMaxWidth(),
            singleLine = true,
            enabled = !busy,
            label = { Text("Server address") },
            placeholder = {
                Text(
                    text = URL_PLACEHOLDER,
                    style = BowtieType.body.copy(color = BowtieColors.dim),
                )
            },
            keyboardOptions = KeyboardOptions(
                keyboardType = KeyboardType.Uri,
                imeAction = ImeAction.Go,
            ),
            keyboardActions = KeyboardActions(onGo = { submit() }),
            shape = RoundedCornerShape(BowtieDimens.cornerRadius),
            colors = bowtieTextFieldColors(),
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
            shape = RoundedCornerShape(BowtieDimens.cornerRadius),
            colors = ButtonDefaults.buttonColors(
                containerColor = BowtieColors.amber,
                contentColor = BowtieColors.bg,
                disabledContainerColor = BowtieColors.raised,
                disabledContentColor = BowtieColors.dim,
            ),
        ) {
            if (busy) {
                CircularProgressIndicator(
                    modifier = Modifier.height(20.dp),
                    color = BowtieColors.bg,
                    strokeWidth = 2.dp,
                )
            } else {
                Text("Validate")
            }
        }
    }
}

@Composable
internal fun bowtieTextFieldColors() = OutlinedTextFieldDefaults.colors(
    focusedTextColor = BowtieColors.text,
    unfocusedTextColor = BowtieColors.text,
    disabledTextColor = BowtieColors.dim,
    focusedContainerColor = BowtieColors.surface,
    unfocusedContainerColor = BowtieColors.surface,
    disabledContainerColor = BowtieColors.surface,
    focusedBorderColor = BowtieColors.amber,
    unfocusedBorderColor = BowtieColors.line,
    disabledBorderColor = BowtieColors.line,
    focusedLabelColor = BowtieColors.amber,
    unfocusedLabelColor = BowtieColors.dim,
    cursorColor = BowtieColors.amber,
    focusedPlaceholderColor = BowtieColors.dim,
    unfocusedPlaceholderColor = BowtieColors.dim,
)
