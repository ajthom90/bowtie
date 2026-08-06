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
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import androidx.tv.material3.Button
import androidx.tv.material3.ButtonDefaults
import androidx.tv.material3.Text
import app.bowtie.core.User
import app.bowtie.core.vm.AppViewModel
import app.bowtie.tv.BowtieColors
import app.bowtie.tv.BowtieDimens
import app.bowtie.tv.BowtieType
import kotlinx.coroutines.launch

private const val PASSWORD_CHANGED = "Password changed."

@Composable
fun SettingsScreen(
    appViewModel: AppViewModel,
    user: User,
    onBack: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val client = appViewModel.client
    val serverAddress = client?.server?.toString()?.trimEnd('/') ?: "—"

    var currentPassword by remember { mutableStateOf("") }
    var newPassword by remember { mutableStateOf("") }
    var confirmPassword by remember { mutableStateOf("") }
    var passwordMessage by remember { mutableStateOf<String?>(null) }
    var passwordError by remember { mutableStateOf<String?>(null) }
    var busy by remember { mutableStateOf(false) }
    val scope = rememberCoroutineScope()

    Column(
        modifier = modifier
            .fillMaxSize()
            .background(BowtieColors.bg)
            .verticalScroll(rememberScrollState())
            .padding(BowtieDimens.screenPadding),
        verticalArrangement = Arrangement.Top,
    ) {
        Column(
            modifier = Modifier
                .widthIn(max = 720.dp)
                .fillMaxWidth(),
        ) {
            Button(
                onClick = onBack,
                colors = secondaryButtonColors(),
            ) {
                Text(
                    text = "← Back",
                    style = BowtieType.body,
                    color = BowtieColors.amber,
                )
            }
            Spacer(Modifier.height(16.dp))
            Text(
                text = "Settings",
                style = BowtieType.title,
                color = BowtieColors.text,
            )
            Spacer(Modifier.height(24.dp))

            // ── Server ──────────────────────────────────────────────────────────
            Text(
                text = "Server",
                style = BowtieType.label,
                color = BowtieColors.dim,
            )
            Spacer(Modifier.height(8.dp))
            Text(
                text = serverAddress,
                style = BowtieType.mono.copy(color = BowtieColors.text),
            )
            Spacer(Modifier.height(12.dp))
            Button(
                onClick = { appViewModel.changeServer() },
                colors = secondaryButtonColors(),
            ) {
                Text(
                    text = "Change server",
                    style = BowtieType.body,
                    color = BowtieColors.text,
                )
            }

            Spacer(Modifier.height(32.dp))

            // ── Account ─────────────────────────────────────────────────────────
            Text(
                text = "Account",
                style = BowtieType.label,
                color = BowtieColors.dim,
            )
            Spacer(Modifier.height(8.dp))
            Text(
                text = user.username,
                style = BowtieType.body,
                color = BowtieColors.text,
            )

            Spacer(Modifier.height(20.dp))
            Text(
                text = "Change password",
                style = BowtieType.label,
                color = BowtieColors.dim,
            )
            Spacer(Modifier.height(8.dp))
            TvTextField(
                value = currentPassword,
                onValueChange = {
                    currentPassword = it
                    passwordError = null
                    passwordMessage = null
                },
                label = "Current password",
                enabled = !busy,
                isPassword = true,
            )
            Spacer(Modifier.height(8.dp))
            TvTextField(
                value = newPassword,
                onValueChange = {
                    newPassword = it
                    passwordError = null
                    passwordMessage = null
                },
                label = "New password",
                enabled = !busy,
                isPassword = true,
            )
            Spacer(Modifier.height(8.dp))
            TvTextField(
                value = confirmPassword,
                onValueChange = {
                    confirmPassword = it
                    passwordError = null
                    passwordMessage = null
                },
                label = "Confirm new password",
                enabled = !busy,
                isPassword = true,
            )

            if (passwordMessage != null) {
                Spacer(Modifier.height(8.dp))
                Text(
                    text = passwordMessage!!,
                    style = BowtieType.body,
                    color = BowtieColors.signal,
                )
            }
            if (passwordError != null) {
                Spacer(Modifier.height(8.dp))
                Text(
                    text = passwordError!!,
                    style = BowtieType.body,
                    color = BowtieColors.alert,
                )
            }

            Spacer(Modifier.height(12.dp))
            Button(
                onClick = {
                    if (busy || client == null) return@Button
                    if (newPassword.isEmpty()) {
                        passwordError = "Enter a new password."
                        return@Button
                    }
                    if (newPassword != confirmPassword) {
                        passwordError = "New passwords don't match."
                        return@Button
                    }
                    busy = true
                    passwordError = null
                    passwordMessage = null
                    scope.launch {
                        try {
                            client.changePassword(currentPassword, newPassword)
                            passwordMessage = PASSWORD_CHANGED
                            currentPassword = ""
                            newPassword = ""
                            confirmPassword = ""
                        } catch (e: Exception) {
                            passwordError = e.message?.takeIf { it.isNotBlank() }
                                ?: "Couldn't change password."
                        } finally {
                            busy = false
                        }
                    }
                },
                enabled = !busy,
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
                    text = "Update password",
                    style = BowtieType.body,
                )
            }

            Spacer(Modifier.height(40.dp))

            Button(
                onClick = {
                    scope.launch { appViewModel.signOut() }
                },
                enabled = !busy,
                modifier = Modifier.fillMaxWidth(),
                colors = ButtonDefaults.colors(
                    containerColor = BowtieColors.raised,
                    contentColor = BowtieColors.alert,
                    focusedContainerColor = BowtieColors.line,
                    focusedContentColor = BowtieColors.alert,
                    pressedContainerColor = BowtieColors.line,
                    pressedContentColor = BowtieColors.alert,
                    disabledContainerColor = BowtieColors.raised,
                    disabledContentColor = BowtieColors.dim,
                ),
            ) {
                Text(
                    text = "Sign out",
                    style = BowtieType.body,
                    color = BowtieColors.alert,
                )
            }
        }
    }
}

@Composable
private fun secondaryButtonColors() = ButtonDefaults.colors(
    containerColor = BowtieColors.surface,
    contentColor = BowtieColors.text,
    focusedContainerColor = BowtieColors.raised,
    focusedContentColor = BowtieColors.text,
    pressedContainerColor = BowtieColors.raised,
    pressedContentColor = BowtieColors.text,
    disabledContainerColor = BowtieColors.surface,
    disabledContentColor = BowtieColors.dim,
)
