package app.bowtie.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.unit.dp
import app.bowtie.AppViewModel
import app.bowtie.BowtieColors
import app.bowtie.BowtieDimens
import app.bowtie.BowtieType
import app.bowtie.core.User
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
        TextButton(onClick = onBack) {
            Text("← Back", color = BowtieColors.amber)
        }
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
        OutlinedButton(
            onClick = { appViewModel.changeServer() },
            shape = RoundedCornerShape(BowtieDimens.cornerRadius),
        ) {
            Text("Change server", color = BowtieColors.text)
        }

        Spacer(Modifier.height(24.dp))
        HorizontalDivider(color = BowtieColors.line)
        Spacer(Modifier.height(24.dp))

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
        OutlinedTextField(
            value = currentPassword,
            onValueChange = {
                currentPassword = it
                passwordError = null
                passwordMessage = null
            },
            modifier = Modifier.fillMaxWidth(),
            singleLine = true,
            enabled = !busy,
            label = { Text("Current password") },
            visualTransformation = PasswordVisualTransformation(),
            shape = RoundedCornerShape(BowtieDimens.cornerRadius),
            colors = bowtieTextFieldColors(),
        )
        Spacer(Modifier.height(8.dp))
        OutlinedTextField(
            value = newPassword,
            onValueChange = {
                newPassword = it
                passwordError = null
                passwordMessage = null
            },
            modifier = Modifier.fillMaxWidth(),
            singleLine = true,
            enabled = !busy,
            label = { Text("New password") },
            visualTransformation = PasswordVisualTransformation(),
            shape = RoundedCornerShape(BowtieDimens.cornerRadius),
            colors = bowtieTextFieldColors(),
        )
        Spacer(Modifier.height(8.dp))
        OutlinedTextField(
            value = confirmPassword,
            onValueChange = {
                confirmPassword = it
                passwordError = null
                passwordMessage = null
            },
            modifier = Modifier.fillMaxWidth(),
            singleLine = true,
            enabled = !busy,
            label = { Text("Confirm new password") },
            visualTransformation = PasswordVisualTransformation(),
            shape = RoundedCornerShape(BowtieDimens.cornerRadius),
            colors = bowtieTextFieldColors(),
        )

        if (passwordMessage != null) {
            Spacer(Modifier.height(8.dp))
            Text(text = passwordMessage!!, color = BowtieColors.signal, style = BowtieType.body)
        }
        if (passwordError != null) {
            Spacer(Modifier.height(8.dp))
            Text(text = passwordError!!, color = BowtieColors.alert, style = BowtieType.body)
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
            shape = RoundedCornerShape(BowtieDimens.cornerRadius),
            colors = ButtonDefaults.buttonColors(
                containerColor = BowtieColors.amber,
                contentColor = BowtieColors.bg,
            ),
        ) {
            Text("Update password")
        }

        Spacer(Modifier.height(32.dp))
        HorizontalDivider(color = BowtieColors.line)
        Spacer(Modifier.height(24.dp))

        Button(
            onClick = {
                scope.launch { appViewModel.signOut() }
            },
            enabled = !busy,
            modifier = Modifier.fillMaxWidth(),
            shape = RoundedCornerShape(BowtieDimens.cornerRadius),
            colors = ButtonDefaults.buttonColors(
                containerColor = BowtieColors.raised,
                contentColor = BowtieColors.alert,
            ),
        ) {
            Text("Sign out")
        }
    }
}
