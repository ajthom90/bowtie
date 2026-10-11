package app.bowtie.core.pairing

import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import java.io.ByteArrayOutputStream
import java.net.ServerSocket
import java.net.Socket

/** Serves [session] over plain HTTP, one connection at a time. [onClosed] runs once the session locks out. */
class PairingServer(
    private val session: PairingSession,
    private val onClosed: () -> Unit = {},
    private val onSubmit: (String) -> Unit,
) {
    @Volatile private var socket: ServerSocket? = null

    /** Binds an ephemeral port on all interfaces and starts accepting; returns the port, or null if binding failed. */
    fun start(scope: CoroutineScope): Int? {
        val server = runCatching { ServerSocket(0) }.getOrNull() ?: return null
        socket = server
        scope.launch(Dispatchers.IO) {
            while (!server.isClosed) {
                val client = try { server.accept() } catch (_: Exception) { break }
                runCatching { client.use { serve(it) } }
                if (session.state == PairingSession.State.Closed) {
                    stop()
                    onClosed()
                    break
                }
            }
        }
        return server.localPort
    }

    fun stop() {
        runCatching { socket?.close() }
    }

    private fun serve(client: Socket) {
        client.soTimeout = READ_TIMEOUT_MS
        val input = client.getInputStream()
        val received = ByteArrayOutputStream()
        val chunk = ByteArray(2048)
        var parse: Parse = Parse.Incomplete
        while (parse == Parse.Incomplete) {
            val n = input.read(chunk)
            if (n < 0) return
            received.write(chunk, 0, n)
            parse = HttpRequest.parse(received.toByteArray())
        }
        val (response, submitted) = session.handle(parse)
        client.getOutputStream().apply { write(response.serialize()); flush() }
        if (submitted != null) onSubmit(submitted)
    }

    private companion object {
        const val READ_TIMEOUT_MS = 5000
    }
}
