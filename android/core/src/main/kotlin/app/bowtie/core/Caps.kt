package app.bowtie.core

import android.content.Context
import android.media.AudioAttributes
import android.media.AudioDeviceInfo
import android.media.AudioFormat
import android.media.AudioManager
import android.media.AudioTrack
import android.media.MediaCodecList
import android.os.Build
import android.view.WindowManager
import kotlin.math.max
import kotlin.math.min

/**
 * Client capability detection for session negotiation.
 *
 * Pure [detect] is fully unit-testable (inject codec list / AC-3 flag / height).
 * [current] gathers real device inputs; [audioPassthroughProbe] is a thin
 * platform wrapper (untested on JVM — inject [probe] into [current] instead).
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
     * @param ac3Passthrough true when the platform probe reports AC-3 / E-AC-3 passthrough
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
     *
     * @param probe injectable AC-3 passthrough probe (defaults to [audioPassthroughProbe]).
     *   Unit tests inject a constant so wiring is covered without platform audio APIs.
     */
    fun current(
        context: Context,
        probe: (Context) -> Boolean = ::audioPassthroughProbe,
    ): ClientCaps {
        val decoders = hardwareDecoderMimes()
        val ac3 = probe(context)
        val height = displayHeightPx(context)
        return detect(decoders, ac3, height)
    }

    /**
     * Platform AC-3 / E-AC-3 passthrough detection (thin wrapper — not unit-tested).
     *
     * Ladder (API-level strategy):
     * - **API 31+ (S):** [AudioManager.getDevices] output devices whose
     *   [AudioDeviceInfo.getEncodings] includes AC-3 / E-AC-3 / E-AC-3_JOC
     *   (device encodings check — not a "profile" path).
     * - **API 29–30 (Q–R):** [AudioTrack.isDirectPlaybackSupported] with a full
     *   [AudioFormat] (ENCODING_AC3 or ENCODING_E_AC3, 48000 Hz, CHANNEL_OUT_5POINT1)
     *   and [AudioAttributes] USAGE_MEDIA.
     * - **API 25–28:** best-effort — HDMI (or HDMI ARC) output device encodings
     *   contain AC-3 / E-AC-3. **False-negative risk** on some Fire OS 6 stacks
     *   that omit encodings from [AudioDeviceInfo] even when passthrough works.
     */
    fun audioPassthroughProbe(context: Context): Boolean {
        val am = context.getSystemService(Context.AUDIO_SERVICE) as? AudioManager ?: return false
        return when {
            Build.VERSION.SDK_INT >= Build.VERSION_CODES.S -> {
                am.getDevices(AudioManager.GET_DEVICES_OUTPUTS).any { device ->
                    device.encodings.any { isAc3FamilyEncoding(it) }
                }
            }
            Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q -> {
                isDirectAc3Supported()
            }
            else -> {
                // API 25–28: HDMI device encodings (best-effort; see KDoc false-negative risk).
                am.getDevices(AudioManager.GET_DEVICES_OUTPUTS).any { device ->
                    isHdmiOutput(device) && device.encodings.any { isAc3FamilyEncoding(it) }
                }
            }
        }
    }

    private fun isAc3FamilyEncoding(encoding: Int): Boolean =
        encoding == AudioFormat.ENCODING_AC3 ||
            encoding == AudioFormat.ENCODING_E_AC3 ||
            encoding == AudioFormat.ENCODING_E_AC3_JOC

    private fun isHdmiOutput(device: AudioDeviceInfo): Boolean {
        if (device.type == AudioDeviceInfo.TYPE_HDMI) return true
        // TYPE_HDMI_ARC added in API 24 (minSdk for this branch is 25).
        return device.type == AudioDeviceInfo.TYPE_HDMI_ARC
    }

    /**
     * API 29–30 direct-playback probe for AC-3 / E-AC-3.
     * [AudioTrack.isDirectPlaybackSupported] is deprecated from API 33 but is
     * the correct path on Q/R where the S encodings list is unavailable.
     */
    @Suppress("DEPRECATION")
    private fun isDirectAc3Supported(): Boolean {
        val attrs = AudioAttributes.Builder()
            .setUsage(AudioAttributes.USAGE_MEDIA)
            .build()
        val encodings = intArrayOf(
            AudioFormat.ENCODING_AC3,
            AudioFormat.ENCODING_E_AC3,
        )
        return encodings.any { encoding ->
            val format = AudioFormat.Builder()
                .setEncoding(encoding)
                .setSampleRate(48_000)
                .setChannelMask(AudioFormat.CHANNEL_OUT_5POINT1)
                .build()
            AudioTrack.isDirectPlaybackSupported(format, attrs)
        }
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
