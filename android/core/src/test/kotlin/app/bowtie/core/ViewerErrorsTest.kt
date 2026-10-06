package app.bowtie.core

import app.bowtie.core.BowtieClientRecordingsTest.Companion.TOKEN_PAIR
import app.bowtie.core.vm.ChannelListViewModel
import kotlinx.coroutines.runBlocking
import kotlinx.serialization.SerializationException
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import org.junit.Assert.assertEquals
import org.junit.Assert.fail
import org.junit.Test
import java.io.IOException

/** Plain-words errors: viewers never see exception text, HTTP codes or raw bodies. */
class ViewerErrorsTest {

    @Test
    fun networkFailureSaysCantReachServer() {
        assertEquals(
            "Can't reach your Bowtie server. Check your connection and try again.",
            ViewerErrors.message(BowtieError.Network(IOException("failed to connect to /10.0.0.2 (port 8400)"))),
        )
        assertEquals(ViewerErrors.CANT_REACH_SERVER, ViewerErrors.message(IOException("timeout")))
    }

    @Test
    fun serverMessagesAreAlreadyPlainWords() {
        val plain = "This channel isn't coming in right now. Try again later or pick another channel."
        assertEquals(plain, ViewerErrors.message(BowtieError.Server(502, plain)))
        assertEquals(
            "Blocked by parental controls (rated TV-MA)",
            ViewerErrors.message(BowtieError.Parental("Blocked by parental controls (rated TV-MA)")),
        )
    }

    @Test
    fun serverWithoutAMessageIsSomethingWentWrong() {
        assertEquals("Something went wrong. Try again.", ViewerErrors.message(BowtieError.Server(500, "")))
    }

    @Test
    fun decodeAndOtherExceptionsAreSomethingWentWrong() {
        assertEquals(
            ViewerErrors.SOMETHING_WRONG,
            ViewerErrors.message(SerializationException("decode failed: Unexpected JSON token at offset 0")),
        )
        assertEquals(ViewerErrors.SOMETHING_WRONG, ViewerErrors.message(IllegalStateException("boom")))
    }

    @Test
    fun signedOutAndBusyHavePlainCopy() {
        assertEquals("Your session ended. Sign in again.", ViewerErrors.message(BowtieError.Unauthorized))
        assertEquals(
            "All tuners are in use. Try again in a few minutes.",
            ViewerErrors.message(BowtieError.TunersBusy(emptyList())),
        )
    }

    @Test
    fun channelListUsesPlainWords() {
        assertEquals(
            ViewerErrors.CANT_REACH_SERVER,
            ChannelListViewModel.messageFor(BowtieError.Network(IOException("Connection refused"))),
        )
        assertEquals(
            ViewerErrors.SOMETHING_WRONG,
            ChannelListViewModel.messageFor(SerializationException("decode failed")),
        )
    }

    @Test
    fun recordingErrorsUsePlainWords() {
        assertEquals(
            ViewerErrors.CANT_REACH_SERVER,
            RecordingLogic.errorMessage(BowtieError.Network(IOException("reset"))),
        )
        assertEquals(ViewerErrors.SOMETHING_WRONG, RecordingLogic.errorMessage(BowtieError.Server(500, "")))
        assertEquals(ViewerErrors.SOMETHING_WRONG, RecordingLogic.errorMessage(RuntimeException("npe")))
    }

    /** A proxy's HTML error page (or an empty body) is not a server message. */
    @Test
    fun nonJsonErrorBodyIsNotShownToViewers() = runBlocking {
        val server = MockWebServer()
        server.start()
        try {
            server.enqueue(MockResponse().setBody(TOKEN_PAIR))
            val c = BowtieClient(server.url("/"), InMemoryTokenStore())
            c.login("alice", "secret")
            server.enqueue(MockResponse().setResponseCode(502).setBody("<html><h1>502 Bad Gateway</h1></html>"))
            try {
                c.channels()
                fail("expected Server")
            } catch (e: BowtieError.Server) {
                assertEquals(502, e.status)
                assertEquals(ViewerErrors.SOMETHING_WRONG, ViewerErrors.message(e))
            }
            server.enqueue(MockResponse().setResponseCode(500))
            try {
                c.channels()
                fail("expected Server")
            } catch (e: BowtieError.Server) {
                assertEquals(ViewerErrors.SOMETHING_WRONG, ViewerErrors.message(e))
            }
        } finally {
            server.shutdown()
        }
    }
}
