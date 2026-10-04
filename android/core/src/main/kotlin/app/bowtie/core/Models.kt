package app.bowtie.core

import kotlinx.serialization.KSerializer
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.descriptors.PrimitiveKind
import kotlinx.serialization.descriptors.PrimitiveSerialDescriptor
import kotlinx.serialization.descriptors.SerialDescriptor
import kotlinx.serialization.encoding.Decoder
import kotlinx.serialization.encoding.Encoder
import kotlinx.serialization.json.Json
import java.time.Instant

/**
 * Shared JSON config for all viewer API (de)serialization.
 * Server may send fields our models omit (e.g. full SessionInfo in 503 bodies);
 * strict decoding is a bug.
 */
val BowtieJson: Json = Json {
    ignoreUnknownKeys = true
    encodeDefaults = false
}

/** ISO-8601 / RFC3339 Instant codec for java.time (desugared on API 25). */
object InstantIso8601Serializer : KSerializer<Instant> {
    override val descriptor: SerialDescriptor =
        PrimitiveSerialDescriptor("Instant", PrimitiveKind.STRING)

    override fun serialize(encoder: Encoder, value: Instant) {
        encoder.encodeString(value.toString())
    }

    override fun deserialize(decoder: Decoder): Instant {
        return Instant.parse(decoder.decodeString())
    }
}

@Serializable
data class User(
    val id: Long,
    val username: String,
    val role: String,
    val maxQuality: String,
)

@Serializable
data class TokenPair(
    val accessToken: String,
    val refreshToken: String,
    val user: User,
)

/** `POST /auth/device`: a quick sign-in code for a TV to show. */
@Serializable
data class DeviceSignIn(
    /** Secret; only this device knows it. Polled with `/auth/device/token`. */
    val deviceCode: String,
    /** What the person types on their phone ("BCDF-2345"). */
    val userCode: String,
    /** The web app's /link page with the code filled in. */
    val verifyUrl: String,
    /** Server-relative PNG of [verifyUrl] as a QR code. */
    val qrUrl: String = "",
    /** Seconds the code lives. */
    val expiresIn: Int,
    /** Seconds between polls. */
    val interval: Int,
)

/** One `/auth/device/token` poll. */
sealed class DevicePoll {
    /** 428: not approved yet. */
    data object Pending : DevicePoll()

    /** 410: expired, used or unknown; start over with a new code. */
    data object Expired : DevicePoll()

    /** 200: approved; the client now holds the session, as after a password sign-in. */
    data class SignedIn(val user: User) : DevicePoll()
}

@Serializable
data class Channel(
    val id: Long,
    val guideNumber: String,
    val name: String,
    val logoUrl: String,
    /** Last tune outcome learned by the server: "ok", "noSignal" or "unknown"; null from servers older than 0.6.3. */
    val reception: String? = null,
    /** The caller starred this channel; null from servers without favorites (hide the star). */
    val favorite: Boolean? = null,
) {
    /** The antenna got no signal the last time this channel was tuned. */
    val hasNoSignal: Boolean get() = reception == "noSignal"
}

/** A channel the caller watched recently (`GET /api/v1/me/recents`), newest first. */
@Serializable
data class RecentChannel(
    val channelId: Long,
    val guideNumber: String,
    val name: String,
    val logoUrl: String,
    @Serializable(with = InstantIso8601Serializer::class)
    val watchedAt: Instant,
)

@Serializable
data class GuideProgram(
    @Serializable(with = InstantIso8601Serializer::class)
    val start: Instant,
    @Serializable(with = InstantIso8601Serializer::class)
    val stop: Instant,
    val title: String,
    val subtitle: String,
    val description: String,
    val category: String,
    /** Present when this program is scheduled or recorded (servers with the DVR). */
    val recording: GuideRecordingMark? = null,
    /** Guide program ID of the episode (when the source has one). */
    val programId: String? = null,
    /** Series ID of the show (when the source has one). */
    val seriesId: String? = null,
    /** First airing. */
    val isNew: Boolean? = null,
    /** Rating from the guide source (e.g. TV-14; "" = not rated). */
    val rating: String = "",
    /** Parental controls block this program for the caller (description is hidden). */
    val locked: Boolean = false,
)

/** One airing from `GET /guide/search`: the program plus the channel it's on. */
@Serializable
data class GuideSearchResult(
    val channelId: Long,
    val guideNumber: String = "",
    val channelName: String = "",
    val logoUrl: String = "",
    @Serializable(with = InstantIso8601Serializer::class)
    val start: Instant,
    @Serializable(with = InstantIso8601Serializer::class)
    val stop: Instant,
    val title: String,
    /** Episode title. */
    val subtitle: String = "",
    val description: String = "",
    val category: String = "",
    val rating: String = "",
    /** Parental controls block this program for the caller (description is hidden). */
    val locked: Boolean = false,
    /** Present when this airing is scheduled or recorded. */
    val recording: GuideRecordingMark? = null,
)

/** A guide program's DVR mark: which recording covers it, and its state. */
@Serializable
data class GuideRecordingMark(
    val id: Long,
    val state: String,
)

@Serializable
data class GuideChannel(
    val channelId: Long,
    val guideNumber: String,
    val name: String,
    val logoUrl: String,
    val programs: List<GuideProgram>,
    /** The caller starred this channel; null from servers without favorites. */
    val favorite: Boolean? = null,
)

@Serializable
data class ClientCaps(
    val videoCodecs: List<String>,
    val audioCodecs: List<String>,
    val maxHeight: Int,
    val profile: String,
)

@Serializable
data class SessionInfoMeta(
    val videoCodec: String,
    val profile: String,
    val backend: String,
    val channelName: String,
)

@Serializable
data class CreatedSession(
    val viewerId: String,
    val playlistUrl: String,
    val session: SessionInfoMeta? = null,
)

/**
 * Trimmed view of an active session for the tuners-busy UI.
 * Wire 503 bodies carry full SessionInfo; [BowtieJson] ignores unknown keys.
 */
@Serializable
data class ActiveSessionSummary(
    val channelName: String,
    val viewers: List<ViewerSummary> = emptyList(),
) {
    @Serializable
    data class ViewerSummary(
        val username: String,
    )
}

/** A DVR recording (OpenAPI `Recording`): scheduled, in progress, recorded or missed. */
@Serializable
data class Recording(
    val id: Long,
    val title: String,
    val subtitle: String = "",
    val description: String = "",
    val category: String = "",
    val channelId: Long,
    val channelName: String = "",
    @Serializable(with = InstantIso8601Serializer::class)
    val start: Instant,
    @Serializable(with = InstantIso8601Serializer::class)
    val stop: Instant,
    /** scheduled, waiting, recording, converting, ready or failed. */
    val state: String,
    /** More than a minute is missing (late start, dropped stream, or a restart). */
    val partial: Boolean = false,
    /** "", noTuner, noSignal, diskFull, error or skipped. */
    val failure: String = "",
    val failureDetail: String = "",
    val durationSec: Int = 0,
    val sizeBytes: Long = 0,
    /** Kept: never deleted automatically when space runs low. */
    @SerialName("protected")
    val isProtected: Boolean = false,
    /** The caller's resume position. */
    val positionSec: Int = 0,
    val scheduledBy: String = "",
    /** The caller may stop, delete or keep it (scheduler or admin). */
    val canManage: Boolean = false,
    /** The program's rating when scheduled ("" = not rated). */
    val rating: String = "",
    /** Series rule that scheduled it (0 = one-off). */
    val ruleId: Long = 0,
    /** Parental controls block it for the caller (no description; play is 403). */
    val locked: Boolean = false,
) {
    companion object {
        const val SCHEDULED = "scheduled"
        const val WAITING = "waiting"
        const val RECORDING = "recording"
        const val CONVERTING = "converting"
        const val READY = "ready"
        const val FAILED = "failed"
    }
}

/** A series recording rule (OpenAPI `RecordingRule`): record every (new) episode of a show. */
@Serializable
data class RecordingRule(
    val id: Long,
    val title: String,
    val seriesId: String = "",
    /** 0 = any channel. */
    val channelId: Long = 0,
    val channelName: String = "",
    val newOnly: Boolean = true,
    /** Keep only this many recordings of the show (0 = all). */
    val keepLatest: Int = 0,
    val scheduledBy: String = "",
    /** The caller may delete it (who made it, or an admin). */
    val canManage: Boolean = false,
    @Serializable(with = InstantIso8601Serializer::class)
    val createdAt: Instant? = null,
)

/** 201 body of `POST /recording-rules`: the rule and how many airings it scheduled now. */
@Serializable
data class CreatedRecordingRule(
    val rule: RecordingRule,
    val scheduled: Int = 0,
)

@Serializable
data class RecordingWarning(
    val code: String,
    val message: String,
)

/** 201 body of `POST /recordings`. */
@Serializable
data class ScheduledRecording(
    val recording: Recording,
    val warnings: List<RecordingWarning> = emptyList(),
)

/** `POST /recordings/{id}/play`: server-relative, token-signed VOD playlist. */
@Serializable
data class RecordingPlayback(
    val playlistUrl: String,
    val positionSec: Int,
    val durationSec: Int,
)
