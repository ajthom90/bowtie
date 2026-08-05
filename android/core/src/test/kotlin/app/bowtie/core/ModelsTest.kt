package app.bowtie.core

import kotlinx.serialization.Serializable
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import java.time.Instant

class ModelsTest {

    @Test
    fun decode_tokenPair() {
        val json = """
            {
              "accessToken": "access-xyz",
              "refreshToken": "refresh-xyz",
              "user": {
                "id": 1,
                "username": "admin",
                "role": "admin",
                "maxQuality": "high"
              }
            }
        """.trimIndent()

        val pair = BowtieJson.decodeFromString<TokenPair>(json)
        assertEquals("access-xyz", pair.accessToken)
        assertEquals("refresh-xyz", pair.refreshToken)
        assertEquals(1L, pair.user.id)
        assertEquals("admin", pair.user.username)
        assertEquals("admin", pair.user.role)
        assertEquals("high", pair.user.maxQuality)
    }

    @Test
    fun decode_guideChannel_rfc3339Instants() {
        val json = """
            {
              "channelId": 42,
              "guideNumber": "7.1",
              "name": "WABC",
              "logoUrl": "https://example.com/logo.png",
              "programs": [
                {
                  "start": "2026-08-05T18:00:00Z",
                  "stop": "2026-08-05T19:00:00Z",
                  "title": "News",
                  "subtitle": "Evening",
                  "description": "Local news",
                  "category": "News"
                }
              ]
            }
        """.trimIndent()

        val channel = BowtieJson.decodeFromString<GuideChannel>(json)
        assertEquals(42L, channel.channelId)
        assertEquals("7.1", channel.guideNumber)
        assertEquals("WABC", channel.name)
        assertEquals(1, channel.programs.size)
        val program = channel.programs[0]
        assertEquals(Instant.parse("2026-08-05T18:00:00Z"), program.start)
        assertEquals(Instant.parse("2026-08-05T19:00:00Z"), program.stop)
        assertEquals("News", program.title)
        assertEquals("Evening", program.subtitle)
        assertEquals("Local news", program.description)
        assertEquals("News", program.category)
    }

    @Test
    fun decode_createdSession_withSession() {
        val json = """
            {
              "viewerId": "viewer-1",
              "playlistUrl": "/api/v1/stream/viewer-1/index.m3u8?token=abc",
              "session": {
                "videoCodec": "h264",
                "profile": "high",
                "backend": "videotoolbox",
                "channelName": "WABC"
              }
            }
        """.trimIndent()

        val created = BowtieJson.decodeFromString<CreatedSession>(json)
        assertEquals("viewer-1", created.viewerId)
        assertEquals("/api/v1/stream/viewer-1/index.m3u8?token=abc", created.playlistUrl)
        assertNotNull(created.session)
        assertEquals("h264", created.session!!.videoCodec)
        assertEquals("high", created.session!!.profile)
        assertEquals("videotoolbox", created.session!!.backend)
        assertEquals("WABC", created.session!!.channelName)
    }

    @Test
    fun decode_createdSession_withoutSession() {
        val json = """
            {
              "viewerId": "viewer-2",
              "playlistUrl": "/api/v1/stream/viewer-2/index.m3u8?token=xyz"
            }
        """.trimIndent()

        val created = BowtieJson.decodeFromString<CreatedSession>(json)
        assertEquals("viewer-2", created.viewerId)
        assertEquals("/api/v1/stream/viewer-2/index.m3u8?token=xyz", created.playlistUrl)
        assertNull(created.session)
    }

    /**
     * 503 body uses full SessionInfo objects on the wire (id, channelId, key,
     * videoCodec, profile, backend, startedAt, viewers[{id,username,lastSeen}]).
     * Our model only keeps channelName + viewer usernames; ignoreUnknownKeys
     * must make decoding the FULL fixture safe.
     */
    @Test
    fun decode_tunersBusy_fullWireShape503() {
        // Full wire shape copied from openapi SessionInfo / ViewerInfo (not the trimmed model).
        val json = """
            {
              "error": "all tuners in use",
              "sessions": [
                {
                  "id": "sess-abc",
                  "channelId": 7,
                  "channelName": "WABC",
                  "key": "7|h264|high",
                  "videoCodec": "h264",
                  "profile": "high",
                  "backend": "videotoolbox",
                  "startedAt": "2026-08-05T12:00:00Z",
                  "viewers": [
                    {
                      "id": "viewer-1",
                      "username": "alice",
                      "lastSeen": "2026-08-05T12:05:00Z"
                    },
                    {
                      "id": "viewer-2",
                      "username": "bob",
                      "lastSeen": "2026-08-05T12:06:00Z"
                    }
                  ]
                }
              ]
            }
        """.trimIndent()

        val body = BowtieJson.decodeFromString<TunersBusyPayload>(json)
        assertEquals("all tuners in use", body.error)
        assertEquals(1, body.sessions.size)
        val session = body.sessions[0]
        assertEquals("WABC", session.channelName)
        assertEquals(2, session.viewers.size)
        assertEquals("alice", session.viewers[0].username)
        assertEquals("bob", session.viewers[1].username)

        // Extra wire fields must not surface on the summary model (trimmed).
        // Sanity: unknown keys were ignored rather than failing decode.
        assertTrue(session.viewers.all { it.username.isNotEmpty() })
    }

    @Test
    fun decode_channel() {
        val json = """
            {
              "id": 3,
              "guideNumber": "4.1",
              "name": "WNBC",
              "logoUrl": ""
            }
        """.trimIndent()
        val channel = BowtieJson.decodeFromString<Channel>(json)
        assertEquals(3L, channel.id)
        assertEquals("4.1", channel.guideNumber)
        assertEquals("WNBC", channel.name)
        assertEquals("", channel.logoUrl)
    }

    @Test
    fun decode_clientCaps() {
        val json = """
            {
              "videoCodecs": ["h264", "hevc"],
              "audioCodecs": ["aac", "ac3"],
              "maxHeight": 1080,
              "profile": "high"
            }
        """.trimIndent()
        val caps = BowtieJson.decodeFromString<ClientCaps>(json)
        assertEquals(listOf("h264", "hevc"), caps.videoCodecs)
        assertEquals(listOf("aac", "ac3"), caps.audioCodecs)
        assertEquals(1080, caps.maxHeight)
        assertEquals("high", caps.profile)
    }

    @Test
    fun bowtieError_taxonomyExists() {
        val unauthorized: BowtieError = BowtieError.Unauthorized
        val busy: BowtieError = BowtieError.TunersBusy(emptyList())
        val negotiation: BowtieError = BowtieError.NegotiationFailed("no codec")
        val notFound: BowtieError = BowtieError.NotFound
        val server: BowtieError = BowtieError.Server(500, "boom")
        val network: BowtieError = BowtieError.Network(RuntimeException("offline"))

        assertTrue(unauthorized is BowtieError.Unauthorized)
        assertTrue(busy is BowtieError.TunersBusy)
        assertEquals("no codec", (negotiation as BowtieError.NegotiationFailed).message)
        assertTrue(notFound is BowtieError.NotFound)
        assertEquals(500, (server as BowtieError.Server).status)
        assertNotNull((network as BowtieError.Network).cause2)
    }
}

/** Wire envelope for 503 tuners-busy; kept here so Models stays viewer-DTO focused. */
@Serializable
private data class TunersBusyPayload(
    val error: String,
    val sessions: List<ActiveSessionSummary>,
)
