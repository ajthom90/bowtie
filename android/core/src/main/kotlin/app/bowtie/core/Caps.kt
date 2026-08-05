package app.bowtie.core

import android.content.Context
import android.media.AudioFormat
import android.media.AudioManager
import android.media.MediaCodecList
import android.os.Build
import android.view.WindowManager
import kotlin.math.max
import kotlin.math.min

/**
 * Client capability detection for session negotiation.
 *
 * Pure [detect] is fully unit-testable (inject codec list / AC-3 flag / height).
 * [current] is a thin platform wrapper that gathers real device inputs — untested
 * on JVM (Robolectric codec shadows are weak).
 *
 * Contract (design): h264 always; hevc when a hardware HEVC decoder exists;
 * aac always; ac3/eac3 when passthrough or decoder support confirms;
 * maxHeight from display metrics, capped at 1080 in v1; profile "" (Auto).
 */
object Caps {

    /** Quality ladder max height advertised in v1 (phones/tablets). */
    const val V1_MAX_HEIGHT_CAP = 1080

    /**
     * Pure capability construction from injected inputs.
     *
     * @param hardwareDecoders MIME types of hardware decoders (e.g. `"video/hevc"`, `"audio/ac3"`)
     * @param ac3Passthrough true when [AudioManager] reports AC-3 / E-AC-3 passthrough capability
     * @param displayHeight physical display height in pixels (larger edge preferred by caller)
     */
    fun detect(
        hardwareDecoders: List<String>,
        ac3Passthrough: Boolean,
        displayHeight: Int,
    ): ClientCaps {
        val mimes = hardwareDecoders.map { it.lowercase() }

        val videoCodecs = mutableListOf("h264")
        if (mimes.any { it == "video/hevc" || it.contains("hevc") || it.contains("h265") }) {
            videoCodecs.add("hevc")
        }

        val audioCodecs = mutableListOf("aac")
        val hasAc3Decoder = mimes.any {
            it == "audio/ac3" || it == "audio/eac3" ||
                it.contains("ac3") || it.contains("eac3") || it.contains("ac-3")
        }
        if (ac3Passthrough || hasAc3Decoder) {
            audioCodecs.add("ac3")
            audioCodecs.add("eac3")
        }

        val maxHeight = min(max(displayHeight, 0), V1_MAX_HEIGHT_CAP)

        return ClientCaps(
            videoCodecs = videoCodecs,
            audioCodecs = audioCodecs,
            maxHeight = maxHeight,
            profile = "",
        )
    }

    /**
     * Gathers real device codecs / AC-3 passthrough / display height and calls [detect].
     * Not covered by unit tests (platform-only).
     */
    fun current(context: Context): ClientCaps {
        val decoders = hardwareDecoderMimes()
        val ac3 = hasAc3Passthrough(context)
        val height = displayHeightPx(context)
        return detect(decoders, ac3, height)
    }

    private fun hardwareDecoderMimes(): List<String> {
        val out = LinkedHashSet<String>()
        val list = MediaCodecList(MediaCodecList.ALL_CODECS)
        for (info in list.codecInfos) {
            if (info.isEncoder) continue
            if (isSoftwareOnly(info.name)) continue
            for (type in info.supportedTypes) {
                out.add(type)
            }
        }
        return out.toList()
    }

    /** Filter known software decoder name prefixes (stable across API levels). */
    private fun isSoftwareOnly(codecName: String): Boolean {
        val n = codecName.lowercase()
        return n.startsWith("omx.google.") ||
            n.startsWith("c2.android.") ||
            n.startsWith("c2.google.") ||
            n.contains(".sw.") ||
            n.endsWith(".sw")
    }

    private fun hasAc3Passthrough(context: Context): Boolean {
        val am = context.getSystemService(Context.AUDIO_SERVICE) as? AudioManager ?: return false
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) {
            return am.getDevices(AudioManager.GET_DEVICES_OUTPUTS).any { device ->
                device.encodings.any { enc ->
                    enc == AudioFormat.ENCODING_AC3 ||
                        enc == AudioFormat.ENCODING_E_AC3 ||
                        enc == AudioFormat.ENCODING_E_AC3_JOC
                }
            }
        }
        return false
    }

    private fun displayHeightPx(context: Context): Int {
        // Prefer the larger edge so portrait phones still report ~height of landscape content.
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R) {
            val wm = context.getSystemService(Context.WINDOW_SERVICE) as? WindowManager
            val bounds = wm?.currentWindowMetrics?.bounds
            if (bounds != null) {
                return max(bounds.height(), bounds.width())
            }
        }
        val dm = context.resources.displayMetrics
        return max(dm.heightPixels, dm.widthPixels)
    }
}
