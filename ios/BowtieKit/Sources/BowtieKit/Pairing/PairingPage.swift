import Foundation

/// A complete HTTP/1.1 response for the pairing listener.
public struct PairingResponse: Equatable, Sendable {
    public let status: Int
    public let headers: [(String, String)]
    public let body: Data

    public init(status: Int, headers: [(String, String)], body: Data) {
        self.status = status
        self.headers = headers
        self.body = body
    }

    public static func == (a: PairingResponse, b: PairingResponse) -> Bool {
        a.status == b.status && a.body == b.body
            && a.headers.map { "\($0.0): \($0.1)" } == b.headers.map { "\($0.0): \($0.1)" }
    }

    public func serialized() -> Data {
        var out = "HTTP/1.1 \(status) \(PairingPage.reason(status))\r\n"
        for (name, value) in headers { out += "\(name): \(value)\r\n" }
        out += "\r\n"
        var data = Data(out.utf8)
        data.append(body)
        return data
    }
}

/// The shared pairing page markup (byte-identical across platforms) and responses.
enum PairingPage {
    private static func document(_ body: String, refreshToken: String? = nil) -> String {
        let refresh = refreshToken.map { "<meta http-equiv=\"refresh\" content=\"1;url=/p/status?t=\($0)\">" } ?? ""
        return "<!doctype html><html><head><meta charset=\"utf-8\"><meta name=\"viewport\" content=\"width=device-width,initial-scale=1\">\(refresh)<title>Bowtie</title><style>body{font-family:-apple-system,system-ui,sans-serif;background:#101418;color:#f2efe8;margin:0;padding:32px 20px}main{max-width:420px;margin:0 auto}h1{color:#f0a428;font-size:24px}label{display:block;margin:16px 0 8px}input{width:100%;box-sizing:border-box;font-size:18px;padding:12px;border-radius:8px;border:1px solid #9ba5ae;background:#1b2128;color:#f2efe8}button{margin-top:16px;width:100%;font-size:18px;padding:12px;border:0;border-radius:8px;background:#f0a428;color:#101418;font-weight:600}.err{color:#e05050}.hint{color:#9ba5ae;font-size:14px}</style></head><body><main>\(body)</main></body></html>"
    }

    static func form(token: String, error: String? = nil, value: String = "") -> PairingResponse {
        let err = error.map { "<p class=\"err\">\(escape($0))</p>" } ?? ""
        return status(200, "<h1>Set up Bowtie on your TV</h1>\(err)<form method=\"post\" action=\"/p\"><input type=\"hidden\" name=\"t\" value=\"\(escape(token))\"><label for=\"s\">Your Bowtie server address</label><input id=\"s\" name=\"server\" type=\"url\" inputmode=\"url\" autocapitalize=\"off\" autocorrect=\"off\" spellcheck=\"false\" placeholder=\"http://192.168.1.50:8400\" value=\"\(escape(value))\" required><button type=\"submit\">Continue</button></form><p class=\"hint\">Use the address you open Bowtie with in your browser.</p>")
    }

    static func waiting(token: String) -> PairingResponse {
        response(200, document("<h1>Connecting to your TV…</h1>", refreshToken: token))
    }

    static func expired() -> PairingResponse {
        status(403, "<h1>Set up Bowtie on your TV</h1><p class=\"err\">This pairing code has expired. Scan the code on your TV again.</p>")
    }

    static func notFound() -> PairingResponse { status(404, "<h1>Not found</h1>") }
    static func badRequest() -> PairingResponse { status(400, "<h1>Bad request</h1>") }
    static func tooLarge() -> PairingResponse { status(413, "<h1>Too large</h1>") }

    static func redirect(_ location: String) -> PairingResponse {
        PairingResponse(status: 303, headers: [
            ("Content-Length", "0"), ("Cache-Control", "no-store"), ("Connection", "close"), ("Location", location),
        ], body: Data())
    }

    private static func status(_ code: Int, _ body: String) -> PairingResponse {
        response(code, document(body))
    }

    private static func response(_ code: Int, _ page: String) -> PairingResponse {
        let body = Data(page.utf8)
        return PairingResponse(status: code, headers: [
            ("Content-Type", "text/html; charset=utf-8"), ("Content-Length", String(body.count)),
            ("Cache-Control", "no-store"), ("Connection", "close"),
        ], body: body)
    }

    static func reason(_ code: Int) -> String {
        switch code {
        case 200: return "OK"
        case 303: return "See Other"
        case 400: return "Bad Request"
        case 403: return "Forbidden"
        case 404: return "Not Found"
        case 413: return "Payload Too Large"
        default: return "OK"
        }
    }

    static func escape(_ s: String) -> String {
        var out = ""
        for ch in s {
            switch ch {
            case "&": out += "&amp;"
            case "<": out += "&lt;"
            case ">": out += "&gt;"
            case "\"": out += "&quot;"
            case "'": out += "&#39;"
            default: out.append(ch)
            }
        }
        return out
    }
}
