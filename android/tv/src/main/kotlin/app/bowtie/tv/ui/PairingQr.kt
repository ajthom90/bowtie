package app.bowtie.tv.ui

import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.widthIn
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.FilterQuality
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.tv.material3.Text
import app.bowtie.tv.BowtieColors
import app.bowtie.tv.BowtieType
import com.google.zxing.BarcodeFormat
import com.google.zxing.EncodeHintType
import com.google.zxing.common.BitMatrix
import com.google.zxing.qrcode.QRCodeWriter
import com.google.zxing.qrcode.decoder.ErrorCorrectionLevel

/** QR modules for [text], scaled by zxing to the largest whole-module size that fits [sizePx]. */
internal fun pairingMatrix(text: String, sizePx: Int): BitMatrix =
    QRCodeWriter().encode(
        text,
        BarcodeFormat.QR_CODE,
        sizePx,
        sizePx,
        mapOf(EncodeHintType.ERROR_CORRECTION to ErrorCorrectionLevel.M, EncodeHintType.MARGIN to 2),
    )

internal fun qrBitmap(text: String, sizePx: Int): ImageBitmap {
    val m = pairingMatrix(text, sizePx)
    val px = IntArray(m.width * m.height) { i ->
        if (m.get(i % m.width, i / m.width)) 0xFF000000.toInt() else 0xFFFFFFFF.toInt()
    }
    return android.graphics.Bitmap
        .createBitmap(px, m.width, m.height, android.graphics.Bitmap.Config.ARGB_8888)
        .asImageBitmap()
}

/** "Scan with your phone to set up this TV"; draws nothing while there is no [url]. */
@Composable
fun PairingQrPanel(url: String?, modifier: Modifier = Modifier) {
    if (url == null) return
    val bitmap = remember(url) { qrBitmap(url, 320) }
    Column(
        modifier = modifier.widthIn(max = 352.dp),
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.Center,
    ) {
        Box(modifier = Modifier.size(352.dp).background(Color.White), contentAlignment = Alignment.Center) {
            Image(
                bitmap = bitmap,
                contentDescription = "QR code to set up this TV from your phone",
                modifier = Modifier.size(320.dp),
                filterQuality = FilterQuality.None,
            )
        }
        Spacer(Modifier.height(16.dp))
        Text(
            text = "Scan with your phone to set up this TV",
            style = BowtieType.body,
            color = BowtieColors.text,
            textAlign = TextAlign.Center,
        )
        Spacer(Modifier.height(8.dp))
        Text(
            text = "Or open $url on your phone",
            style = BowtieType.label,
            color = BowtieColors.dim,
            textAlign = TextAlign.Center,
        )
    }
}
