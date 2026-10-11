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

    private func startListener(token: String, host: String? = "127.0.0.1") async throws -> (PairingListener, UInt16) {
        let listener = PairingListener(session: PairingSession(token: token), host: host, queue: .main) { _ in }
        let port: UInt16 = try await withCheckedThrowingContinuation { c in
            listener.start { p in p.map { c.resume(returning: $0) } ?? c.resume(throwing: URLError(.cannotConnectToHost)) }
        }
        return (listener, port)
    }

    func testSlowDripIsDroppedAfterFiveSecondsAndNextClientIsServed() async throws {
        let t = "0123456789abcdef01234567"
        let (listener, port) = try await startListener(token: t)
        defer { listener.stop() }
        let conn = NWConnection(host: "127.0.0.1", port: NWEndpoint.Port(rawValue: port)!, using: .tcp)
        conn.start(queue: .global())
        let start = Date()
        let closed = expectation(description: "closed")
        conn.receive(minimumIncompleteLength: 1, maximumLength: 1024) { _, _, done, err in
            if done || err != nil { closed.fulfill() }
        }
        let dripper = Task {
            for _ in 0..<12 {
                conn.send(content: Data("x".utf8), completion: .contentProcessed { _ in })
                try? await Task.sleep(nanoseconds: 1_000_000_000)
            }
        }
        await fulfillment(of: [closed], timeout: 8)
        dripper.cancel()
        conn.cancel()
        XCTAssertLessThan(Date().timeIntervalSince(start), 7.5)
        let reply = try await exchange(port: port, parts: ["GET /p?t=\(t) HTTP/1.1\r\n\r\n"])
        XCTAssertTrue(reply.hasPrefix("HTTP/1.1 200 OK\r\n"))
    }

    func testSilentClientIsDroppedAndNextClientIsServed() async throws {
        let t = "0123456789abcdef01234567"
        let (listener, port) = try await startListener(token: t)
        defer { listener.stop() }
        let conn = NWConnection(host: "127.0.0.1", port: NWEndpoint.Port(rawValue: port)!, using: .tcp)
        conn.start(queue: .global())
        let closed = expectation(description: "closed")
        conn.receive(minimumIncompleteLength: 1, maximumLength: 1024) { _, _, done, err in
            if done || err != nil { closed.fulfill() }
        }
        await fulfillment(of: [closed], timeout: 7)
        conn.cancel()
        let reply = try await exchange(port: port, parts: ["GET /p?t=\(t) HTTP/1.1\r\n\r\n"])
        XCTAssertTrue(reply.hasPrefix("HTTP/1.1 200 OK\r\n"))
    }

    func testOversizeAndGarbageRequestsGetErrorsAndListenerSurvives() async throws {
        let t = "0123456789abcdef01234567"
        let (listener, port) = try await startListener(token: t)
        defer { listener.stop() }
        let big = "POST /p HTTP/1.1\r\nContent-Length: 9000\r\n\r\n" + String(repeating: "a", count: 9000)
        let tooLarge = try await exchange(port: port, parts: [big])
        XCTAssertTrue(tooLarge.hasPrefix("HTTP/1.1 413 "))
        let bad = try await exchange(port: port, parts: ["\u{1}\u{2}garbage\r\n\r\n"])
        XCTAssertTrue(bad.hasPrefix("HTTP/1.1 400 "))
        let ok = try await exchange(port: port, parts: ["GET /p?t=\(t) HTTP/1.1\r\n\r\n"])
        XCTAssertTrue(ok.hasPrefix("HTTP/1.1 200 OK\r\n"))
    }

    func testBoundToTheGivenAddressOnly() async throws {
        guard let lan = LocalAddress.lanIPv4() else { throw XCTSkip("no LAN IPv4 address") }
        let (listener, port) = try await startListener(token: "0123456789abcdef01234567", host: lan)
        defer { listener.stop() }
        let conn = NWConnection(host: "127.0.0.1", port: NWEndpoint.Port(rawValue: port)!, using: .tcp)
        let failed = expectation(description: "loopback refused")
        failed.assertForOverFulfill = false
        conn.stateUpdateHandler = { if case .ready = $0 { conn.cancel() } else if case .failed = $0 { failed.fulfill() } else if case .waiting = $0 { failed.fulfill() } }
        conn.start(queue: .global())
        await fulfillment(of: [failed], timeout: 3)
        conn.cancel()
    }

    func testSecondConnectionWaitsItsTurnInsteadOfBeingReset() async throws {
        let t = "0123456789abcdef01234567"
        let (listener, port) = try await startListener(token: t)
        defer { listener.stop() }
        let silent = NWConnection(host: "127.0.0.1", port: NWEndpoint.Port(rawValue: port)!, using: .tcp)
        silent.start(queue: .global())
        try await Task.sleep(nanoseconds: 200_000_000)
        let reply = try await exchange(port: port, parts: ["GET /p?t=\(t) HTTP/1.1\r\n\r\n"])
        silent.cancel()
        XCTAssertTrue(reply.hasPrefix("HTTP/1.1 200 OK\r\n"))
    }

    @MainActor
    func testListenerFailureClearsPairURLAndStartIssuesAFreshOne() async throws {
        let c = PairingCoordinator(connect: { _ in true }, host: { "127.0.0.1" })
        c.start()
        for _ in 0..<50 where c.pairURL == nil { try await Task.sleep(nanoseconds: 20_000_000) }
        let first = try XCTUnwrap(c.pairURL)
        c.listener?.simulateFailure()
        for _ in 0..<50 where c.pairURL != nil { try await Task.sleep(nanoseconds: 20_000_000) }
        XCTAssertNil(c.pairURL)
        c.start()
        for _ in 0..<50 where c.pairURL == nil { try await Task.sleep(nanoseconds: 20_000_000) }
        let second = try XCTUnwrap(c.pairURL)
        XCTAssertNotEqual(second, first)
        c.stop()
    }

    @MainActor
    func testLockoutClearsPairURL() async throws {
        let c = PairingCoordinator(connect: { _ in true }, host: { "127.0.0.1" })
        c.start()
        for _ in 0..<50 where c.pairURL == nil { try await Task.sleep(nanoseconds: 20_000_000) }
        let port = UInt16(try XCTUnwrap(c.pairURL).port!)
        for _ in 0..<20 {
            _ = try await exchange(port: port, parts: ["GET /p?t=ffffffffffffffffffffffff HTTP/1.1\r\n\r\n"])
        }
        for _ in 0..<50 where c.pairURL != nil { try await Task.sleep(nanoseconds: 20_000_000) }
        XCTAssertNil(c.pairURL)
        c.stop()
    }

    func testLanAddressLooksLikeIPv4() {
        if let ip = LocalAddress.lanIPv4() {
            XCTAssertEqual(ip.split(separator: ".").count, 4)
            XCTAssertNotEqual(ip, "127.0.0.1")
        }
    }
}
