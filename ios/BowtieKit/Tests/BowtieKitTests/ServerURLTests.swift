import XCTest
@testable import BowtieKit

final class ServerURLTests: XCTestCase {
    func testNormalizeAddsHTTPSchemeToHostPort() {
        let url = ServerURL.normalize("192.168.1.50:8400")
        XCTAssertEqual(url?.absoluteString, "http://192.168.1.50:8400")
    }

    func testNormalizeStripsTrailingSlash() {
        let url = ServerURL.normalize("https://tv.example.com/")
        XCTAssertEqual(url?.absoluteString, "https://tv.example.com")
    }

    func testNormalizeRejectsEmpty() {
        XCTAssertNil(ServerURL.normalize(""))
        XCTAssertNil(ServerURL.normalize("   "))
    }

    func testNormalizeRejectsGarbage() {
        XCTAssertNil(ServerURL.normalize("not a url"))
        XCTAssertNil(ServerURL.normalize("://"))
        XCTAssertNil(ServerURL.normalize("http://"))
    }

    func testNormalizePreservesHTTPSAndPort() {
        let url = ServerURL.normalize("https://tv.example.com:8443/")
        XCTAssertEqual(url?.absoluteString, "https://tv.example.com:8443")
    }

    func testResolvePreservesQuery() {
        let base = URL(string: "http://192.168.1.50:8400")!
        let resolved = ServerURL.resolve(
            path: "/api/v1/stream/x/index.m3u8?token=abc",
            against: base
        )
        XCTAssertEqual(resolved.scheme, "http")
        XCTAssertEqual(resolved.host, "192.168.1.50")
        XCTAssertEqual(resolved.port, 8400)
        XCTAssertEqual(resolved.path, "/api/v1/stream/x/index.m3u8")
        XCTAssertEqual(resolved.query, "token=abc")
        XCTAssertEqual(
            resolved.absoluteString,
            "http://192.168.1.50:8400/api/v1/stream/x/index.m3u8?token=abc"
        )
    }
}
