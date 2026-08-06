package app.bowtie.tv.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.interaction.collectIsFocusedAsState
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.text.input.VisualTransformation
import androidx.compose.ui.unit.dp
import androidx.tv.material3.Text
import app.bowtie.tv.BowtieColors
import app.bowtie.tv.BowtieDimens
import app.bowtie.tv.BowtieType

/**
 * Focus-visible text field for 10-foot UI (TV keyboard friendly).
 * Amber border when focused; surface fill; single-line.
 */
@Composable
fun TvTextField(
    value: String,
    onValueChange: (String) -> Unit,
    label: String,
    modifier: Modifier = Modifier,
    enabled: Boolean = true,
    isPassword: Boolean = false,
    placeholder: String? = null,
    keyboardOptions: KeyboardOptions = KeyboardOptions.Default,
    keyboardActions: KeyboardActions = KeyboardActions.Default,
) {
    val interactionSource = remember { MutableInteractionSource() }
    val isFocused by interactionSource.collectIsFocusedAsState()
    val shape = RoundedCornerShape(BowtieDimens.cornerRadius)
    val borderColor = when {
        !enabled -> BowtieColors.line
        isFocused -> BowtieColors.amber
        else -> BowtieColors.line
    }
    val labelColor = when {
        !enabled -> BowtieColors.dim
        isFocused -> BowtieColors.amber
        else -> BowtieColors.dim
    }

    Column(modifier = modifier.fillMaxWidth()) {
        Text(
            text = label,
            style = BowtieType.label,
            color = labelColor,
        )
        Spacer(Modifier.height(8.dp))
        BasicTextField(
            value = value,
            onValueChange = onValueChange,
            enabled = enabled,
            singleLine = true,
            textStyle = BowtieType.body.copy(
                color = if (enabled) BowtieColors.text else BowtieColors.dim,
            ),
            visualTransformation = if (isPassword) {
                PasswordVisualTransformation()
            } else {
                VisualTransformation.None
            },
            keyboardOptions = keyboardOptions,
            keyboardActions = keyboardActions,
            interactionSource = interactionSource,
            cursorBrush = SolidColor(BowtieColors.amber),
            decorationBox = { inner ->
                if (value.isEmpty() && placeholder != null) {
                    Text(
                        text = placeholder,
                        style = BowtieType.body,
                        color = BowtieColors.dim,
                    )
                }
                inner()
            },
            modifier = Modifier
                .fillMaxWidth()
                .background(BowtieColors.surface, shape)
                .border(
                    width = if (isFocused) 2.dp else 1.dp,
                    color = borderColor,
                    shape = shape,
                )
                .padding(BowtieDimens.fieldPadding),
        )
    }
}
