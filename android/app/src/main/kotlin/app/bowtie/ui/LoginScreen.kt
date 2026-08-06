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
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
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
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.unit.dp
import app.bowtie.BowtieColors
import app.bowtie.core.vm.AppViewModel
import app.bowtie.BowtieDimens
import app.bowtie.BowtieType
import app.bowtie.core.BowtieError
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
                    is BowtieError.Server -> if (e.status == 401) LOGIN_ERROR else (e.message.ifBlank { LOGIN_ERROR })
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

        OutlinedTextField(
            value = username,
            onValueChange = {
                username = it
                error = null
            },
            modifier = Modifier.fillMaxWidth(),
            singleLine = true,
            enabled = !busy,
            label = { Text("Username") },
            keyboardOptions = KeyboardOptions(
                keyboardType = KeyboardType.Text,
                imeAction = ImeAction.Next,
            ),
            shape = RoundedCornerShape(BowtieDimens.cornerRadius),
            colors = bowtieTextFieldColors(),
        )
        Spacer(Modifier.height(12.dp))
        OutlinedTextField(
            value = password,
            onValueChange = {
                password = it
                error = null
            },
            modifier = Modifier.fillMaxWidth(),
            singleLine = true,
            enabled = !busy,
            label = { Text("Password") },
            visualTransformation = PasswordVisualTransformation(),
            keyboardOptions = KeyboardOptions(
                keyboardType = KeyboardType.Password,
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
                Text("Sign in")
            }
        }

        Spacer(Modifier.height(12.dp))
        TextButton(
            onClick = { appViewModel.changeServer() },
            enabled = !busy,
        ) {
            Text(
                text = "Change server",
                color = BowtieColors.dim,
            )
        }
    }
}
