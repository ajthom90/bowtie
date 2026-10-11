import XCTest
@testable import BowtieKit

final class PairingSessionTests: XCTestCase {
    private let t = "0123456789abcdef01234567"

    private func get(_ path: String) -> PairingParse { HTTPRequest.parse(Data("GET \(path) HTTP/1.1\r\nHost: tv\r\n\r\n".utf8)) }
    private func post(_ body: String) -> PairingParse {
        HTTPRequest.parse(Data("POST /p HTTP/1.1\r\nContent-Type: application/x-www-form-urlencoded\r\nContent-Length: \(body.utf8.count)\r\n\r\n\(body)".utf8))
    }
    private func text(_ r: PairingResponse) -> String { String(decoding: r.body, as: UTF8.self) }
    private func header(_ r: PairingResponse, _ name: String) -> String? { r.headers.first { $0.0.caseInsensitiveCompare(name) == .orderedSame }?.1 }

    func testFormShowsToken() {
        let s = PairingSession(token: t)
        let (r, sub) = s.handle(get("/p?t=\(t)"))
        XCTAssertEqual(r.status, 200); XCTAssertNil(sub)
        XCTAssertTrue(text(r).contains("<h1>Set up Bowtie on your TV</h1>"))
        XCTAssertTrue(text(r).contains("name=\"t\" value=\"\(t)\""))
        XCTAssertEqual(header(r, "Connection"), "close")
        XCTAssertEqual(header(r, "Content-Length"), String(r.body.count))
    }

    func testPostNormalizesSubmitsOnceAndRedirects() {
        let s = PairingSession(token: t)
        let (r, sub) = s.handle(post("t=\(t)&server=192.168.1.50%3A8400%2F"))
        XCTAssertEqual(r.status, 303)
        XCTAssertEqual(header(r, "Location"), "/p/status?t=\(t)")
        XCTAssertEqual(sub?.absoluteString, "http://192.168.1.50:8400")
        XCTAssertNil(s.handle(post("t=\(t)&server=other")).submitted)
    }

    func testBadAddressReshowsForm() {
        let s = PairingSession(token: t)
        let (r, sub) = s.handle(post("t=\(t)&server=ftp%3A%2F%2Fx"))
        XCTAssertEqual(r.status, 200); XCTAssertNil(sub)
        XCTAssertTrue(text(r).contains("Enter an address like http://192.168.1.50:8400."))
        XCTAssertTrue(text(r).contains("value=\"ftp://x\""))
        XCTAssertEqual(s.state, .waiting)
    }

    func testStatusWaitsThenRedirectsToLatestCode() {
        let s = PairingSession(token: t)
        let server = URL(string: "http://bowtie.lan:8400")!
        _ = s.handle(post("t=\(t)&server=http%3A%2F%2Fbowtie.lan%3A8400"))
        let waiting = s.handle(get("/p/status?t=\(t)")).0
        XCTAssertTrue(text(waiting).contains("Connecting to your TV…"))
        XCTAssertTrue(text(waiting).contains("content=\"1;url=/p/status?t=\(t)\""))
        s.codeIssued(PairingSession.linkURL(server: server, userCode: "BCDF-2345"))
        XCTAssertEqual(header(s.handle(get("/p/status?t=\(t)")).0, "Location"), "http://bowtie.lan:8400/link?code=BCDF2345")
        s.codeIssued(PairingSession.linkURL(server: server, userCode: "GHJK-6789"))
        XCTAssertEqual(header(s.handle(get("/p/status?t=\(t)")).0, "Location"), "http://bowtie.lan:8400/link?code=GHJK6789")
        XCTAssertEqual(header(s.handle(get("/p?t=\(t)")).0, "Location"), "/p/status?t=\(t)")
    }

    func testConnectFailureAllowsRetry() {
        let s = PairingSession(token: t)
        let (_, sub) = s.handle(post("t=\(t)&server=bowtie.lan"))
        s.connectFailed(sub!)
        let page = text(s.handle(get("/p/status?t=\(t)")).0)
        XCTAssertTrue(page.contains("Couldn&#39;t reach a Bowtie server at http://bowtie.lan. Check the address and try again."))
        XCTAssertEqual(s.handle(post("t=\(t)&server=bowtie2.lan")).submitted?.absoluteString, "http://bowtie2.lan")
    }

    func testHTMLInAddressIsRejectedAndEscaped() {
        let (r, sub) = PairingSession(token: t).handle(post("t=\(t)&server=%22%3E%3Cscript%3E"))
        XCTAssertNil(sub)
        XCTAssertFalse(text(r).contains("<script>"))
        XCTAssertTrue(text(r).contains("value=\"&quot;&gt;&lt;script&gt;\""))
    }

    func testLockoutCountsOnlyWrongTokens() {
        let s = PairingSession(token: t)
        for _ in 0..<30 {
            XCTAssertEqual(s.handle(get("/favicon.ico")).0.status, 404)
            XCTAssertEqual(s.handle(get("/p")).0.status, 403)
        }
        for _ in 0..<19 { _ = s.handle(get("/p?t=nope")) }
        XCTAssertEqual(s.handle(get("/p?t=\(t)")).0.status, 200)
        _ = s.handle(get("/p/status?t=nope"))
        XCTAssertEqual(s.handle(get("/p?t=\(t)")).0.status, 403)
        XCTAssertEqual(s.state, .closed)
    }

    func testParserLimits() {
        XCTAssertEqual(HTTPRequest.parse(Data("hello\r\n\r\n".utf8)), .bad)
        XCTAssertEqual(HTTPRequest.parse(Data("GET /p HTTP/1.1\r\nHost: x".utf8)), .incomplete)
        let big = "t=" + String(repeating: "a", count: 9000)
        XCTAssertEqual(HTTPRequest.parse(Data("POST /p HTTP/1.1\r\nContent-Length: \(big.count)\r\n\r\n\(big)".utf8)), .tooLarge)
        XCTAssertEqual(HTTPRequest.parse(Data("POST /p HTTP/1.1\r\nContent-Length: 10\r\n\r\nt=1".utf8)), .incomplete)
        XCTAssertEqual(PairingSession(token: t).handle(HTTPRequest.parse(Data("PUT /p?t=\(t) HTTP/1.1\r\n\r\n".utf8))).0.status, 404)
    }

    func testFormDecodingAndToken() {
        guard case .complete(let r) = HTTPRequest.parse(Data("GET /p?a=x+y%2Fz%3a HTTP/1.1\r\n\r\n".utf8)) else { return XCTFail() }
        XCTAssertEqual(r.query["a"], "x y/z:")
        let tok = PairingSession.newToken()
        XCTAssertEqual(tok.count, 24)
        XCTAssertTrue(tok.allSatisfy { "0123456789abcdef".contains($0) })
    }
}
