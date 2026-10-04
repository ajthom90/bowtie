package app.bowtie.tv.ui

import android.graphics.BitmapFactory
import android.os.Build
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.FilterQuality
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.tv.material3.Button
import androidx.tv.material3.Text
import app.bowtie.core.vm.DeviceSignInViewModel
import app.bowtie.core.vm.DeviceSignInViewModel.State
import app.bowtie.tv.BowtieColors
import app.bowtie.tv.BowtieType

/** This TV's name as the phone approving the sign-in sees it. */
internal fun tvDeviceName(): String =
    DeviceSignInViewModel.deviceName(Build.MANUFACTURER.orEmpty(), Build.MODEL.orEmpty())

/**
 * "Sign in with your phone": the QR code and the typed code, kept fresh by
 * [viewModel]. Always offers a way back to the password form, so DPAD focus
 * has somewhere to land whatever the state.
 */
@Composable
internal fun PhoneSignInPanel(
    viewModel: DeviceSignInViewModel,
    onUsePassword: () -> Unit,
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val png by viewModel.qrPng.collectAsStateWithLifecycle()
    val primaryFocus = remember { FocusRequester() }
    val needsNewCode = state is State.Expired || state is State.Failed

    LaunchedEffect(needsNewCode) { runCatching { primaryFocus.requestFocus() } }

    Row(
        horizontalArrangement = Arrangement.spacedBy(48.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        QrBox(png = png, dimmed = state !is State.Waiting)
        Column(modifier = Modifier.width(440.dp)) {
            Text("Sign in with your phone", style = BowtieType.title, color = BowtieColors.text)
            Spacer(Modifier.height(12.dp))
            when (val s = state) {
                is State.Waiting -> {
                    Text(
                        text = "Scan the code with your phone's camera.",
                        style = BowtieType.body,
                        color = BowtieColors.dim,
                    )
                    Spacer(Modifier.height(16.dp))
                    Text(
                        text = "Or go to ${s.link} and enter",
                        style = BowtieType.body,
                        color = BowtieColors.dim,
                    )
                    Spacer(Modifier.height(8.dp))
                    Text(
                        text = s.userCode,
                        style = BowtieType.mono.copy(fontSize = 44.sp, letterSpacing = 4.sp),
                        color = BowtieColors.amber,
                    )
                    Spacer(Modifier.height(16.dp))
                    Text(
                        text = "Waiting for your phone…",
                        style = BowtieType.label,
                        color = BowtieColors.dim,
                    )
                }
                is State.Expired -> Text(
                    text = "That code expired.",
                    style = BowtieType.body,
                    color = BowtieColors.dim,
                )
                is State.Failed -> Text(text = s.message, style = BowtieType.body, color = BowtieColors.alert)
                is State.SignedIn -> Text(text = "Signed in.", style = BowtieType.body, color = BowtieColors.signal)
                State.Idle, State.Starting -> Text(
                    text = "Getting a code…",
                    style = BowtieType.body,
                    color = BowtieColors.dim,
                )
            }
            Spacer(Modifier.height(24.dp))
            if (needsNewCode) {
                Button(
                    onClick = { viewModel.start() },
                    colors = tvButtonColors(selected = true),
                    modifier = Modifier.focusRequester(primaryFocus),
                ) {
                    Text("Get a new code", style = BowtieType.body, color = BowtieColors.bg)
                }
                Spacer(Modifier.height(12.dp))
            }
            Button(
                onClick = onUsePassword,
                colors = tvButtonColors(),
                modifier = if (needsNewCode) Modifier else Modifier.focusRequester(primaryFocus),
            ) {
                Text("Use a password instead", style = BowtieType.body, color = BowtieColors.text)
            }
        }
    }
}

/** The QR on white (scanners want the quiet zone), crisp-scaled; a blank square until it loads. */
@Composable
private fun QrBox(png: ByteArray?, dimmed: Boolean) {
    val bitmap = remember(png) {
        png?.let { runCatching { BitmapFactory.decodeByteArray(it, 0, it.size) }.getOrNull() }?.asImageBitmap()
    }
    Box(
        modifier = Modifier
            .size(320.dp)
            .background(if (bitmap != null && !dimmed) Color.White else BowtieColors.surface, RoundedCornerShape(12.dp))
            .padding(16.dp),
        contentAlignment = Alignment.Center,
    ) {
        if (bitmap != null && !dimmed) {
            Image(
                bitmap = bitmap,
                contentDescription = "QR code that opens the sign-in page on your phone",
                filterQuality = FilterQuality.None,
                modifier = Modifier.size(288.dp),
            )
        }
    }
}
