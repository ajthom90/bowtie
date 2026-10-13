package app.bowtie.core.pairing

import java.io.ByteArrayOutputStream

sealed interface Parse {
    data object Incomplete : Parse
    data object TooLarge : Parse
    data object Bad : Parse
    data class Complete(val request: HttpRequest) : Parse
}

/** A minimal parsed HTTP/1.1 request: just what the pairing page needs. */
data class HttpRequest(
    val method: String,
    val path: String,
    val query: Map<String, String>,
    val form: Map<String, String>,
) {
    companion object {
        private val HEADER_END = "\r\n\r\n".toByteArray(Charsets.ISO_8859_1)

        /**
         * Parse [bytes] read so far. [Parse.Incomplete] means keep reading; anything over
         * [limit] bytes (including a declared Content-Length that would exceed it) is
         * [Parse.TooLarge].
         */
        fun parse(bytes: ByteArray, limit: Int = 8192): Parse {
            if (bytes.size > limit) return Parse.TooLarge
            val headerEnd = indexOf(bytes, HEADER_END)
            if (headerEnd < 0) return Parse.Incomplete

            val head = String(bytes, 0, headerEnd, Charsets.ISO_8859_1).split("\r\n")
            val requestLine = head[0].split(" ")
            if (requestLine.size != 3 || !requestLine[2].startsWith("HTTP/") || !requestLine[1].startsWith("/")) {
                return Parse.Bad
            }

            var contentLength = 0
            for (line in head.drop(1)) {
                val colon = line.indexOf(':')
                if (colon <= 0) return Parse.Bad
                if (line.substring(0, colon).trim().equals("Content-Length", ignoreCase = true)) {
                    contentLength = line.substring(colon + 1).trim().toIntOrNull()?.takeIf { it >= 0 }
                        ?: return Parse.Bad
                }
            }

            val bodyStart = headerEnd + HEADER_END.size
            if (bodyStart.toLong() + contentLength > limit) return Parse.TooLarge
            if (bytes.size - bodyStart < contentLength) return Parse.Incomplete

            val target = requestLine[1]
            val queryStart = target.indexOf('?')
            val path = if (queryStart < 0) target else target.substring(0, queryStart)
            val query = if (queryStart < 0) emptyMap() else pairs(target.substring(queryStart + 1))
            val body = String(bytes, bodyStart, contentLength, Charsets.UTF_8)
            return Parse.Complete(HttpRequest(requestLine[0], path, query, pairs(body)))
        }

        /** Decode `application/x-www-form-urlencoded` text: `+` is a space, `%XX` is a UTF-8 byte. */
        fun urlDecode(s: String): String {
            val out = ByteArrayOutputStream()
            var i = 0
            while (i < s.length) {
                val c = s[i]
                if (c == '%' && i + 2 < s.length && hex(s[i + 1]) >= 0 && hex(s[i + 2]) >= 0) {
                    out.write(hex(s[i + 1]) * 16 + hex(s[i + 2]))
                    i += 3
                } else {
                    val bytes = (if (c == '+') " " else c.toString()).toByteArray(Charsets.UTF_8)
                    out.write(bytes, 0, bytes.size)
                    i++
                }
            }
            return out.toString(Charsets.UTF_8)
        }

        private fun pairs(s: String): Map<String, String> {
            val map = LinkedHashMap<String, String>()
            for (part in s.split('&')) {
                if (part.isEmpty()) continue
                val eq = part.indexOf('=')
                val key = urlDecode(if (eq < 0) part else part.substring(0, eq))
                val value = if (eq < 0) "" else urlDecode(part.substring(eq + 1))
                map.putIfAbsent(key, value)
            }
            return map
        }

        private fun hex(c: Char): Int = Character.digit(c, 16)

        private fun indexOf(haystack: ByteArray, needle: ByteArray): Int {
            outer@ for (i in 0..haystack.size - needle.size) {
                for (j in needle.indices) if (haystack[i + j] != needle[j]) continue@outer
                return i
            }
            return -1
        }
    }
}
