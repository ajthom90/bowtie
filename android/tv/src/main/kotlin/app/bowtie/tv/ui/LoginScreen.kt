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
import app.bowtie.core.BowtieError
import app.bowtie.core.vm.AppViewModel
import app.bowtie.tv.BowtieColors
import app.bowtie.tv.BowtieDimens
import app.bowtie.tv.BowtieType
import kotlinx.coroutines.launch

private const val LOGIN_ERROR = "Wrong username or password."

@Composable
fun LoginScreen(
    appViewModel: AppViewModel,
    modifier: Modifier = Modifier,
) {
    var username by remember { mutableStateOf("") }
    var password by remember { mutableStateOf("") }
    var busy by remember { mutableStateOf(false) }
    var error by remember { mutableStateOf<String?>(null) }
    val scope = rememberCoroutineScope()

    fun submit() {
        if (busy) return
        if (username.isBlank() || password.isEmpty()) {
            error = LOGIN_ERROR
            return
        }
        busy = true
        error = null
        scope.launch {
            try {
                appViewModel.signIn(username.trim(), password)
            } catch (e: Exception) {
                error = when (e) {
                    is BowtieError.Unauthorized -> LOGIN_ERROR
                    is BowtieError.Network -> LOGIN_ERROR
                    is BowtieError.Server -> if (e.status == 401) {
                        LOGIN_ERROR
                    } else {
                        e.message.ifBlank { LOGIN_ERROR }
                    }
                    else -> LOGIN_ERROR
                }
            } finally {
                busy = false
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
                text = "Sign in",
                style = BowtieType.title,
                color = BowtieColors.text,
            )
            Spacer(Modifier.height(8.dp))
            Text(
                text = "Use your Bowtie account",
                style = BowtieType.body,
                color = BowtieColors.dim,
            )
            Spacer(Modifier.height(32.dp))

            TvTextField(
                value = username,
                onValueChange = {
                    username = it
                    error = null
                },
                label = "Username",
                enabled = !busy,
                keyboardOptions = KeyboardOptions(
                    keyboardType = KeyboardType.Text,
                    imeAction = ImeAction.Next,
                ),
            )
            Spacer(Modifier.height(12.dp))
            TvTextField(
                value = password,
                onValueChange = {
                    password = it
                    error = null
                },
                label = "Password",
                enabled = !busy,
                isPassword = true,
                keyboardOptions = KeyboardOptions(
                    keyboardType = KeyboardType.Password,
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
                    text = if (busy) "…" else "Sign in",
                    style = BowtieType.body,
                )
            }

            Spacer(Modifier.height(16.dp))

            Button(
                onClick = { appViewModel.changeServer() },
                enabled = !busy,
                colors = ButtonDefaults.colors(
                    containerColor = BowtieColors.surface,
                    contentColor = BowtieColors.dim,
                    focusedContainerColor = BowtieColors.raised,
                    focusedContentColor = BowtieColors.text,
                    pressedContainerColor = BowtieColors.raised,
                    pressedContentColor = BowtieColors.text,
                    disabledContainerColor = BowtieColors.surface,
                    disabledContentColor = BowtieColors.dim,
                ),
            ) {
                Text(
                    text = "Change server",
                    style = BowtieType.body,
                    color = BowtieColors.dim,
                )
            }
        }
    }
}
