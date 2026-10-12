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
import java.util.concurrent.Semaphore
import java.util.concurrent.atomic.AtomicBoolean

/** Serves [session] over plain HTTP, up to four connections at once. [onClosed] runs once the session locks out. */
class PairingServer(
    private val session: PairingSession,
    private val onClosed: () -> Unit = {},
    private val onSubmit: (String) -> Unit,
) {
    @Volatile private var socket: ServerSocket? = null
    @Volatile private var stopped = false
    private val slots = Semaphore(MAX_CONNECTIONS)
    private val closedNotified = AtomicBoolean(false)

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
                    if (!slots.tryAcquire()) {
                        runCatching { client.close() }
                        continue
                    }
                    launch {
                        try {
                            runCatching { client.use { serve(it) } }
                        } finally {
                            slots.release()
                        }
                        if (session.state == PairingSession.State.Closed) notifyClosed()
                    }
                }
            } finally {
                val byUs = stopped
                stop()
                if (!byUs) notifyClosed()
            }
        }
        return server.localPort
    }

    /** Lockout or listener failure: stop listening and tell the owner, once. */
    private fun notifyClosed() {
        if (!closedNotified.compareAndSet(false, true)) return
        stop()
        onClosed()
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
        const val MAX_CONNECTIONS = 4
        const val MAX_ACCEPT_FAILURES = 3
        const val ACCEPT_RETRY_MS = 200L
    }
}
