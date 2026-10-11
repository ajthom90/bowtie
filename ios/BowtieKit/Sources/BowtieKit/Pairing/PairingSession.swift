import Foundation
import Security

/// State machine behind the TV's pairing page: request in, response out, no sockets.
/// Used only on its owner's actor.
public final class PairingSession: @unchecked Sendable {
    public enum State: Equatable {
        case waiting
        case connecting(URL)
        case failed(URL, String)
        case ready(URL)
        case closed
    }

    private static let maxBadTokens = 20

    public let token: String
    public private(set) var state: State = .waiting
    private var badTokenCount = 0

    public init(token: String = PairingSession.newToken()) {
        self.token = token
    }

    /// 96 bits from the system CSPRNG, as 24 lowercase hex characters.
    public static func newToken() -> String {
        var bytes = [UInt8](repeating: 0, count: 12)
        guard SecRandomCopyBytes(kSecRandomDefault, bytes.count, &bytes) == errSecSuccess else {
            return (0..<24).map { _ in String(Int.random(in: 0..<16), radix: 16) }.joined()
        }
        return bytes.map { String(format: "%02x", $0) }.joined()
    }

    /// `server` + `/link?code=` + the user code without dashes. Nil unless the server's code is
    /// letters, digits and dashes, so nothing it sends can reach the `Location` header.
    public static func linkURL(server: URL, userCode: String) -> URL? {
        guard !userCode.isEmpty, userCode.unicodeScalars.allSatisfy({ ($0.isASCII && CharacterSet.alphanumerics.contains($0)) || $0 == "-" }) else {
            return nil
        }
        let code = userCode.replacingOccurrences(of: "-", with: "")
        return URL(string: server.absoluteString + "/link?code=" + code)
    }

    public func handle(_ parse: PairingParse) -> (PairingResponse, submitted: URL?) {
        if state == .closed { return (PairingPage.expired(), nil) }
        let request: HTTPRequest
        switch parse {
        case .tooLarge: return (PairingPage.tooLarge(), nil)
        case .bad, .incomplete: return (PairingPage.badRequest(), nil)
        case .complete(let r): request = r
        }

        let isPost = request.method == "POST" && request.path == "/p"
        let isGet = request.method == "GET" && (request.path == "/p" || request.path == "/p/status")
        guard isPost || isGet else { return (PairingPage.notFound(), nil) }

        let supplied = isPost ? request.form["t"] : request.query["t"]
        guard let supplied else { return (PairingPage.expired(), nil) }
        guard Self.constantTimeEqual(supplied, token) else {
            badTokenCount += 1
            if badTokenCount >= Self.maxBadTokens { state = .closed }
            return (PairingPage.expired(), nil)
        }

        let statusPath = "/p/status?t=\(token)"
        if isPost { return submit(request.form["server"] ?? "", statusPath: statusPath) }
        return request.path == "/p" ? showForm(statusPath: statusPath) : showStatus()
    }

    public func connectFailed(_ address: URL) {
        guard case .connecting(let current) = state, current == address else { return }
        state = .failed(address, "Couldn't reach a Bowtie server at \(address.absoluteString). Check the address and try again.")
    }

    public func codeIssued(_ link: URL) {
        switch state {
        case .connecting, .ready: state = .ready(link)
        default: break
        }
    }

    public func close() {
        state = .closed
    }

    // MARK: - Routes

    private func showForm(statusPath: String) -> (PairingResponse, submitted: URL?) {
        switch state {
        case .waiting: return (PairingPage.form(token: token), nil)
        case .failed(let address, let message): return (PairingPage.form(token: token, error: message, value: address.absoluteString), nil)
        default: return (PairingPage.redirect(statusPath), nil)
        }
    }

    private func submit(_ raw: String, statusPath: String) -> (PairingResponse, submitted: URL?) {
        switch state {
        case .waiting, .failed:
            guard let address = ServerURL.normalize(raw) else {
                return (PairingPage.form(token: token, error: "Enter an address like http://192.168.1.50:8400.", value: raw), nil)
            }
            state = .connecting(address)
            return (PairingPage.redirect(statusPath), address)
        default:
            return (PairingPage.redirect(statusPath), nil)
        }
    }

    private func showStatus() -> (PairingResponse, submitted: URL?) {
        switch state {
        case .connecting: return (PairingPage.waiting(token: token), nil)
        case .failed(let address, let message): return (PairingPage.form(token: token, error: message, value: address.absoluteString), nil)
        case .ready(let link): return (PairingPage.redirect(link.absoluteString), nil)
        default: return (PairingPage.redirect("/p?t=\(token)"), nil)
        }
    }

    private static func constantTimeEqual(_ a: String, _ b: String) -> Bool {
        let x = Array(a.utf8), y = Array(b.utf8)
        guard x.count == y.count else { return false }
        return zip(x, y).reduce(UInt8(0)) { $0 | ($1.0 ^ $1.1) } == 0
    }
}
