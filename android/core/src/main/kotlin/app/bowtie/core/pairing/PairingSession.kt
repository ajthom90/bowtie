package app.bowtie.core.pairing

import app.bowtie.core.ServerUrl
import java.security.MessageDigest
import java.security.SecureRandom

data class PairingResponse(val status: Int, val headers: List<Pair<String, String>>, val body: ByteArray) {
    fun serialize(): ByteArray {
        val head = StringBuilder("HTTP/1.1 $status ${reason(status)}\r\n")
        for ((name, value) in headers) head.append(name).append(": ").append(value).append("\r\n")
        head.append("\r\n")
        return head.toString().toByteArray(Charsets.UTF_8) + body
    }

    private fun reason(code: Int) = when (code) {
        200 -> "OK"
        303 -> "See Other"
        400 -> "Bad Request"
        403 -> "Forbidden"
        404 -> "Not Found"
        413 -> "Payload Too Large"
        else -> "OK"
    }
}

/**
 * The phone-facing side of TV pairing: pure request-to-response logic, no sockets.
 * The TV shows a QR for `/p?t=<token>`; the phone types a server address, the TV
 * probes it, and the phone is then redirected to that server's `/link` page.
 */
class PairingSession(val token: String = newToken()) {
    sealed interface State {
        data object Waiting : State
        data class Connecting(val address: String) : State
        data class Failed(val address: String, val message: String) : State
        data class Ready(val link: String) : State
        data object Closed : State
    }

    @get:Synchronized
    var state: State = State.Waiting
        private set

    private var badTokens = 0

    /** Returns the response and, when a new address was accepted, that normalized address. */
    @Synchronized
    fun handle(parse: Parse): Pair<PairingResponse, String?> {
        if (state == State.Closed) return forbidden()
        val request = when (parse) {
            is Parse.Complete -> parse.request
            Parse.TooLarge -> return html(413, PairingPage.tooLarge()) to null
            Parse.Bad, Parse.Incomplete -> return html(400, PairingPage.badRequest()) to null
        }

        val post = request.method == "POST" && request.path == "/p"
        val get = request.method == "GET" && (request.path == "/p" || request.path == "/p/status")
        if (!post && !get) return html(404, PairingPage.notFound()) to null

        val supplied = if (post) request.form["t"] else request.query["t"]
        if (supplied == null) return forbidden()
        if (!MessageDigest.isEqual(supplied.toByteArray(Charsets.UTF_8), token.toByteArray(Charsets.UTF_8))) {
            if (++badTokens >= MAX_BAD_TOKENS) state = State.Closed
            return forbidden()
        }

        val current = state
        return when {
            post -> when (current) {
                is State.Waiting, is State.Failed -> submit(request.form["server"].orEmpty())
                else -> redirect(statusPath()) to null
            }
            request.path == "/p" -> when (current) {
                is State.Waiting -> html(200, PairingPage.form(token)) to null
                is State.Failed -> html(200, PairingPage.form(token, current.message, current.address)) to null
                else -> redirect(statusPath()) to null
            }
            else -> when (current) {
                is State.Connecting -> html(200, PairingPage.waiting(token)) to null
                is State.Failed -> html(200, PairingPage.form(token, current.message, current.address)) to null
                is State.Ready -> redirect(current.link) to null
                else -> redirect("/p?t=$token") to null
            }
        }
    }

    /** The TV could not reach [address]; let the phone try again. */
    @Synchronized
    fun connectFailed(address: String) {
        if (state is State.Connecting) {
            state = State.Failed(address, "Couldn't reach a Bowtie server at $address. Check the address and try again.")
        }
    }

    /** A sign-in code exists; the phone's next status poll goes to [link]. Later codes replace it. */
    @Synchronized
    fun codeIssued(link: String) {
        if (state is State.Connecting || state is State.Ready) state = State.Ready(link)
    }

    @Synchronized
    fun close() {
        state = State.Closed
    }

    private fun submit(raw: String): Pair<PairingResponse, String?> {
        val url = ServerUrl.normalize(raw)
        // ServerUrl.normalize accepts hosts like `"><script>`; a real host is only letters, digits, `.`, `-`, `:` (IPv6).
        if (url == null || !url.host.all { it.isLetterOrDigit() || it in ".-:" }) {
            return html(200, PairingPage.form(token, PairingPage.BAD_ADDRESS, raw)) to null
        }
        val address = url.toString().removeSuffix("/")
        state = State.Connecting(address)
        return redirect(statusPath()) to address
    }

    private fun statusPath() = "/p/status?t=$token"

    private fun forbidden() = html(403, PairingPage.forbidden()) to null

    private fun html(status: Int, body: String): PairingResponse {
        val bytes = body.toByteArray(Charsets.UTF_8)
        return PairingResponse(
            status,
            listOf(
                "Content-Type" to "text/html; charset=utf-8",
                "Content-Length" to bytes.size.toString(),
                "Cache-Control" to "no-store",
                "Connection" to "close",
            ),
            bytes,
        )
    }

    private fun redirect(location: String) = PairingResponse(
        303,
        listOf(
            "Content-Length" to "0",
            "Cache-Control" to "no-store",
            "Connection" to "close",
            "Location" to location,
        ),
        ByteArray(0),
    )

    companion object {
        private const val MAX_BAD_TOKENS = 20

        /** 96 random bits as 24 lowercase hex characters. */
        fun newToken(): String {
            val bytes = ByteArray(12).also { SecureRandom().nextBytes(it) }
            return bytes.joinToString("") { "%02x".format(it) }
        }

        /** Where the phone goes once the TV has a sign-in code: the typed server's `/link` page. */
        fun linkUrl(server: String, userCode: String): String =
            "${server.removeSuffix("/")}/link?code=${userCode.replace("-", "")}"

        fun htmlEscape(s: String): String = buildString {
            for (c in s) when (c) {
                '&' -> append("&amp;")
                '<' -> append("&lt;")
                '>' -> append("&gt;")
                '"' -> append("&quot;")
                '\'' -> append("&#39;")
                else -> append(c)
            }
        }
    }
}
