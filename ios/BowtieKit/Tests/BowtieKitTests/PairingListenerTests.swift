import XCTest
import Network
@testable import BowtieKit

final class PairingListenerTests: XCTestCase {
    /// Raw TCP client so we control how the request is split.
    private func exchange(port: UInt16, parts: [String], gap: TimeInterval = 0.1) async throws -> String {
        let conn = NWConnection(host: "127.0.0.1", port: NWEndpoint.Port(rawValue: port)!, using: .tcp)
        conn.start(queue: .global())
        for (i, p) in parts.enumerated() {
            if i > 0 { try await Task.sleep(nanoseconds: UInt64(gap * 1_000_000_000)) }
            conn.send(content: Data(p.utf8), completion: .contentProcessed { _ in })
        }
        return try await withCheckedThrowingContinuation { cont in
            var acc = Data()
            func read() {
                conn.receive(minimumIncompleteLength: 1, maximumLength: 65536) { data, _, done, err in
                    if let data { acc.append(data) }
                    if done || err != nil { conn.cancel(); cont.resume(returning: String(decoding: acc, as: UTF8.self)) } else { read() }
                }
            }
            read()
        }
    }

    func testBodyInASecondWriteStillSubmits() async throws {
        let t = "0123456789abcdef01234567"
        let session = PairingSession(token: t)
        let submitted = expectation(description: "submit")
        let listener = PairingListener(session: session, queue: .main) { url in
            XCTAssertEqual(url.absoluteString, "http://bowtie.lan:8400"); submitted.fulfill()
        }
        let port: UInt16 = try await withCheckedThrowingContinuation { c in
            listener.start { p in p.map { c.resume(returning: $0) } ?? c.resume(throwing: URLError(.cannotConnectToHost)) }
        }
        defer { listener.stop() }
        let body = "t=\(t)&server=bowtie.lan%3A8400"
        let reply = try await exchange(port: port, parts: [
            "POST /p HTTP/1.1\r\nContent-Type: application/x-www-form-urlencoded\r\nContent-Length: \(body.utf8.count)\r\n\r\n",
            body,
        ])
        XCTAssertTrue(reply.hasPrefix("HTTP/1.1 303 See Other\r\n"))
        await fulfillment(of: [submitted], timeout: 2)
    }

    @MainActor
    func testCoordinatorRedirectsOnlyForTheSubmittedServer() async throws {
        var connected: [String] = []
        let c = PairingCoordinator(connect: { connected.append($0); return true }, host: { "127.0.0.1" })
        c.start()
        for _ in 0..<50 where c.pairURL == nil { try await Task.sleep(nanoseconds: 20_000_000) }
        let pair = try XCTUnwrap(c.pairURL)
        let port = UInt16(pair.port!)
        let token = URLComponents(url: pair, resolvingAgainstBaseURL: false)!.queryItems!.first { $0.name == "t" }!.value!
        let body = "t=\(token)&server=http%3A%2F%2Fbowtie.lan%3A8400"
        _ = try await exchange(port: port, parts: ["POST /p HTTP/1.1\r\nContent-Length: \(body.utf8.count)\r\n\r\n\(body)"])
        for _ in 0..<50 where connected.isEmpty { try await Task.sleep(nanoseconds: 20_000_000) }
        XCTAssertEqual(connected, ["http://bowtie.lan:8400"])
        c.codeIssued(server: URL(string: "http://elsewhere:1")!, userCode: "AAAA-BBBB")
        let waiting = try await exchange(port: port, parts: ["GET /p/status?t=\(token) HTTP/1.1\r\n\r\n"])
        XCTAssertTrue(waiting.contains("Connecting to your TV"))
        c.codeIssued(server: URL(string: "http://bowtie.lan:8400")!, userCode: "BCDF-2345")
        let ready = try await exchange(port: port, parts: ["GET /p/status?t=\(token) HTTP/1.1\r\n\r\n"])
        XCTAssertTrue(ready.contains("Location: http://bowtie.lan:8400/link?code=BCDF2345\r\n"))
        c.stop()
        XCTAssertNil(c.pairURL)
    }

    @MainActor
    func testStartAfterLockoutIssuesANewTokenAndURL() async throws {
        let c = PairingCoordinator(connect: { _ in true }, host: { "127.0.0.1" })
        c.start()
        for _ in 0..<50 where c.pairURL == nil { try await Task.sleep(nanoseconds: 20_000_000) }
        let first = try XCTUnwrap(c.pairURL)
        c.start()   // live session: no-op
        XCTAssertEqual(c.pairURL, first)

        let port = UInt16(first.port!)
        for _ in 0..<20 {
            _ = try await exchange(port: port, parts: ["GET /p?t=ffffffffffffffffffffffff HTTP/1.1\r\n\r\n"])
        }
        c.start()
        for _ in 0..<50 where c.pairURL == nil || c.pairURL == first { try await Task.sleep(nanoseconds: 20_000_000) }
        let second = try XCTUnwrap(c.pairURL)
        XCTAssertNotEqual(second, first)

        let token = URLComponents(url: second, resolvingAgainstBaseURL: false)!.queryItems!.first { $0.name == "t" }!.value!
        let form = try await exchange(port: UInt16(second.port!), parts: ["GET /p?t=\(token) HTTP/1.1\r\n\r\n"])
        XCTAssertTrue(form.hasPrefix("HTTP/1.1 200 OK\r\n"))
        c.stop()
    }

    func testLanAddressLooksLikeIPv4() {
        if let ip = LocalAddress.lanIPv4() {
            XCTAssertEqual(ip.split(separator: ".").count, 4)
            XCTAssertNotEqual(ip, "127.0.0.1")
        }
    }
}
