package app.bowtie.tv.ui

import com.google.zxing.BinaryBitmap
import com.google.zxing.RGBLuminanceSource
import com.google.zxing.common.HybridBinarizer
import com.google.zxing.qrcode.QRCodeReader
import org.junit.Assert.assertEquals
import org.junit.Test

class PairingQrTest {
    @Test fun matrixScansBack() {
        val text = "http://255.255.255.255:65535/p?t=0123456789abcdef01234567"
        val m = pairingMatrix(text, 300)
        val px = IntArray(m.width * m.height) { i -> if (m.get(i % m.width, i / m.width)) 0xFF000000.toInt() else -1 }
        val bmp = BinaryBitmap(HybridBinarizer(RGBLuminanceSource(m.width, m.height, px)))
        assertEquals(text, QRCodeReader().decode(bmp).text)
    }
}
