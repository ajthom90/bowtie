package app.bowtie.core.pairing

import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import java.io.ByteArrayOutputStream
import java.net.InetAddress
import java.net.ServerSocket
import java.net.Socket

/** Serves [session] over plain HTTP, one connection at a time. [onClosed] runs once the session locks out. */
class PairingServer(
    private val session: PairingSession,
    private val onClosed: () -> Unit = {},
    private val onSubmit: (String) -> Unit,
) {
    @Volatile private var socket: ServerSocket? = null
    @Volatile private var stopped = false

    /** Binds an ephemeral port on [bindAddress] only (the address shown in the QR) and starts accepting; returns the port, or null if binding failed. */
    fun start(scope: CoroutineScope, bindAddress: String): Int? {
        val server = runCatching { ServerSocket(0, 50, InetAddress.getByName(bindAddress)) }.getOrNull() ?: return null
        socket = server
        scope.coroutineContext[Job]?.invokeOnCompletion { stop() }
        scope.launch(Dispatchers.IO) {
            try {
                var failures = 0
                while (!server.isClosed) {
                    val client = try {
                        server.accept()
                    } catch (_: Exception) {
                        if (server.isClosed || ++failures > MAX_ACCEPT_FAILURES) break
                        delay(ACCEPT_RETRY_MS)
                        continue
                    }
                    failures = 0
                    runCatching { client.use { serve(it) } }
                    if (session.state == PairingSession.State.Closed) break
                }
            } finally {
                val byUs = stopped
                stop()
                if (!byUs) onClosed()
            }
        }
        return server.localPort
    }

    fun stop() {
        stopped = true
        runCatching { socket?.close() }
    }

    private fun serve(client: Socket) {
        val deadline = System.nanoTime() + READ_TIMEOUT_MS * 1_000_000L
        val input = client.getInputStream()
        val received = ByteArrayOutputStream()
        val chunk = ByteArray(2048)
        var parse: Parse = Parse.Incomplete
        while (parse == Parse.Incomplete) {
            val remainingMs = (deadline - System.nanoTime()) / 1_000_000L
            if (remainingMs <= 0) return
            client.soTimeout = remainingMs.toInt()
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
        /** Whole-request deadline, not per read, so a slow drip can't hold the listener. */
        const val READ_TIMEOUT_MS = 5000
        const val MAX_ACCEPT_FAILURES = 3
        const val ACCEPT_RETRY_MS = 200L
    }
}
