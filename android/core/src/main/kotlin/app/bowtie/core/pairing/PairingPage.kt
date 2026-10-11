package app.bowtie.core.pairing

import app.bowtie.core.pairing.PairingSession.Companion.htmlEscape

/** The pairing pages. Markup is shared byte-for-byte with the other platforms. */
internal object PairingPage {
    const val TITLE = "Set up Bowtie on your TV"
    const val BAD_ADDRESS = "Enter an address like http://192.168.1.50:8400."
    const val EXPIRED = "This pairing code has expired. Scan the code on your TV again."

    private fun page(refresh: String, body: String): String =
        "<!doctype html><html><head><meta charset=\"utf-8\">" +
            "<meta name=\"viewport\" content=\"width=device-width,initial-scale=1\">$refresh<title>Bowtie</title>" +
            "<style>body{font-family:-apple-system,system-ui,sans-serif;background:#101418;color:#f2efe8;margin:0;padding:32px 20px}" +
            "main{max-width:420px;margin:0 auto}h1{color:#f0a428;font-size:24px}label{display:block;margin:16px 0 8px}" +
            "input{width:100%;box-sizing:border-box;font-size:18px;padding:12px;border-radius:8px;border:1px solid #9ba5ae;background:#1b2128;color:#f2efe8}" +
            "button{margin-top:16px;width:100%;font-size:18px;padding:12px;border:0;border-radius:8px;background:#f0a428;color:#101418;font-weight:600}" +
            ".err{color:#e05050}.hint{color:#9ba5ae;font-size:14px}</style></head><body><main>$body</main></body></html>"

    fun form(token: String, error: String? = null, value: String = ""): String {
        val err = if (error == null) "" else "<p class=\"err\">${htmlEscape(error)}</p>"
        return page(
            "",
            "<h1>$TITLE</h1>$err<form method=\"post\" action=\"/p\">" +
                "<input type=\"hidden\" name=\"t\" value=\"$token\">" +
                "<label for=\"s\">Your Bowtie server address</label>" +
                "<input id=\"s\" name=\"server\" type=\"url\" inputmode=\"url\" autocapitalize=\"off\" autocorrect=\"off\" " +
                "spellcheck=\"false\" placeholder=\"http://192.168.1.50:8400\" value=\"${htmlEscape(value)}\" required>" +
                "<button type=\"submit\">Continue</button></form>" +
                "<p class=\"hint\">Use the address you open Bowtie with in your browser.</p>",
        )
    }

    fun waiting(token: String): String =
        page("<meta http-equiv=\"refresh\" content=\"1;url=/p/status?t=$token\">", "<h1>Connecting to your TV…</h1>")

    fun forbidden(): String = page("", "<h1>$TITLE</h1><p class=\"err\">$EXPIRED</p>")

    fun notFound(): String = page("", "<h1>Not found</h1>")

    fun badRequest(): String = page("", "<h1>Bad request</h1>")

    fun tooLarge(): String = page("", "<h1>Too large</h1>")
}
