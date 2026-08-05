package app.bowtie.core

import kotlinx.serialization.KSerializer
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

@Serializable
data class Channel(
    val id: Long,
    val guideNumber: String,
    val name: String,
    val logoUrl: String,
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
)

@Serializable
data class GuideChannel(
    val channelId: Long,
    val guideNumber: String,
    val name: String,
    val logoUrl: String,
    val programs: List<GuideProgram>,
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
